package watch

import (
	"context"

	mcp "github.com/Wolf258/mekami-cli/internal/mcp"
)

// MCPClient adapts the IPC client to the mcp.WatcherClient
// interface. It is constructed by the serve command so the MCP
// server can enrich index_status without taking a hard
// dependency on the watcher's transport.
type MCPClient struct {
	root string
}

// NewMCPClient returns an mcp.WatcherClient that talks to the
// daemon bound to root.
func NewMCPClient(root string) *MCPClient {
	return &MCPClient{root: root}
}

// Status implements mcp.WatcherClient. It dials the daemon's
// socket and returns the payload. On any error (no daemon,
// timeout) it returns the error and an empty payload.
func (c *MCPClient) Status(ctx context.Context) (mcp.WatcherStatusPayload, error) {
	cli := NewClient(c.root)
	cli.Timeout = 1500e6 // 1.5s; a slow status call is unusual
	resp, err := cli.Status(ctx)
	if err != nil {
		return mcp.WatcherStatusPayload{}, err
	}
	return mcp.WatcherStatusPayload{
		UptimeS:       resp.UptimeS,
		LastBatchUnix: resp.LastBatchUnix,
		Batches:       resp.Batches,
		FilesIngested: resp.FilesIngested,
		FilesRemoved:  resp.FilesRemoved,
		FullRebuilds:  resp.FullRebuilds,
		Errors:        resp.Errors,
		Source:        resp.Source,
		Root:          resp.Root,
		OK:            resp.Ok,
	}, nil
}

// PID returns the daemon's PID by reading the PID file. It
// returns 0 if the file is missing or unreadable.
func (c *MCPClient) PID() int {
	pid, _ := ReadPID(c.root)
	return pid
}

// Compile-time guard.
var _ mcp.WatcherClient = (*MCPClient)(nil)
