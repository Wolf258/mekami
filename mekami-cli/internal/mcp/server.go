// Package mcp is the mekami MCP server. The tool registry is built
// from internal/naming.Specs so the CLI and the MCP server share
// one definition: change a name, change it on both sides.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/handlers"
	"github.com/Wolf258/mekami-cli/internal/naming"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	server        *mcp.Server
	store         *store.Store
	watcherClient WatcherClient
}

// WatcherClient is the small interface the MCP server uses to
// query the watcher daemon for its status. The interface lives
// here (not in internal/watch) so the MCP package does not
// import the watcher. The CLI injects a watch.Client; tests
// can inject a stub.
type WatcherClient interface {
	Status(ctx context.Context) (WatcherStatusPayload, error)
	PID() int
}

// WatcherStatusPayload is the shape the client returns. We
// keep it as a flat struct (mirroring watch.Response) so the
// conversion to queries.WatcherStatus is mechanical.
type WatcherStatusPayload struct {
	UptimeS       int64
	LastBatchUnix int64
	Batches       int64
	FilesIngested int64
	FilesRemoved  int64
	FullRebuilds  int64
	Errors        int64
	Source        string
	Root          string
	OK            bool
}

// NewServerWithWatcher opens the graph DB, registers every MCP
// tool declared in naming.Specs, and stores an optional watcher
// client used by index_status to enrich the response with daemon
// uptime and counters. A nil watcher is fine: the index_status
// response stays valid (the watcher field is just absent).
func NewServerWithWatcher(dbPath string, wc WatcherClient) (*Server, error) {
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "mekami",
		Version: "0.1.0",
	}, nil)
	s := &Server{server: server, store: st, watcherClient: wc}
	s.registerTools()
	return s, nil
}

func (s *Server) Close() error { return s.store.Close() }

func (s *Server) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}

// registerTools walks naming.Specs and adds an MCP tool for every
// Spec that declares a Name. The handler is dispatched in toolCall
// via the tool's name.
func (s *Server) registerTools() {
	for _, spec := range naming.Specs {
		if spec.Name == "" {
			continue
		}
		// Build the input schema from Args + Flags (snake_case).
		props := map[string]any{}
		var required []string
		for _, a := range spec.Args {
			props[a.Name] = map[string]any{
				"type":        "string",
				"description": a.Description,
			}
			required = append(required, a.Name)
		}
		for _, f := range spec.Flags {
			if f.CLIOnly {
				continue
			}
			props[f.Name] = mcpFlagSchema(f)
		}
		schema := map[string]any{
			"type":       "object",
			"properties": props,
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		s.server.AddTool(&mcp.Tool{
			Name:        spec.Name,
			Description: toolDescription(spec),
			InputSchema: schema,
		}, s.makeHandler(spec.Name))
	}
}

// toolDescription assembles the description the LLM sees. We keep
// the wording tight: it lands in the system prompt on every call.
func toolDescription(s naming.Spec) string {
	if s.Long != "" {
		return s.Long
	}
	return s.Short
}

// mcpFlagSchema returns the JSON-schema fragment for f.
func mcpFlagSchema(f naming.Flag) map[string]any {
	out := map[string]any{"description": f.Description}
	switch f.Type {
	case "int":
		out["type"] = "integer"
	case "bool":
		out["type"] = "boolean"
	case "stringSlice":
		out["type"] = "array"
		out["items"] = map[string]any{"type": "string"}
	default:
		out["type"] = "string"
	}
	return out
}

// makeHandler returns the SDK-compatible handler for a given tool
// name. The handler decodes the raw JSON arguments into an ArgMap
// and dispatches to the matching function in internal/handlers.
//
// After the Result refactor (see internal/handlers/result.go) every
// handler returns a Result{Text, Data}. The MCP wire default is
// the same text view the CLI prints by default — keeping CLI and
// MCP byte-for-byte equivalent. When the caller asked for JSON
// (via the json arg the runner injects), the data side is
// serialized instead.
func (s *Server) makeHandler(name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args naming.ArgMap
		if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
			}
		}
		out, err := dispatch(ctx, s, name, args)
		if err != nil {
			// SourceError is a benign absence (e.g. no last_root);
			// surface it as a text result so the LLM can recover.
			if msg := handlers.SourceError(err); msg != "" {
				return handlers.ToolResult(msg), nil
			}
			// "no symbol found" / "file not found" type errors are
			// reported as text results, not transport errors, so
			// the agent sees a useful hint.
			if isUserError(err) {
				return handlers.ToolResult(err.Error()), nil
			}
			return nil, err
		}
		// If the LLM asked for JSON explicitly, honor it. Otherwise
		// prefer the text view so MCP and CLI default match.
		if args.GetBool("json", false) {
			return handlers.ToolResult(handlers.ExtractData(out)), nil
		}
		if text := handlers.TextView(out); text != "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: text}},
			}, nil
		}
		return handlers.ToolResult(handlers.ExtractData(out)), nil
	}
}

// errorResult returns a CallToolResult that flags the call as an
// error with the given message as text content. Used for argument
// decode failures that should not abort the transport.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

// dispatch routes a tool call to the matching handler. index_status
// has special handling: the watcher payload is appended when the
// daemon is reachable. All other graph-read tools share a single
// dispatch table in handlers.DispatchRead.
func dispatch(ctx context.Context, s *Server, name string, args naming.ArgMap) (any, error) {
	if name == "index_status" {
		return indexStatusWithWatcher(ctx, s)
	}
	return handlers.DispatchRead(ctx, s.store, name, args)
}

// indexStatusWithWatcher enriches the basic status with watcher
// data when a watcher client is registered and the daemon is
// reachable.
func indexStatusWithWatcher(ctx context.Context, s *Server) (any, error) {
	out, err := handlers.IndexStatus(ctx, s.store, nil)
	if err != nil {
		return out, err
	}
	if s.watcherClient == nil {
		return out, nil
	}
	payload, werr := s.watcherClient.Status(ctx)
	if werr != nil || !payload.OK {
		return out, nil
	}
	type watcherStatus struct {
		Running       bool   `json:"running"`
		PID           int    `json:"pid,omitempty"`
		UptimeS       int64  `json:"uptime_s,omitempty"`
		LastBatchUnix int64  `json:"last_batch_unix,omitempty"`
		Batches       int64  `json:"batches,omitempty"`
		Source        string `json:"source,omitempty"`
		Errors        int64  `json:"errors,omitempty"`
	}
	enriched := map[string]any{}
	switch v := out.(type) {
	case map[string]any:
		enriched = v
	default:
		// IndexStatus returned a string (no last_root); leave it.
		return out, nil
	}
	enriched["watcher"] = watcherStatus{
		Running:       true,
		PID:           s.watcherClient.PID(),
		UptimeS:       payload.UptimeS,
		LastBatchUnix: payload.LastBatchUnix,
		Batches:       payload.Batches,
		Source:        payload.Source,
		Errors:        payload.Errors,
	}
	return enriched, nil
}

// isUserError reports whether err is the kind of "the input was
// wrong" error that should be surfaced as a text result (so the
// LLM sees a hint) rather than a transport-level error.
func isUserError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrNoLastRoot) {
		return true
	}
	msg := err.Error()
	return msg == "file not found in graph"
}
