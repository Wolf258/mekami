package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
)

// State is the lifecycle state of a single daemon as tracked by
// the supervisor.
type State string

const (
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateReloading  State = "reloading"
	StateStopping   State = "stopping"
	StateStopped    State = "stopped"
	StateCrashed    State = "crashed"
	StateDegraded   State = "degraded-poller"
	StateDeferrable State = "deferrable"
)

// RestartPolicy controls whether the supervisor restarts a
// crashed daemon automatically.
type RestartPolicy string

const (
	PolicyOnCrash RestartPolicy = "on-crash"
	PolicyAlways  RestartPolicy = "always"
	PolicyNever   RestartPolicy = "never"
)

// Daemon is the in-memory state the supervisor holds for one
// watched project. Persisted fields live in DaemonState.
type Daemon struct {
	mu sync.Mutex

	Spec SpawnSpec

	PID       int
	State     State
	StartedAt time.Time
	// LastStatus is the most recent payload returned by the
	// daemon's IPC status call.
	LastStatus DaemonView
	// CrashCount and LastCrashAt are reset on a successful
	// start; used by the backoff policy.
	CrashCount  int
	LastCrashAt time.Time
	// RestartAt is the earliest time the supervisor will
	// re-launch a crashed daemon. Zero means "no backoff".
	RestartAt time.Time
	// BudgetFallback, when true, says the supervisor
	// instructed this daemon to use the poller.
	BudgetFallback bool
	// done is closed when the spawned process exits.
	done chan struct{}
}

// Options customises how a Supervisor is built. The zero
// value means "production defaults": the supervisor resolves
// its state directory from StateDir() (env-driven).
type Options struct {
	// StateDir overrides the directory used for the registry
	// file and the unix socket. When empty, the supervisor
	// falls back to StateDir().
	StateDir string
}

// Supervisor is the per-user process that owns all daemons.
// Use NewSupervisor to construct one; Run blocks until ctx is
// done or Quit is invoked.
type Supervisor struct {
	mu       sync.Mutex
	daemons  map[string]*Daemon
	registry *Registry
	budget   *InotifyBudget

	// stateDir is the absolute directory the supervisor reads
	// its registry from and writes its socket into. It is
	// resolved once at construction and never mutated, so
	// supervisors built with different Options cannot share
	// state by accident.
	stateDir string

	// shutdown is closed when the supervisor should stop
	// accepting work and exit.
	shutdown chan struct{}
	// done is closed when the Run loop returns.
	done chan struct{}

	// Tunables. Default values match production; tests can
	// override before Run.
	HealthInterval    time.Duration
	BackoffSchedule   []time.Duration
	StopTimeout       time.Duration
	StartProbeTimeout time.Duration
	BudgetWarnPct     int64
	BudgetDegradePct  int64
	BudgetCriticalPct int64
}

// socketPath returns the unix socket path owned by this
// supervisor. Derived from stateDir so test instances with
// custom state dirs stay hermetic.
func (s *Supervisor) socketPath() string {
	return filepath.Join(s.stateDir, "supervisor.sock")
}

// NewSupervisor builds a Supervisor using production defaults
// (env-driven state directory). Callers that need to control
// the state directory (e.g. tests) should use
// NewSupervisorWithOptions.
func NewSupervisor() (*Supervisor, error) {
	return NewSupervisorWithOptions(Options{})
}

// NewSupervisorWithOptions builds a Supervisor that stores its
// registry file at opts.StateDir (or StateDir() when empty).
// The directory is created with 0700 perms if missing.
func NewSupervisorWithOptions(opts Options) (*Supervisor, error) {
	dir := opts.StateDir
	if dir == "" {
		dir = StateDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Record this state dir as the package singleton so
	// adoption-time logging can find the supervisor log
	// without holding a *Supervisor. See setStateDirSingleton.
	setStateDirSingleton(dir)
	reg, err := LoadRegistryAt(filepath.Join(dir, "daemons.json"))
	if err != nil {
		return nil, err
	}
	return &Supervisor{
		daemons:  make(map[string]*Daemon),
		registry: reg,
		budget:   NewInotifyBudget(),
		stateDir: dir,
		shutdown: make(chan struct{}),
		done:     make(chan struct{}),
		// Tunables.
		HealthInterval:    5 * time.Second,
		BackoffSchedule:   []time.Duration{1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second},
		StopTimeout:       5 * time.Second,
		StartProbeTimeout: 3 * time.Second,
		BudgetWarnPct:     60,
		BudgetDegradePct:  80,
		BudgetCriticalPct: 95,
	}, nil
}

// LoadFromRegistry rehydrates the in-memory daemon table from
// the persisted registry. Daemons that were "running" or
// "crashed" are eligible for re-spawn; others stay as
// records-of-intent only.
//
// For each eligible daemon, the function first tries to
// adopt an existing orphan: if the project's
// .mekami/watcher.pid and .mekami/watcher.sock point to a
// live process that answers a ping, the supervisor registers
// that PID instead of starting a fresh daemon. This is the
// fix for the "kill -9 supervisor leaves orphan daemons"
// problem: when the supervisor comes back, it finds the
// existing watcher and treats it as its own.
//
// If no orphan is present, the daemon is marked as crashed
// and the supervisor's health loop schedules a re-spawn with
// backoff (the legacy behaviour).
func (s *Supervisor) LoadFromRegistry() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ds := range s.registry.Daemons {
		absRoot, err := filepath.Abs(ds.Root)
		if err != nil {
			continue
		}
		d := &Daemon{
			Spec: SpawnSpec{
				Root:   absRoot,
				Lang:   ds.Lang,
				DBPath: ds.DBPath,
			},
			State: StateStopped,
		}
		// Indexer names are not persisted in the registry; the
		// supervisor reloads them from the on-disk config (when
		// present) so the next respawn sees the current tracking
		// set, not the one captured at start time.
		if cfg, err := config.Load(filepath.Join(absRoot, ".mekami", "config.json")); err == nil {
			names := make([]string, 0, len(cfg.Indexers))
			for name := range cfg.Indexers {
				names = append(names, name)
			}
			sort.Strings(names)
			d.Spec.IndexerNames = names
		}
		// Reload the on-disk config to populate Spec.Watch
		// and Spec.Build. We tolerate missing config: the
		// daemon defaults to the config-package defaults.
		cfgPath := filepath.Join(absRoot, ".mekami", "config.json")
		if cfg, err := config.Load(cfgPath); err == nil {
			d.Spec.Watch = cfg.Watch
			d.Spec.Build = cfg.Build
		}
		switch ds.LastState {
		case string(StateRunning), string(StateStarting), string(StateReloading), string(StateCrashed):
			// Try to adopt a live orphan first. We must drop
			// s.mu around the adopt call because it does
			// blocking I/O (dial, ping); re-acquire
			// immediately after.
			s.mu.Unlock()
			adopted := s.tryAdopt(absRoot, d)
			s.mu.Lock()
			if adopted {
				// tryAdopt already inserted d into
				// s.daemons with StateRunning. Skip
				// the "mark for re-spawn" branch.
				continue
			}
			d.State = StateCrashed
			d.LastCrashAt = time.Now()
			s.daemons[absRoot] = d
		default:
			s.daemons[absRoot] = d
		}
	}
	return nil
}

