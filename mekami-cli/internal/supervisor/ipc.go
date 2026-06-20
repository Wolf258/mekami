package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// IPC request types. Same line-delimited JSON protocol the
// watcher daemon uses; we keep the wire format compatible so a
// single client library can talk to either.
const (
	CmdPing    = "ping"
	CmdStart   = "start"
	CmdStop    = "stop"
	CmdStatus  = "status"
	CmdList    = "list"
	CmdReload  = "reload"
	CmdRestart = "restart"
	CmdQuit    = "quit"
	// CmdQuitAll is the hard-stop signal used by
	// `mekami service uninstall`. Unlike CmdQuit,
	// which only closes the IPC server, CmdQuitAll
	// stops every registered daemon, then closes the
	// server, and signals the watchdog to exit
	// (writing the sentinel file). The supervisor
	// exits after handling CmdQuitAll, so the
	// service manager (systemd / launchd) does not
	// need to send a second signal.
	CmdQuitAll = "quit-all"
)

// Request is the wire format for client -> supervisor.
type Request struct {
	Cmd     string          `json:"cmd"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// StartPayload is the body of a "start" request.
type StartPayload struct {
	Root          string `json:"root"`
	Lang          string `json:"lang"`
	DBPath        string `json:"db_path"`
	RestartPolicy string `json:"restart_policy"`
	// IndexerNames is the set of language identifiers the
	// project tracks (from .mekami/config.json's indexers).
	// The supervisor passes them to the daemon so the cross-
	// language cleanup runs on every full build the watcher
	// triggers. Empty/nil means "no cross-language cleanup".
	IndexerNames []string `json:"indexer_names,omitempty"`
}

// Response is the wire format for supervisor -> client.
type Response struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Status payload: one entry per daemon when "status" is
	// called without args; a single entry when called with a
	// specific root.
	Daemons []DaemonView `json:"daemons,omitempty"`
	// Roots is the response to "list".
	Roots []string `json:"roots,omitempty"`
	// Started is set on a successful "start" so the caller
	// knows the daemon is up.
	Started *DaemonView `json:"started,omitempty"`
}

// DaemonView is the user-facing snapshot of a daemon, used by
// "status" and "list" responses.
type DaemonView struct {
	Root          string `json:"root"`
	Lang          string `json:"lang"`
	DBPath        string `json:"db_path"`
	PID           int    `json:"pid"`
	State         string `json:"state"`
	UptimeS       int64  `json:"uptime_s"`
	LastBatchUnix int64  `json:"last_batch_unix,omitempty"`
	Batches       int64  `json:"batches,omitempty"`
	FilesIngested int64  `json:"files_ingested,omitempty"`
	FilesRemoved  int64  `json:"files_removed,omitempty"`
	FullRebuilds  int64  `json:"full_rebuilds,omitempty"`
	Errors        int64  `json:"errors,omitempty"`
	Source        string `json:"source,omitempty"`
	Watches       int64  `json:"watches,omitempty"`
	ConfigHash    string `json:"config_hash,omitempty"`
	RestartPolicy string `json:"restart_policy,omitempty"`
	BudgetLevel   string `json:"budget_level,omitempty"`
}

// Handler is the interface the IPC layer uses to dispatch
// commands. The Supervisor implements it.
type Handler interface {
	HandlePing(ctx context.Context) error
	HandleStart(ctx context.Context, p StartPayload) (*DaemonView, error)
	HandleStop(ctx context.Context, root string, force bool) error
	HandleStatus(ctx context.Context, root string) ([]DaemonView, error)
	HandleList(ctx context.Context) []string
	HandleReload(ctx context.Context, root string) error
	HandleRestart(ctx context.Context, root string) (*DaemonView, error)
	HandleQuit(ctx context.Context) error
	// HandleQuitAll is the "hard uninstall" entry
	// point. The implementation is expected to:
	//  1. stop every registered daemon;
	//  2. write the stop sentinel so the watchdog
	//     notices on its next tick (or sooner, if it
	//     also watches the file);
	//  3. signal the watchdog PID (best-effort) so
	//     it exits immediately;
	//  4. close the IPC server so no further
	//     commands are accepted.
	// The function returns nil on success; any
	// per-daemon error is logged but not surfaced
	// (uninstall is best-effort).
	HandleQuitAll(ctx context.Context) error
}

// ipcServer is the in-process server. One listener, one goroutine
// per connection, line-delimited JSON.
type ipcServer struct {
	socketPath string
	listener   net.Listener
	handler    Handler
	mu         sync.Mutex
	closed     bool
}

// StartIPCServer binds the socket, starts accepting, returns
// the server. The caller must call Shutdown.
func StartIPCServer(handler Handler) (*ipcServer, error) {
	return startIPCServerAt(SocketPath(), handler)
}

func startIPCServerAt(socketPath string, handler Handler) (*ipcServer, error) {
	// Derive the parent dir from the socket path itself so the
	// caller is the single source of truth. Previously this
	// used the global StateDir(), which forced callers to set
	// XDG_CONFIG_HOME to make custom paths work; the socket
	// path is now the only thing that matters.
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	// Remove any stale socket from a previous run.
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	_ = os.Chmod(socketPath, 0o600)
	s := &ipcServer{
		socketPath: socketPath,
		listener:   ln,
		handler:    handler,
	}
	go s.acceptLoop()
	return s, nil
}

func (s *ipcServer) Shutdown() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.listener
	s.mu.Unlock()
	var err error
	if ln != nil {
		err = ln.Close()
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
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		// One request per connection for state-changing
		// commands; "status" and "list" can be called many
		// times. We use a per-line model so the client can
		// pipeline calls.
		req, err := DecodeRequest(line)
		if err != nil {
			s.writeResp(conn, Response{Ok: false, Error: err.Error()})
			continue
		}
		resp := s.dispatch(conn, req)
		s.writeResp(conn, resp)

		if req.Cmd == CmdQuit {
			// Server-side shutdown. The accept loop
			// returns on the next Accept; the caller
			// is expected to also call Shutdown.
			go func() { _ = s.Shutdown() }()
			return
		}
		if req.Cmd == CmdQuitAll {
			// Same as CmdQuit, but the handler
			// is expected to also stop every
			// daemon and write the stop sentinel
			// before returning. The IPC server
			// closes the same way.
			go func() { _ = s.Shutdown() }()
			return
		}
	}
}

func (s *ipcServer) dispatch(_ net.Conn, req Request) Response {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	switch req.Cmd {
	case CmdPing:
		if err := s.handler.HandlePing(ctx); err != nil {
			return Response{Ok: false, Error: err.Error()}
		}
		return Response{Ok: true}
	case CmdStart:
		var p StartPayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return Response{Ok: false, Error: "decode payload: " + err.Error()}
		}
		v, err := s.handler.HandleStart(ctx, p)
		if err != nil {
			return Response{Ok: false, Error: err.Error()}
		}
		return Response{Ok: true, Started: v}
	case CmdStop:
		root, force := decodeStop(req.Payload)
		if err := s.handler.HandleStop(ctx, root, force); err != nil {
			return Response{Ok: false, Error: err.Error()}
		}
		return Response{Ok: true}
	case CmdStatus:
		root := decodeRoot(req.Payload)
		views, err := s.handler.HandleStatus(ctx, root)
		if err != nil {
			return Response{Ok: false, Error: err.Error()}
		}
		return Response{Ok: true, Daemons: views}
	case CmdList:
		roots := s.handler.HandleList(ctx)
		return Response{Ok: true, Roots: roots}
	case CmdReload:
		root := decodeRoot(req.Payload)
		if err := s.handler.HandleReload(ctx, root); err != nil {
			return Response{Ok: false, Error: err.Error()}
		}
		return Response{Ok: true}
	case CmdRestart:
		root := decodeRoot(req.Payload)
		v, err := s.handler.HandleRestart(ctx, root)
		if err != nil {
			return Response{Ok: false, Error: err.Error()}
		}
		return Response{Ok: true, Started: v}
	case CmdQuit:
		if err := s.handler.HandleQuit(ctx); err != nil {
			return Response{Ok: false, Error: err.Error()}
		}
		return Response{Ok: true}
	case CmdQuitAll:
		if err := s.handler.HandleQuitAll(ctx); err != nil {
			return Response{Ok: false, Error: err.Error()}
		}
		return Response{Ok: true}
	}
	return Response{Ok: false, Error: "unknown command: " + req.Cmd}
}

func decodeRoot(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var p struct {
		Root string `json:"root"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Root
}

