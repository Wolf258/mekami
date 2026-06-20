package queries

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// SourceSlice reads a range of source lines from a file in the graph.
// `path` is resolved via ResolveFilePath (exact then shortest-suffix
// match). The file is read from disk relative to last_root.
func SourceSlice(ctx context.Context, s *store.Store, path string, startLine, endLine, maxLines int) ([]model.SourceLine, error) {
	root, err := s.GetMeta(ctx, store.MetaLastRoot)
	if err != nil {
		return nil, err
	}
	resolved, err := ResolveFilePath(ctx, s, path)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return nil, fmt.Errorf("file not found in graph: %q (run 'mekami build' if the file was added recently)", path)
	}
	abs := filepath.Join(root, resolved)
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if startLine < 1 {
		startLine = 1
	}
	if endLine <= 0 || endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine {
		startLine = endLine
	}
	if maxLines > 0 && endLine-startLine+1 > maxLines {
		endLine = startLine + maxLines - 1
	}
	out := make([]model.SourceLine, 0, endLine-startLine+1)
	for i := startLine; i <= endLine; i++ {
		out = append(out, model.SourceLine{Line: i, Content: lines[i-1]})
	}
	return out, nil
}