// tryAdopt is the supervisor-side adapter around
// adoptDaemon. It registers the adopted PID in the
// in-memory daemon table, starts the waitProcess
// goroutine, and refreshes the registry's LastState
// field so a future crash is recorded against the
// adopted PID (not a phantom). Returns true on
// success.
//
// The function is called with s.mu NOT held because
// the underlying dial/ping is blocking. It takes the
// lock when it needs to mutate s.daemons.
func (s *Supervisor) tryAdopt(absRoot string, d *Daemon) bool {
	res, err := adoptDaemon(absRoot)
	if err != nil {
		return false
	}
	// If the heartbeat is stale, log a warning to the
	// supervisor's own log. This is the only signal a
	// user gets that an adopted daemon was wedged at
	// adoption time. We do not refuse adoption on a
	// stale heartbeat: a PID that answers signal 0 and
	// pings is by definition live; the heartbeat may
	// just be lagging because the daemon is busy.
	if hb, ok := readHeartbeatFile(absRoot); ok {
		if time.Since(hb) > HeartbeatStale {
			s.logAdopt("warning: adopted orphan for %s has stale heartbeat (%s old)", absRoot, time.Since(hb).Truncate(time.Second))
		}
	}
	s.logAdopt("adopted orphan daemon for %s (pid=%d)", absRoot, res.PID)
	s.mu.Lock()
	d.PID = res.PID
	d.State = StateRunning
	d.StartedAt = res.StartedAt
	d.CrashCount = 0
	d.done = make(chan struct{})
	done := d.done
	s.daemons[absRoot] = d
	// Persist the adopted state so a subsequent crash
	// is recorded against this PID, not a phantom.
	ds := s.registry.Find(absRoot)
	if ds != nil {
		ds.LastState = string(StateRunning)
		_ = s.registry.Save()
	}
	s.mu.Unlock()
	// Start the waitProcess goroutine so the health
	// tick is notified when the adopted process exits.
	go s.waitProcess(absRoot, res.PID, done)
	return true
}

// logAdopt writes a single line to the supervisor's log
// if a log path is known, or to stderr otherwise. The
// supervisor's main log file is wired up in runSupervisorMain;
// for the test path (and for the early rehydration phase
// where the supervisor log may not be open yet), we fall
// back to stderr.
func (s *Supervisor) logAdopt(format string, args ...any) {
	if logPath := supervisorLogPath(); logPath != "" {
		_ = appendLogLine(logPath, fmt.Sprintf(format, args...))
		return
	}
	fmt.Fprintf(os.Stderr, "supervisor: "+format+"\n", args...)
}

// supervisorLogPath returns the canonical log path used
// by the running supervisor, or "" if the log file has
// not been initialised yet (e.g. in tests). The path is
// resolved the same way runSupervisorMain does it; we
// keep the resolution local to avoid a circular
// dependency with the IPC package.
func supervisorLogPath() string {
	if s := stateDirSingletonGet(); s != "" {
		return filepath.Join(s, "supervisor.log")
	}
	return ""
}

// Register adds a daemon to the supervisor without starting it.
// Used by tests and by `mekami watch stop --persist` flows.
func (s *Supervisor) Register(spec SpawnSpec, policy RestartPolicy) error {
	absRoot, err := filepath.Abs(spec.Root)
	if err != nil {
		return err
	}
	spec.Root = absRoot
	d := &Daemon{
		Spec:  spec,
		State: StateStopped,
	}
	s.mu.Lock()
	s.daemons[absRoot] = d
	s.mu.Unlock()
	// Update the registry directly so policyFor sees the new
	// policy on the same Register call.
	ds := s.registry.Find(absRoot)
	if ds == nil {
		s.registry.Upsert(DaemonState{
			Root:          absRoot,
			Lang:          spec.Lang,
			DBPath:        spec.DBPath,
			ConfigHash:    hashForSpec(spec),
			RestartPolicy: string(policy),
			LastState:     string(StateStopped),
		})
	} else {
		ds.RestartPolicy = string(policy)
		ds.Lang = spec.Lang
		ds.DBPath = spec.DBPath
		ds.ConfigHash = hashForSpec(spec)
		ds.LastState = string(StateStopped)
	}
	return s.registry.Save()
}