func decodeStop(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var p struct {
		Root  string `json:"root"`
		Force bool   `json:"force"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Root, p.Force
}

func (s *ipcServer) writeResp(conn net.Conn, r Response) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

// DecodeRequest parses a single line into a Request.
func DecodeRequest(line []byte) (Request, error) {
	var r Request
	if err := json.Unmarshal(line, &r); err != nil {
		return r, fmt.Errorf("decode request: %w", err)
	}
	if strings.TrimSpace(r.Cmd) == "" {
		return r, errors.New("decode request: empty cmd")
	}
	return r, nil
}

// Client is a thin IPC client for the supervisor. Same wire
// format as watch.Client but with a different socket path and a
// richer payload (start payload, restart, etc.).
type Client struct {
	SocketPath string
	Timeout    time.Duration
}

// NewClient returns a Client bound to the supervisor socket.
func NewClient() *Client {
	return &Client{
		SocketPath: SocketPath(),
		Timeout:    5 * time.Second,
	}
}

// Call sends a single request and returns the response. Returns
// ErrSupervisorNotRunning if the socket is missing or the dial
// fails.
func (c *Client) Call(ctx context.Context, req Request) (Response, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if _, err := os.Stat(c.SocketPath); err != nil {
		if os.IsNotExist(err) {
			return Response{}, ErrSupervisorNotRunning
		}
		return Response{}, fmt.Errorf("stat socket: %w", err)
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", c.SocketPath)
	if err != nil {
		return Response{}, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encode: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return Response{}, fmt.Errorf("write: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Response{}, fmt.Errorf("read: %w", err)
		}
		return Response{}, ErrSupervisorNotRunning
	}
	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("decode: %w", err)
	}
	return resp, nil
}

// ErrSupervisorNotRunning is returned by Client when the
// supervisor socket is missing or unreachable.
var ErrSupervisorNotRunning = errors.New("supervisor: not running")

// Ping is shorthand for a "ping" request. Returns true if the
// supervisor responded.
func (c *Client) Ping(ctx context.Context) bool {
	resp, err := c.Call(ctx, Request{Cmd: CmdPing})
	return err == nil && resp.Ok
}

// List returns the registered roots.
func (c *Client) List(ctx context.Context) ([]string, error) {
	resp, err := c.Call(ctx, Request{Cmd: CmdList})
	if err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, fmt.Errorf("supervisor: %s", resp.Error)
	}
	return resp.Roots, nil
}

// Start asks the supervisor to spawn a daemon.
func (c *Client) Start(ctx context.Context, p StartPayload) (*DaemonView, error) {
	raw, _ := json.Marshal(p)
	resp, err := c.Call(ctx, Request{Cmd: CmdStart, Payload: raw})
	if err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, fmt.Errorf("supervisor: %s", resp.Error)
	}
	return resp.Started, nil
}

// Stop asks the supervisor to stop a daemon.
func (c *Client) Stop(ctx context.Context, root string, force bool) error {
	raw, _ := json.Marshal(struct {
		Root  string `json:"root"`
		Force bool   `json:"force"`
	}{root, force})
	resp, err := c.Call(ctx, Request{Cmd: CmdStop, Payload: raw})
	if err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("supervisor: %s", resp.Error)
	}
	return nil
}

// Status returns the daemon views. If root is empty, all daemons
// are returned.
func (c *Client) Status(ctx context.Context, root string) ([]DaemonView, error) {
	raw, _ := json.Marshal(struct {
		Root string `json:"root"`
	}{root})
	resp, err := c.Call(ctx, Request{Cmd: CmdStatus, Payload: raw})
	if err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, fmt.Errorf("supervisor: %s", resp.Error)
	}
	return resp.Daemons, nil
}

// Reload asks the supervisor to reload config for root (or all
// if root is empty).
func (c *Client) Reload(ctx context.Context, root string) error {
	raw, _ := json.Marshal(struct {
		Root string `json:"root"`
	}{root})
	resp, err := c.Call(ctx, Request{Cmd: CmdReload, Payload: raw})
	if err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("supervisor: %s", resp.Error)
	}
	return nil
}

// Restart is a stop+start for a single daemon.
func (c *Client) Restart(ctx context.Context, root string) (*DaemonView, error) {
	raw, _ := json.Marshal(struct {
		Root string `json:"root"`
	}{root})
	resp, err := c.Call(ctx, Request{Cmd: CmdRestart, Payload: raw})
	if err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, fmt.Errorf("supervisor: %s", resp.Error)
	}
	return resp.Started, nil
}

// Quit asks the supervisor to shut itself down (and all daemons).
func (c *Client) Quit(ctx context.Context) error {
	resp, err := c.Call(ctx, Request{Cmd: CmdQuit})
	if err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("supervisor: %s", resp.Error)
	}
	return nil
}

// QuitAll is the hard-uninstall path: the supervisor
// stops every daemon, writes the stop sentinel, and
// signals the watchdog to exit. The function returns
// once the supervisor has acknowledged the request;
// the supervisor process itself is expected to exit
// shortly after, and the watchdog within milliseconds.
//
// Errors are returned to the caller so the CLI can
// decide between "carry on" and "abort uninstall". A
// supervisor-not-running error means the system is
// already in a clean state, which is the success case
// for `mekami service uninstall`.
func (c *Client) QuitAll(ctx context.Context) error {
	resp, err := c.Call(ctx, Request{Cmd: CmdQuitAll})
	if err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("supervisor: %s", resp.Error)
	}
	return nil
}
