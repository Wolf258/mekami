package mekami

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// runMCPTest is the body of `mekami mcp-test`. It spawns the
// server as a subprocess, lists tools, and calls a sample so the
// user can verify the wire works against the local graph.
func runMCPTest(ctx context.Context, cmd *cobra.Command) error {
	path, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	return smokeTest(ctx, path)
}

func smokeTest(ctx context.Context, dbPath string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mekami-mcp-test", Version: "0.1.0"}, nil)
	transport := &mcp.CommandTransport{Command: exec.Command(self, "serve", "--db", dbPath)}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	session, err := client.Connect(dialCtx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer session.Close()

	fmt.Println("→ Listing tools...")
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	for _, t := range tools.Tools {
		fmt.Printf("  - %s\n", t.Name)
	}

	discoverCtx, discoverCancel := context.WithTimeout(ctx, 5*time.Second)
	defer discoverCancel()
	discovered, err := firstSymbolQName(discoverCtx, session, "a")
	if err != nil {
		return fmt.Errorf("discover seed symbol: %w", err)
	}
	if discovered == "" {
		return fmt.Errorf("no symbols found in graph; cannot run smoke test")
	}
	fmt.Printf("\n→ Discovered seed symbol: %s\n", discovered)

	filePath := discoverFilePath(ctx, session, discovered)

	calls := []struct {
		name string
		args map[string]any
	}{
		{"list_modules", map[string]any{}},
		{"show_modules", map[string]any{}},
		{"index_status", map[string]any{}},
		{"get_symbol", map[string]any{"qualified_name": discovered}},
		{"who_calls", map[string]any{"qualified_name": discovered, "ref_kind": "call"}},
		{"what_calls", map[string]any{"qualified_name": discovered}},
		{"list_files", map[string]any{"max_depth": 1}},
		{"get_symbol", map[string]any{"qualified_name": discovered, "body": true, "max_lines": 5}},
		{"show_changes", map[string]any{}},
		{"trace_calls", map[string]any{"from": "this.does.not.Exist", "to": discovered}},
	}
	if filePath != "" {
		calls = append(calls,
			struct {
				name string
				args map[string]any
			}{"list_file", map[string]any{"path": filePath}},
		)
	}

	for _, c := range calls {
		fmt.Printf("\n→ Calling %s(%v)...\n", c.name, c.args)
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      c.name,
			Arguments: c.args,
		})
		if err != nil {
			return fmt.Errorf("call %s: %w", c.name, err)
		}
		for _, item := range res.Content {
			tc, ok := item.(*mcp.TextContent)
			if !ok {
				continue
			}
			txt := truncateForDisplay(tc.Text, 400)
			fmt.Printf("    %s\n", txt)
		}
	}

	fmt.Println("\n✓ smoke test passed")
	return nil
}

// truncateForDisplay shortens s to at most maxBytes without
// splitting a multibyte UTF-8 rune.
func truncateForDisplay(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	out := s[:cut] + "\n    ...(truncated)"
	return out
}

// firstSymbolQName discovers a known-qualified-name seed symbol for
// the smoke test by walking list_modules -> list_package
// and returning the first func/method found. find_symbol was the
// previous path but has been removed; this mirrors the same intent
// (pick a real symbol from the graph) without depending on
// substring search.
func firstSymbolQName(ctx context.Context, session *mcp.ClientSession, _ string) (string, error) {
	modsRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_modules",
		Arguments: map[string]any{"json": true},
	})
	if err != nil {
		return "", err
	}
	var mods []struct {
		Path string `json:"path"`
	}
	if text := firstText(modsRes); text != "" {
		_ = json.Unmarshal([]byte(text), &mods)
	}
	if len(mods) == 0 {
		return "", nil
	}
	pkgsRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_package",
		Arguments: map[string]any{"package_id": mods[0].Path, "json": true},
	})
	if err != nil {
		return "", err
	}
	var hits []struct {
		Kind          string `json:"Kind"`
		QualifiedName string `json:"QualifiedName"`
	}
	if text := firstText(pkgsRes); text != "" {
		_ = json.Unmarshal([]byte(text), &hits)
	}
	for _, h := range hits {
		if (h.Kind == "func" || h.Kind == "method") &&
			h.QualifiedName != "" && h.QualifiedName != "__imports__" {
			return h.QualifiedName, nil
		}
	}
	for _, h := range hits {
		if h.QualifiedName != "" && h.QualifiedName != "__imports__" {
			return h.QualifiedName, nil
		}
	}
	return "", nil
}

// firstText returns the first text content from a tool result, or
// "" if the result has no text content.
func firstText(res *mcp.CallToolResult) string {
	for _, item := range res.Content {
		if tc, ok := item.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func discoverFilePath(ctx context.Context, session *mcp.ClientSession, qn string) string {
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_symbol",
		Arguments: map[string]any{"qualified_name": qn},
	})
	if err != nil {
		return ""
	}
	for _, item := range res.Content {
		tc, ok := item.(*mcp.TextContent)
		if !ok {
			continue
		}
		var hits []struct {
			FilePath string `json:"file_path"`
		}
		if err := json.Unmarshal([]byte(tc.Text), &hits); err != nil {
			continue
		}
		if len(hits) > 0 && hits[0].FilePath != "" {
			return hits[0].FilePath
		}
	}
	return ""
}