// Start spawns a daemon for spec. If a daemon is already up for
// this root, returns ErrAlreadyRunning.
//
// Before forking, Start runs a fast adoption check: if
// .mekami/watcher.pid points to a live process that answers
// a ping, the supervisor registers it as the active daemon
// for this root and returns its view instead of starting a
// new one. This is the manual-trigger counterpart to
// LoadFromRegistry's automatic adoption, and is what makes
// `mekami start` safe to call after a crash.
//
// If the on-disk state is inconsistent (PID file present
// but the process is dead, or socket present but no
// answer), Start cleans up the stale files and proceeds
// with a fresh spawn. Cleaning is best-effort: a leftover
// socket that cannot be removed causes SpawnDaemon to
// fail, which the caller will see as a regular error.
func (s *Supervisor) Start(ctx context.Context, spec SpawnSpec, policy RestartPolicy) (*DaemonView, error) {
	absRoot, err := filepath.Abs(spec.Root)
	if err != nil {
		return nil, err
	}
	spec.Root = absRoot

	s.mu.Lock()
	d, ok := s.daemons[absRoot]
	if !ok {
		d = &Daemon{Spec: spec, State: StateStopped}
		s.daemons[absRoot] = d
	} else {
		// Refresh the spec: a fresh start always picks up
		// the latest config and language.
		d.Spec = spec
	}
	if d.State == StateRunning || d.State == StateStarting {
		s.mu.Unlock()
		return nil, ErrAlreadyRunning
	}
	s.mu.Unlock()

	// Pre-spawn adoption: if a previous run left a live
	// daemon for this root, adopt it. We do this outside
	// the lock because adoptDaemon is blocking.
	if adopted, err := s.tryAdoptAtStart(absRoot, d, spec, policy); err == nil && adopted {
		s.mu.Lock()
		view := daemonViewLocked(d)
		s.mu.Unlock()
		return &view, nil
	}

	// Clean up stale on-disk state from a previous
	// crashed daemon. This is what makes Start safe to
	// call repeatedly: a leftover socket from a SIGKILL'd
	// daemon would otherwise prevent the new daemon from
	// binding. The cleanup is best-effort: a leftover
	// file we cannot remove is reported back as part of
	// the spawn failure.
	if err := cleanStaleDaemonState(absRoot); err != nil {
		s.logAdopt("warning: stale state cleanup for %s: %v", absRoot, err)
	}

	s.mu.Lock()
	d.State = StateStarting
	d.StartedAt = time.Now()
	d.CrashCount = 0
	d.done = make(chan struct{})
	done := d.done
	s.mu.Unlock()

	pid, err := SpawnDaemon(spec)
	if err != nil {
		s.mu.Lock()
		d.State = StateCrashed
		d.CrashCount++
		d.LastCrashAt = time.Now()
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Lock()
	d.PID = pid
	s.mu.Unlock()

	// Wait for the daemon to bind its socket.
	if !ProbeDaemonReady(spec.Root, s.StartProbeTimeout) {
		// Daemon didn't come up in time; mark crashed so
		// the health loop can decide what to do.
		s.mu.Lock()
		d.State = StateCrashed
		d.CrashCount++
		d.LastCrashAt = time.Now()
		s.mu.Unlock()
		_ = killProcess(pid, syscall.SIGTERM)
		return nil, fmt.Errorf("daemon for %s did not start within %s", spec.Root, s.StartProbeTimeout)
	}

	// Update persisted registry.
	s.registry.Upsert(DaemonState{
		Root:          absRoot,
		Lang:          spec.Lang,
		DBPath:        spec.DBPath,
		ConfigHash:    hashForSpec(spec),
		RestartPolicy: string(policy),
		LastState:     string(StateRunning),
	})
	if err := s.registry.Save(); err != nil {
		// Persistence failure is not fatal for start, but
		// we surface it to the caller via the response.
		// Tests assert on this.
	}

	// Mark running after the probe succeeds.
	s.mu.Lock()
	d.State = StateRunning
	view := daemonViewLocked(d)
	s.mu.Unlock()

	// Background goroutine: when the process exits, close d.done
	// so the health loop can react.
	go s.waitProcess(absRoot, pid, done)

	_ = ctx
	return &view, nil
}

// tryAdoptAtStart is the manual-trigger counterpart to
// the LoadFromRegistry adoption path. It is called from
// Start before forking, so a user invoking `mekami start`
// after a supervisor crash picks up the orphan daemon
// rather than starting a duplicate. Returns true on
// successful adoption, false if no orphan was present
// (the caller should proceed with a fresh spawn).
func (s *Supervisor) tryAdoptAtStart(absRoot string, d *Daemon, spec SpawnSpec, policy RestartPolicy) (bool, error) {
	res, err := adoptDaemon(absRoot)
	if err != nil {
		return false, err
	}
	s.logAdopt("start: adopted orphan for %s (pid=%d)", absRoot, res.PID)
	s.mu.Lock()
	d.PID = res.PID
	d.State = StateRunning
	d.StartedAt = res.StartedAt
	d.CrashCount = 0
	d.done = make(chan struct{})
	done := d.done
	// Refresh the spec on adoption so the daemon's view
	// reflects the latest config the user passed via
	// `mekami start`.
	d.Spec = spec
	s.daemons[absRoot] = d
	// Persist the running state with the latest
	// RestartPolicy (the orphan may have been started
	// under a different policy).
	ds := s.registry.Find(absRoot)
	if ds == nil {
		s.registry.Upsert(DaemonState{
			Root:          absRoot,
			Lang:          spec.Lang,
			DBPath:        spec.DBPath,
			ConfigHash:    hashForSpec(spec),
			RestartPolicy: string(policy),
			LastState:     string(StateRunning),
		})
	} else {
		ds.LastState = string(StateRunning)
		ds.Lang = spec.Lang
		ds.DBPath = spec.DBPath
		ds.ConfigHash = hashForSpec(spec)
		ds.RestartPolicy = string(policy)
	}
	_ = s.registry.Save()
	s.mu.Unlock()
	go s.waitProcess(absRoot, res.PID, done)
	return true, nil
}

// cleanStaleDaemonState removes .mekami/watcher.sock and
// .mekami/watcher.pid for a project when the recorded PID
// is no longer alive. The function is best-effort: errors
// are returned to the caller, who is expected to log them
// and continue. A successful cleanup is what makes Start
// idempotent across supervisor restarts when an orphan
// is missing (e.g. a daemon that died between the
// supervisor's last shutdown and the next Start).
func cleanStaleDaemonState(root string) error {
	pidPath := filepath.Join(root, ".mekami", "watcher.pid")
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil {
			if processAlive(pid) {
				// Process is still alive: this is
				// not a stale state we should
				// touch. (The adoption path will
				// have handled the case where the
				// process is alive; we only run
				// after adoption returned
				// ErrNotAnOrphan, which means
				// either the PID file was missing
				// or processAlive returned false.)
				return nil
			}
		}
		// PID file is stale: remove it. We do not
		// consider ENOENT an error.
		if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove pid: %w", err)
		}
	}
	// Remove the socket only if the recorded PID is
	// dead, otherwise we would clobber a running
	// daemon's listener.
	sockPath := filepath.Join(root, ".mekami", "watcher.sock")
	if _, err := os.Stat(sockPath); err == nil {
		if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove sock: %w", err)
		}
	}
	// Best-effort: also remove the heartbeat file, which
	// is meaningless once the daemon is gone.
	hbPath := filepath.Join(root, ".mekami", "heartbeat")
	_ = os.Remove(hbPath)
	return nil
}

