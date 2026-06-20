package watch

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
)

// Package-internal: keep these aligned with internal/supervisor/ipc.go.
const (
	CmdPing    = "ping"
	CmdStatus  = "status"
	CmdStop    = "stop"
	CmdReload  = "reload"
	CmdMetrics = "metrics"
)

// ipcServer is the in-process server the daemon uses to expose
// status/ping/stop over a Unix domain socket. It is intentionally
// minimal: a single listener, one goroutine per connection, and
// line-delimited JSON.
type ipcServer struct {
	socketPath string
	listener   net.Listener

	// stats is a read-only snapshot of the watcher's counters.
	// The server reads them on every request and serialises the
	// current values; updates from the watcher are atomic so
	// the server does not need to lock.
	stats *Stats

	// startedAt is the daemon's start time, used for uptime.
	startedAt time.Time

	// root is reported back in status responses so a client
	// connected to the wrong socket (e.g. two projects) gets a
	// useful error rather than a silent "ok".
	root string

	// onStop is called when a "stop" request arrives. The server
	// signals the daemon to shut down; the daemon then closes
	// the listener, which causes the server's Accept loop to
	// return. We do not call os.Exit because the daemon needs
	// to flush its log and clean up state.
	onStop func()

	// getConfig returns the daemon's current config. Used by
	// the "metrics" command.
	getConfig func() (config.WatchConfig, error)
	// setConfig updates the daemon's config in place. Used by
	// the "reload" command.
	setConfig func(config.WatchConfig) error
}

// startIPCServer binds the socket, starts accepting connections,
// and returns the server. The caller must call Shutdown to clean
// up the socket file.
//
// getConfig and setConfig are optional: if nil, "reload" and
// "metrics" return an error rather than crash. They exist as
// callbacks (not fields) so the server stays decoupled from
// the daemon's config struct layout.
func startIPCServer(socketPath string, root string, stats *Stats, onStop func(),
	getConfig func() (config.WatchConfig, error), setConfig func(config.WatchConfig) error,
) (*ipcServer, error) {
	// Make sure the parent dir exists.
	if err := os.MkdirAll(parentDir(socketPath), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir for socket: %w", err)
	}
	// Remove any stale socket from a previous crash.
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	// Socket file perms: 0600 so only the owning user can connect.
	_ = os.Chmod(socketPath, 0o600)

	s := &ipcServer{
		socketPath: socketPath,
		listener:   ln,
		stats:      stats,
		startedAt:  time.Now(),
		root:       root,
		onStop:     onStop,
		getConfig:  getConfig,
		setConfig:  setConfig,
	}
	go s.acceptLoop()
	return s, nil
}

// Shutdown closes the listener and removes the socket file.
// It does not interrupt in-flight connections; clients see
// EOF on their next read.
func (s *ipcServer) Shutdown() error {
	if s == nil {
		return nil
	}
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	_ = os.Remove(s.socketPath)
	return err
}

func (s *ipcServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *ipcServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		req, err := DecodeRequest(line)
		if err != nil {
			s.writeResponse(conn, Response{Ok: false, Error: err.Error()})
			continue
		}
		s.writeResponse(conn, s.dispatch(req))
		// `stop` is one-shot: close the connection so the
		// client gets EOF, then signal shutdown.
		if req.Cmd == CmdStop {
			if s.onStop != nil {
				go s.onStop()
			}
			return
		}
	}
}

func (s *ipcServer) writeResponse(conn net.Conn, resp Response) {
	data, err := resp.Encode()
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

func (s *ipcServer) dispatch(req Request) Response {
	switch req.Cmd {
	case CmdPing:
		return Response{Ok: true}
	case CmdStatus:
		return s.status()
	case CmdMetrics:
		return s.metrics()
	case CmdReload:
		return s.reload()
	case CmdStop:
		// Stop is handled inline in handle() so the connection
		// can be closed and the callback invoked synchronously.
		// We still return a successful response so the client
		// gets a positive ack.
		return Response{Ok: true}
	default:
		return Response{Ok: false, Error: "unknown command: " + req.Cmd}
	}
}

func (s *ipcServer) status() Response {
	resp := Response{Ok: true, Root: s.root}
	resp.UptimeS = int64(time.Since(s.startedAt).Seconds())
	if s.stats != nil {
		resp.Batches = s.stats.Batches.Load()
		resp.FilesIngested = s.stats.FilesIngested.Load()
		resp.FilesRemoved = s.stats.FilesRemoved.Load()
		resp.FullRebuilds = s.stats.FullRebuilds.Load()
		resp.Errors = s.stats.Errors.Load()
		if last := s.stats.LastBatchAt.Load(); last > 0 {
			resp.LastBatchUnix = last / 1e9
		}
		resp.Source = s.stats.LastSourceName
	}
	return resp
}

// metrics returns the daemon's current view. The supervisor
// uses it to update its DaemonView. The "metrics" command
// exists as a separate command from "status" so future fields
// (e.g. memory usage, inotify watch count) can be added
// without breaking older clients that parse "status".
func (s *ipcServer) metrics() Response {
	resp := s.status()
	if s.getConfig != nil {
		if _, err := s.getConfig(); err != nil {
			return Response{Ok: false, Error: "metrics: " + err.Error()}
		}
	}
	return resp
}

// reload applies a config update. The current implementation
// re-records the in-memory config; the daemon's loop will pick
// up new values on the next batch. Cold changes (on_start,
// lang) require a full process restart, which the supervisor
// handles by stop+start of the daemon process.
func (s *ipcServer) reload() Response {
	if s.getConfig == nil || s.setConfig == nil {
		return Response{Ok: false, Error: "reload not supported"}
	}
	cfg, err := s.getConfig()
	if err != nil {
		return Response{Ok: false, Error: "reload: " + err.Error()}
	}
	if err := s.setConfig(cfg); err != nil {
		return Response{Ok: false, Error: "reload: " + err.Error()}
	}
	return Response{Ok: true}
}

// parentDir is filepath.Dir without importing filepath at the
// top of the file (kept minimal so the IPC code reads top-down).
func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

// _ keeps io and sync in the import set; both are used by other
// files in the package and we want goimports to leave this file
// alone.
var _ io.Reader = (*os.File)(nil)
var _ sync.Locker = (*sync.Mutex)(nil)
