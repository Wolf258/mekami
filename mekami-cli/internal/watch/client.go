package watch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Client is a thin IPC client that talks to a running daemon over
// the project's Unix domain socket. It is used by:
//   - `mekami watch status` and `stop` to query/control the daemon;
//   - `mekami serve` to enrich `index_status` with watcher data.
//
// The client is stateless: each call opens a fresh connection.
// This is simpler than a pooled client and works fine for the
// handful of messages per second a watcher sees.
type Client struct {
	SocketPath string
	// Timeout is the per-call dial + read deadline. Zero means
	// 2s, which is long enough for any reasonable daemon
	// response and short enough to feel instant in `status`.
	Timeout time.Duration
}

// NewClient returns a Client bound to the canonical socket for
// the given root.
func NewClient(root string) *Client {
	return &Client{
		SocketPath: SocketPath(root),
		Timeout:    2 * time.Second,
	}
}

// ErrNotRunning is returned by the client when the socket is
// missing or the daemon does not respond.
var ErrNotRunning = errors.New("watcher: not running")

// Call sends a single Request and returns the Response. It
// returns ErrNotRunning if the socket is missing, the dial fails,
// or the response times out. Network/JSON errors are wrapped.
func (c *Client) Call(ctx context.Context, req Request) (Response, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if _, err := os.Stat(c.SocketPath); err != nil {
		if os.IsNotExist(err) {
			return Response{}, ErrNotRunning
		}
		return Response{}, fmt.Errorf("stat socket: %w", err)
	}
	conn, err := dialIPC(c.SocketPath, timeout)
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
		return Response{}, ErrNotRunning
	}
	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("decode: %w", err)
	}
	return resp, nil
}

// Ping is shorthand for a "ping" request. Returns true if the
// daemon responded.
func (c *Client) Ping(ctx context.Context) bool {
	resp, err := c.Call(ctx, Request{Cmd: CmdPing})
	return err == nil && resp.Ok
}

// Status returns the daemon's status response, or ErrNotRunning.
func (c *Client) Status(ctx context.Context) (Response, error) {
	return c.Call(ctx, Request{Cmd: CmdStatus})
}

// Stop asks the daemon to shut down. The daemon is expected to
// exit before responding, so the call may briefly return
// ErrNotRunning as the socket is removed. Both outcomes are
// considered "stop requested".
func (c *Client) Stop(ctx context.Context) error {
	resp, err := c.Call(ctx, Request{Cmd: CmdStop})
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			return nil
		}
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("daemon: %s", resp.Error)
	}
	return nil
}

// _ keeps filepath in the import set so goimports doesn't churn
// when files in this package are added or removed.
var _ = filepath.Base