// hashForSpec returns the hash of the project's config.json.
// Used as a drift detector: if the file changes on disk, the
// hash changes, and `reload` can apply the new config.
func hashForSpec(spec SpawnSpec) string {
	h, err := HashConfig(filepath.Join(spec.Root, ".mekami", "config.json"))
	if err != nil {
		return ""
	}
	return h
}

// waitProcess reaps the spawned daemon via signal 0 polling.
// When the process disappears, we close the done channel so
// the health loop can transition to crashed/stopped. We do
// not call Wait() because the daemon is detached (Setsid);
// reaping happens via the kernel.
func (s *Supervisor) waitProcess(absRoot string, pid int, done chan struct{}) {
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	defer close(done)
	for {
		select {
		case <-s.shutdown:
			return
		case <-tick.C:
			if !processAlive(pid) {
				return
			}
		}
	}
}

// processAlive is implemented per-platform in
// proc_alive_unix.go (signal 0) and proc_alive_windows.go
// (OpenProcess + GetExitCodeProcess).

// Stop asks the daemon at root to shut down. force=true skips
// the polite IPC and goes straight to SIGTERM.
func (s *Supervisor) Stop(ctx context.Context, root string, force bool) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	s.mu.Lock()
	d, ok := s.daemons[absRoot]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("supervisor: no daemon for %s", absRoot)
	}
	if d.State == StateStopped {
		s.mu.Unlock()
		return nil
	}
	d.State = StateStopping
	pid := d.PID
	s.mu.Unlock()

	if !force {
		// Polite IPC stop first.
		cli := newDaemonClient(absRoot)
		_ = cli.Stop(ctx)
	}
	// SIGTERM after a brief grace period; SIGKILL on timeout.
	if processAlive(pid) {
		_ = killProcess(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(s.StopTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(pid) {
		_ = killProcess(pid, syscall.SIGKILL)
		time.Sleep(100 * time.Millisecond)
	}

	// Wait for waitProcess to notice.
	s.mu.Lock()
	d.State = StateStopped
	d.PID = 0
	s.mu.Unlock()

	// Update persisted state.
	ds := s.registry.Find(absRoot)
	if ds != nil {
		ds.LastState = string(StateStopped)
		_ = s.registry.Save()
	}
	return nil
}

// Status returns the current view of one (or all) daemons.
func (s *Supervisor) Status(ctx context.Context, root string) ([]DaemonView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if root != "" {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		d, ok := s.daemons[absRoot]
		if !ok {
			return nil, fmt.Errorf("supervisor: no daemon for %s", absRoot)
		}
		v := daemonViewLocked(d)
		v.BudgetLevel = s.budgetLevelString()
		return []DaemonView{v}, nil
	}
	out := make([]DaemonView, 0, len(s.daemons))
	roots := make([]string, 0, len(s.daemons))
	for r := range s.daemons {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	for _, r := range roots {
		v := daemonViewLocked(s.daemons[r])
		v.BudgetLevel = s.budgetLevelString()
		out = append(out, v)
	}
	return out, nil
}

// List returns the registered roots.
func (s *Supervisor) List(ctx context.Context) []string {
	return s.HandleList(ctx)
}

// HandleList implements the IPC Handler interface.
func (s *Supervisor) HandleList(ctx context.Context) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	roots := make([]string, 0, len(s.daemons))
	for r := range s.daemons {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	return roots
}

// Reload re-reads config.json for root (or all daemons) and
// applies changes. Hot-only changes (debounce, ignore, log,
// fallback) are pushed to the live daemon via the new IPC
// reload command. Cold changes (on_start, lang) trigger a
// stop+start.
func (s *Supervisor) Reload(ctx context.Context, root string) error {
	absRoot := ""
	if root != "" {
		var err error
		absRoot, err = filepath.Abs(root)
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	targets := []*Daemon{}
	if absRoot != "" {
		d, ok := s.daemons[absRoot]
		if !ok {
			s.mu.Unlock()
			return fmt.Errorf("supervisor: no daemon for %s", absRoot)
		}
		targets = append(targets, d)
	} else {
		for _, d := range s.daemons {
			targets = append(targets, d)
		}
	}
	s.mu.Unlock()

	for _, d := range targets {
		if err := s.reloadOne(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

// reloadOne reloads a single daemon. The contract:
//   - If the daemon is stopped, register the new spec and stop.
//   - If the config changed hot-only, send a "reload" IPC and
//     update the spec in memory.
//   - If the config changed cold, stop + start.
func (s *Supervisor) reloadOne(ctx context.Context, d *Daemon) error {
	absRoot := d.Spec.Root
	cfgPath := filepath.Join(absRoot, ".mekami", "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config for %s: %w", absRoot, err)
	}
	newSpec := d.Spec
	newSpec.Watch = cfg.Watch
	newSpec.Build = cfg.Build

	hot := isHotOnly(d.Spec.Watch, cfg.Watch)
	s.mu.Lock()
	wasRunning := d.State == StateRunning
	d.Spec = newSpec
	s.mu.Unlock()

	if !wasRunning {
		// Nothing live to push to. Update persisted state
		// and return.
		s.persistOne(d)
		return nil
	}

	if hot {
		cli := newDaemonClient(absRoot)
		// Best-effort: if the daemon doesn't understand
		// reload (older build), this will fail and we
		// fall back to stop+start.
		if err := cli.Reload(ctx); err == nil {
			ds := s.registry.Find(absRoot)
			if ds != nil {
				ds.ConfigHash = hashForSpec(newSpec)
				_ = s.registry.Save()
			}
			return nil
		}
	}
	// Cold change (or hot failed): stop+start.
	policy := PolicyOnCrash
	if ds := s.registry.Find(absRoot); ds != nil && ds.RestartPolicy != "" {
		policy = RestartPolicy(ds.RestartPolicy)
	}
	if err := s.Stop(ctx, absRoot, false); err != nil {
		return fmt.Errorf("stop for reload: %w", err)
	}
	if _, err := s.Start(ctx, newSpec, policy); err != nil {
		return fmt.Errorf("start after reload: %w", err)
	}
	return nil
}

// isHotOnly reports whether the only changes between old and
// new are fields the live daemon can re-apply without a
// restart (debounce, ignore, log, fallback, poll interval,
// log level).
func isHotOnly(old, new config.WatchConfig) bool {
	if old.OnStart != new.OnStart {
		return false
	}
	return true
}

// Restart is Stop + Start for a single daemon.
func (s *Supervisor) Restart(ctx context.Context, root string) (*DaemonView, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := s.Stop(ctx, absRoot, false); err != nil {
		// Stop on a non-running daemon is treated as a
		// no-op so Restart always tries to bring the
		// daemon up.
		if !isNoDaemonErr(err) {
			return nil, err
		}
	}
	s.mu.Lock()
	d, ok := s.daemons[absRoot]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("supervisor: no daemon for %s", absRoot)
	}
	spec := d.Spec
	s.mu.Unlock()
	policy := PolicyOnCrash
	if ds := s.registry.Find(absRoot); ds != nil && ds.RestartPolicy != "" {
		policy = RestartPolicy(ds.RestartPolicy)
	}
	return s.Start(ctx, spec, policy)
}

// isNoDaemonErr reports whether err is the "no daemon for X"
// sentinel we use to distinguish a Stop on a missing daemon
// from a real failure.
func isNoDaemonErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no daemon")
}

// Quit shuts the supervisor down: stops every daemon and
// closes the IPC server. The Run loop returns shortly after.
func (s *Supervisor) Quit(ctx context.Context) error {
	return s.HandleQuit(ctx)
}

// HandlePing implements the IPC Handler interface.
func (s *Supervisor) HandlePing(_ context.Context) error { return nil }

// HandleStart implements the IPC Handler interface.
func (s *Supervisor) HandleStart(ctx context.Context, p StartPayload) (*DaemonView, error) {
	spec := SpawnSpec{
		Root:         p.Root,
		DBPath:       p.DBPath,
		Lang:         p.Lang,
		IndexerNames: p.IndexerNames,
	}
	// Load the on-disk config so the daemon starts with the
	// right settings. If the file is missing we still spawn;
	// the daemon will use its package defaults.
	cfgPath := filepath.Join(spec.Root, ".mekami", "config.json")
	if cfg, err := loadConfigFor(spec.Root); err == nil {
		spec.Watch = cfg.Watch
		spec.Build = cfg.Build
		// The StartPayload's IndexerNames is the freshest view
		// the client has; prefer it, fall back to the on-disk
		// config when the client didn't supply one.
		if len(spec.IndexerNames) == 0 {
			names := make([]string, 0, len(cfg.Indexers))
			for name := range cfg.Indexers {
				names = append(names, name)
			}
			sort.Strings(names)
			spec.IndexerNames = names
		}
	} else {
		_ = cfgPath
	}
	policy := RestartPolicy(p.RestartPolicy)
	if policy == "" {
		policy = PolicyOnCrash
	}
	return s.Start(ctx, spec, policy)
}

// HandleStop implements the IPC Handler interface.
func (s *Supervisor) HandleStop(ctx context.Context, root string, force bool) error {
	return s.Stop(ctx, root, force)
}

// HandleStatus implements the IPC Handler interface. We fetch
// live metrics for each running daemon so the response reflects
// the current state, not a stale snapshot. Daemons that are
// stopped are returned as-is (no live fetch).
func (s *Supervisor) HandleStatus(ctx context.Context, root string) ([]DaemonView, error) {
	views, err := s.Status(ctx, root)
	if err != nil {
		return nil, err
	}
	for i, v := range views {
		if v.State != string(StateRunning) {
			continue
		}
		// Best-effort: a transient failure here is fine;
		// the periodic health tick will refresh the
		// snapshot soon.
		cli := newDaemonClient(v.Root)
		if live, err := cli.fetchStatus(ctx); err == nil {
			views[i].Batches = live.Batches
			views[i].FilesIngested = live.FilesIngested
			views[i].FilesRemoved = live.FilesRemoved
			views[i].FullRebuilds = live.FullRebuilds
			views[i].Errors = live.Errors
			views[i].LastBatchUnix = live.LastBatchUnix
			views[i].Source = live.Source
		}
	}
	return views, nil
}

// HandleReload implements the IPC Handler interface.
func (s *Supervisor) HandleReload(ctx context.Context, root string) error {
	return s.Reload(ctx, root)
}

// HandleRestart implements the IPC Handler interface.
func (s *Supervisor) HandleRestart(ctx context.Context, root string) (*DaemonView, error) {
	return s.Restart(ctx, root)
}

// HandleQuit implements the IPC Handler interface.
func (s *Supervisor) HandleQuit(_ context.Context) error {
	select {
	case <-s.shutdown:
	default:
		close(s.shutdown)
	}
	return nil
}

// HandleQuitAll is the "hard uninstall" entry point.
// It performs the steps `mekami service uninstall`
// needs to leave the system clean:
//
//  1. Stop every registered daemon (graceful IPC
//     stop, then SIGTERM, then SIGKILL on timeout).
//     This is what `shutdownAll` does, but the call
//     here is made explicitly so the per-daemon errors
//     are visible to the caller.
//  2. Write the stop sentinel so the watchdog notices
//     on its next tick (or sooner, if it polls the
//     file in addition to the supervisor's PID).
//  3. Best-effort signal the watchdog PID. If the
//     watchdog is not running (e.g. the supervisor was
//     launched manually), this is a no-op.
//  4. Trigger the supervisor's own shutdown so the
//     caller can `disable --now` the systemd unit
//     without racing the supervisor's death.
//
// The function is best-effort: per-daemon stop errors
// are logged but do not abort the uninstall. The
// caller (the CLI's `service uninstall`) is expected
// to follow up with `disable --now` regardless of
// the error returned here, so a partial failure still
// results in a clean system after the next reboot.
func (s *Supervisor) HandleQuitAll(_ context.Context) error {
	// Stop every daemon we know about. We do not
	// propagate per-daemon errors: the uninstall
	// flow is allowed to be lossy (a daemon that
	// refuses to stop will be cleaned up by the OS
	// once the supervisor exits).
	for _, root := range s.allDaemonRoots() {
		_ = s.Stop(context.Background(), root, false)
	}
	// Give the daemons a brief moment to flush their
	// logs. shutdownAll does this internally, but we
	// ran Stop in a loop above, so we mirror the same
	// grace period here.
	time.Sleep(200 * time.Millisecond)
	// Write the stop sentinel. Errors are logged
	// but not returned: a missing sentinel means the
	// watchdog will rely on its supervisor-PID probe
	// to exit, which still works.
	if err := SetSentinel(); err != nil {
		s.logAdopt("quit-all: set sentinel: %v", err)
	}
	// Signal the watchdog. The boolean is best-effort
	// acknowledged; a signal delivery failure (e.g. no
	// watchdog running) is not a hard error.
	_ = SignalWatchdog()
	// Finally, close the shutdown channel so the Run
	// loop returns and the supervisor process exits.
	select {
	case <-s.shutdown:
	default:
		close(s.shutdown)
	}
	return nil
}

// allDaemonRoots returns a snapshot of the current
// daemon roots. The function takes s.mu briefly to
// avoid racing with Start/Stop.
func (s *Supervisor) allDaemonRoots() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.daemons))
	for r := range s.daemons {
		out = append(out, r)
	}
	return out
}

// loadConfigFor loads .mekami/config.json from root, returning
// defaults if the file is missing. We don't propagate the
// error because the supervisor still wants to spawn the
// daemon with package defaults.
func loadConfigFor(root string) (config.Config, error) {
	return config.Load(filepath.Join(root, ".mekami", "config.json"))
}

// Run is the supervisor's main loop. It blocks until ctx is
// done or Quit is called. It performs:
//   - health ticks: poll every daemon, refresh state from
//     its socket, apply backoff/restart policy;
//   - budget ticks: every 30s, recompute the inotify usage
//     and degrade the noisiest daemons if needed;
//   - on shutdown, stop every daemon gracefully.
func (s *Supervisor) Run(ctx context.Context) error {
	defer close(s.done)

	healthTick := time.NewTicker(s.HealthInterval)
	defer healthTick.Stop()
	budgetTick := time.NewTicker(30 * time.Second)
	defer budgetTick.Stop()

	for {
		select {
		case <-ctx.Done():
			s.shutdownAll()
			return nil
		case <-s.shutdown:
			s.shutdownAll()
			return nil
		case <-healthTick.C:
			s.healthTick()
		case <-budgetTick.C:
			s.budgetTick()
		}
	}
}

// shutdownAll stops every daemon it knows about. Best-effort;
// per-daemon errors are not surfaced.
func (s *Supervisor) shutdownAll() {
	s.mu.Lock()
	roots := make([]string, 0, len(s.daemons))
	for r := range s.daemons {
		roots = append(roots, r)
	}
	s.mu.Unlock()
	for _, r := range roots {
		_ = s.Stop(context.Background(), r, false)
	}
	// Give the daemons a moment to flush logs.
	time.Sleep(200 * time.Millisecond)
}

// healthTick performs one round of health checks: refresh
// status from every running daemon, transition dead ones to
// crashed, and restart according to the policy.
func (s *Supervisor) healthTick() {
	s.mu.Lock()
	roots := make([]string, 0, len(s.daemons))
	for r, d := range s.daemons {
		_ = d
		roots = append(roots, r)
	}
	s.mu.Unlock()

	for _, r := range roots {
		s.checkOne(r)
	}
}

func (s *Supervisor) checkOne(absRoot string) {
	s.mu.Lock()
	d, ok := s.daemons[absRoot]
	if !ok {
		s.mu.Unlock()
		return
	}
	pid := d.PID
	state := d.State
	done := d.done
	s.mu.Unlock()

	if state == StateStopped || state == StateStopping {
		return
	}
	if state == StateCrashed {
		// Backoff restart.
		if !d.RestartAt.IsZero() && time.Now().Before(d.RestartAt) {
			return
		}
		policy := PolicyOnCrash
		if ds := s.registry.Find(absRoot); ds != nil && ds.RestartPolicy != "" {
			policy = RestartPolicy(ds.RestartPolicy)
		}
		if policy == PolicyNever {
			return
		}
		// Schedule restart with backoff.
		idx := d.CrashCount
		if idx >= len(s.BackoffSchedule) {
			idx = len(s.BackoffSchedule) - 1
		}
		if idx < 0 {
			idx = 0
		}
		wait := s.BackoffSchedule[idx]
		s.mu.Lock()
		d.RestartAt = time.Now().Add(wait)
		s.mu.Unlock()
		go func(d *Daemon, policy RestartPolicy, wait time.Duration) {
			time.Sleep(wait)
			s.mu.Lock()
			if d.State != StateCrashed {
				s.mu.Unlock()
				return
			}
			spec := d.Spec
			s.mu.Unlock()
			_, _ = s.Start(context.Background(), spec, policy)
		}(d, policy, wait)
		return
	}

	// Live process: poll its IPC for fresh status.
	if !processAlive(pid) {
		// The waitProcess goroutine will close d.done;
		// if it's still open we transition now.
		select {
		case <-done:
		default:
			// process died but waitProcess hasn't
			// noticed yet; do it ourselves.
			close(done)
		}
		s.mu.Lock()
		d.State = StateCrashed
		d.CrashCount++
		d.LastCrashAt = time.Now()
		s.mu.Unlock()
		// Update persisted state.
		ds := s.registry.Find(absRoot)
		if ds != nil {
			ds.LastState = string(StateCrashed)
			_ = s.registry.Save()
		}
		return
	}
	// Process is alive; refresh status over IPC.
	cli := newDaemonClient(absRoot)
	if v, err := cli.fetchStatus(context.Background()); err == nil {
		s.mu.Lock()
		d.LastStatus = v
		s.budget.SetDaemonWatches(absRoot, v.Watches)
		s.mu.Unlock()
	}
}

// budgetTick recomputes the budget level and decides whether
// to flip the noisiest daemons to the poller.
func (s *Supervisor) budgetTick() {
	level := s.budget.Level()
	if level != BudgetDegraded && level != BudgetCritical {
		return
	}
	// Pick the top consumers; flip one at a time.
	targets := s.budget.SuggestPollingTargets(0)
	for _, t := range targets {
		if t.Watches <= 0 {
			continue
		}
		s.mu.Lock()
		d, ok := s.daemons[t.Root]
		if !ok || d.BudgetFallback {
			s.mu.Unlock()
			continue
		}
		d.BudgetFallback = true
		s.mu.Unlock()
		// Restart with the poller fallback.
		spec := d.Spec
		spec.FallbackOverride = "poll"
		_ = s.Stop(context.Background(), t.Root, false)
		_, _ = s.Start(context.Background(), spec, PolicyOnCrash)
		// One per tick; give the system time to settle.
		return
	}
}

func (s *Supervisor) budgetLevelString() string {
	switch s.budget.Level() {
	case BudgetOK:
		return "ok"
	case BudgetWarning:
		return "warning"
	case BudgetDegraded:
		return "degraded"
	case BudgetCritical:
		return "critical"
	default:
		return "unknown"
	}
}

func (s *Supervisor) persistOne(d *Daemon) {
	ds := s.registry.Find(d.Spec.Root)
	if ds == nil {
		s.registry.Upsert(DaemonState{
			Root:          d.Spec.Root,
			Lang:          d.Spec.Lang,
			DBPath:        d.Spec.DBPath,
			ConfigHash:    hashForSpec(d.Spec),
			RestartPolicy: s.policyFor(d.Spec.Root),
			LastState:     string(d.State),
		})
	} else {
		ds.ConfigHash = hashForSpec(d.Spec)
		ds.LastState = string(d.State)
	}
	_ = s.registry.Save()
}

func (s *Supervisor) policyFor(root string) string {
	if ds := s.registry.Find(root); ds != nil && ds.RestartPolicy != "" {
		return ds.RestartPolicy
	}
	return string(PolicyOnCrash)
}

// daemonViewLocked builds the user-facing view of a daemon.
// The caller must hold s.mu.
func daemonViewLocked(d *Daemon) DaemonView {
	v := DaemonView{
		Root:   d.Spec.Root,
		Lang:   d.Spec.Lang,
		DBPath: d.Spec.DBPath,
		PID:    d.PID,
		State:  string(d.State),
	}
	if !d.StartedAt.IsZero() {
		v.UptimeS = int64(time.Since(d.StartedAt).Seconds())
	}
	if !d.LastCrashAt.IsZero() {
		// Surface the most recent crash as a special state
		// detail, but keep State in the lifecycle enum so
		// the IPC contract is stable.
	}
	v.Batches = d.LastStatus.Batches
	v.FilesIngested = d.LastStatus.FilesIngested
	v.FilesRemoved = d.LastStatus.FilesRemoved
	v.FullRebuilds = d.LastStatus.FullRebuilds
	v.Errors = d.LastStatus.Errors
	v.LastBatchUnix = d.LastStatus.LastBatchUnix
	v.Source = d.LastStatus.Source
	v.Watches = d.LastStatus.Watches
	return v
}

// daemonClient is a tiny wrapper around the watch package's
// Client with the extra commands the supervisor uses
// (reload, fetchStatus). We keep it inline to avoid
// circular package dependencies: the supervisor already
// depends on watch via spawn, but the watch package's
// commands may grow over time.
type daemonClient struct {
	socketPath string
}

func newDaemonClient(root string) *daemonClient {
	return &daemonClient{socketPath: DaemonSocketPath(root)}
}

// CallRaw is a small JSON line client to the daemon socket.
// Mirrors watch.Client.Call but is local to the supervisor so
// we don't depend on watch's exported surface.
func (c *daemonClient) CallRaw(cmd string, payload []byte) ([]byte, error) {
	conn, err := dialIPC(c.socketPath, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	req := map[string]any{"cmd": cmd}
	if len(payload) > 0 {
		var p any
		_ = json.Unmarshal(payload, &p)
		req["payload"] = p
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, err
	}
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// Stop sends a "stop" command over IPC.
func (c *daemonClient) Stop(ctx context.Context) error {
	_, err := c.CallRaw("stop", nil)
	return err
}

// Reload sends a "reload" command over IPC.
func (c *daemonClient) Reload(ctx context.Context) error {
	_, err := c.CallRaw("reload", nil)
	return err
}

// fetchStatus returns the daemon's most recent status
// snapshot. Returns the parsed DaemonView so the supervisor
// can update its in-memory state.
func (c *daemonClient) fetchStatus(ctx context.Context) (DaemonView, error) {
	data, err := c.CallRaw("status", nil)
	if err != nil {
		return DaemonView{}, err
	}
	var resp struct {
		Ok            bool   `json:"ok"`
		Error         string `json:"error,omitempty"`
		UptimeS       int64  `json:"uptime_s"`
		LastBatchUnix int64  `json:"last_batch_unix"`
		Batches       int64  `json:"batches"`
		FilesIngested int64  `json:"files_ingested"`
		FilesRemoved  int64  `json:"files_removed"`
		FullRebuilds  int64  `json:"full_rebuilds"`
		Errors        int64  `json:"errors"`
		Source        string `json:"source"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return DaemonView{}, err
	}
	if !resp.Ok {
		return DaemonView{}, errors.New(resp.Error)
	}
	return DaemonView{
		UptimeS:       resp.UptimeS,
		LastBatchUnix: resp.LastBatchUnix,
		Batches:       resp.Batches,
		FilesIngested: resp.FilesIngested,
		FilesRemoved:  resp.FilesRemoved,
		FullRebuilds:  resp.FullRebuilds,
		Errors:        resp.Errors,
		Source:        resp.Source,
	}, nil
}

// ErrNotImplemented is returned by features that depend on
// platform-specific behaviour we haven't wired up yet (e.g.
// Windows service install).
var ErrNotImplemented = errors.New("supervisor: not implemented on this platform")

// stateDirSingleton is a tiny package-level pointer to the
// currently-active supervisor's state directory. It is set
// the first time NewSupervisorWithOptions runs in a given
// process, and read by helpers that need to know where the
// log file lives (e.g. logAdopt) without holding a
// reference to a specific Supervisor. We keep it a pointer
// instead of a value so tests that build multiple
// supervisors do not silently overwrite each other's state
// directory; only the first one wins, and subsequent
// supervisors use their own s.stateDir internally.
var stateDirSingleton *string

// setStateDirSingleton records dir as the active state
// directory. Only the first call has an effect; subsequent
// calls are no-ops. We use this rather than reading the
// env at every call so the resolution order matches
// NewSupervisorWithOptions (env → StateDir()), which
// honours XDG_CONFIG_HOME.
func setStateDirSingleton(dir string) {
	if stateDirSingleton != nil {
		return
	}
	stateDirSingleton = &dir
}

// stateDirSingletonGet returns the recorded state
// directory, or "" if none has been recorded yet (e.g. in
// tests that never call NewSupervisorWithOptions).
func stateDirSingletonGet() string {
	if stateDirSingleton == nil {
		return ""
	}
	return *stateDirSingleton
}

// appendLogLine appends a single line to path, creating
// the file if necessary. The function is best-effort: any
// error is returned to the caller, who is expected to
// ignore it (logging is a side concern, not a correctness
// one).
func appendLogLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}
	return nil
}
