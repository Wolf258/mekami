package grep_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/grep"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGrepSimpleMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package x\n// TODO: refactor\nfunc F(){}\n")
	writeFile(t, dir, "b.md", "TODO write docs\n")

	res, err := grep.Grep(context.Background(), grep.Options{
		Pattern: `TODO`,
		Root:    dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Fatalf("expected 2 matches, got %d (%+v)", res.Total, res.Matches)
	}
	if res.Truncated {
		t.Fatal("did not expect truncated")
	}
}

func TestGrepIncludeExtFilter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "TODO go\n")
	writeFile(t, dir, "b.md", "TODO md\n")

	res, err := grep.Grep(context.Background(), grep.Options{
		Pattern:    `TODO`,
		Root:       dir,
		IncludeExt: []string{"go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Fatalf("expected 1 match in .go, got %d", res.Total)
	}
	if len(res.Matches) == 0 || filepath.Ext(res.Matches[0].Path) != ".go" {
		t.Fatalf("expected only .go matches, got %+v", res.Matches)
	}
}

func TestGrepPathPrefix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "internal/x.go", "TODO inside\n")
	writeFile(t, dir, "cmd/x.go", "TODO outside\n")

	res, err := grep.Grep(context.Background(), grep.Options{
		Pattern:    `TODO`,
		Root:       dir,
		PathPrefix: "internal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Fatalf("expected 1 match under internal/, got %d", res.Total)
	}
}

func TestGrepMaxResultsTruncates(t *testing.T) {
	dir := t.TempDir()
	var content string
	for i := 0; i < 50; i++ {
		content += "TODO line\n"
	}
	writeFile(t, dir, "a.go", content)

	res, err := grep.Grep(context.Background(), grep.Options{
		Pattern:    `TODO`,
		Root:       dir,
		MaxResults: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatalf("expected Truncated=true, got false (total=%d, returned=%d)", res.Total, len(res.Matches))
	}
	if len(res.Matches) > 10 {
		t.Fatalf("expected at most 10 matches, got %d", len(res.Matches))
	}
}

func TestGrepContextLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "line1\nline2\nline3\nTODO hit\nline5\nline6\n")

	res, err := grep.Grep(context.Background(), grep.Options{
		Pattern: `TODO`,
		Root:    dir,
		Context: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 2 lines before + the match itself.
	if res.Total != 1 {
		t.Fatalf("expected 1 match, got %d", res.Total)
	}
	if len(res.Matches) != 3 {
		t.Fatalf("expected 3 entries (2 context + 1 match), got %d: %+v", len(res.Matches), res.Matches)
	}
	if res.Matches[0].Line != 2 {
		t.Fatalf("expected first context line to be line 2, got %d", res.Matches[0].Line)
	}
	if res.Matches[2].Line != 4 {
		t.Fatalf("expected match on line 4, got %d", res.Matches[2].Line)
	}
}

func TestGrepInvalidPattern(t *testing.T) {
	dir := t.TempDir()
	_, err := grep.Grep(context.Background(), grep.Options{
		Pattern: "([",
		Root:    dir,
	})
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestGrepInvalidArgs(t *testing.T) {
	dir := t.TempDir()
	if _, err := grep.Grep(context.Background(), grep.Options{Root: dir}); err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if _, err := grep.Grep(context.Background(), grep.Options{Pattern: "x"}); err == nil {
		t.Fatal("expected error for empty root")
	}
}

func TestGrepIgnoresHiddenAndBuildDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "TODO visible\n")
	writeFile(t, dir, ".git/hooks/a.go", "TODO hidden\n")
	writeFile(t, dir, "node_modules/x/a.go", "TODO nm\n")
	writeFile(t, dir, "vendor/x/a.go", "TODO vendor\n")

	res, err := grep.Grep(context.Background(), grep.Options{
		Pattern: `TODO`,
		Root:    dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Fatalf("expected 1 visible match, got %d (%+v)", res.Total, res.Matches)
	}
}

func TestGrepReuseCompiledRegex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "TODO foo\n")
	re := regexp.MustCompile(`TODO`)

	res, err := grep.Grep(context.Background(), grep.Options{
		Pattern:  `TODO`,
		Compiled: re,
		Root:     dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Fatalf("expected 1 match, got %d", res.Total)
	}
}
