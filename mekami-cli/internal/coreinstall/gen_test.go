package coreinstall

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateAllGen_Empty(t *testing.T) {
	got, err := GenerateAllGen(nil)
	if err != nil {
		t.Fatalf("GenerateAllGen: %v", err)
	}
	if !bytes.Contains(got, []byte("package all_gen")) {
		t.Errorf("missing package decl in:\n%s", got)
	}
	if !bytes.Contains(got, []byte("import ()")) {
		t.Errorf("expected empty import group in:\n%s", got)
	}
}

func TestGenerateAllGen_Populated(t *testing.T) {
	in := map[string]string{
		"go":   "v0.1.0",
		"rust": "v0.2.0",
	}
	got, err := GenerateAllGen(in)
	if err != nil {
		t.Fatalf("GenerateAllGen: %v", err)
	}
	want1 := `_ "github.com/Wolf258/mekami-core-go" // go @ v0.1.0`
	want2 := `_ "github.com/Wolf258/mekami-core-rust" // rust @ v0.2.0`
	if !bytes.Contains(got, []byte(want1)) {
		t.Errorf("missing %q in:\n%s", want1, got)
	}
	if !bytes.Contains(got, []byte(want2)) {
		t.Errorf("missing %q in:\n%s", want2, got)
	}
}

func TestGenerateAllGen_StableOrder(t *testing.T) {
	// The map iteration order is undefined in Go, but the
	// generator must sort the keys before writing the imports
	// so that successive runs produce byte-identical output.
	in := map[string]string{
		"go":     "v0.1.0",
		"rust":   "v0.2.0",
		"python": "v0.3.0",
	}
	got, err := GenerateAllGen(in)
	if err != nil {
		t.Fatalf("GenerateAllGen: %v", err)
	}
	// Find the order of the lines we care about.
	want := []string{
		`_ "github.com/Wolf258/mekami-core-go" // go @ v0.1.0`,
		`_ "github.com/Wolf258/mekami-core-python" // python @ v0.3.0`,
		`_ "github.com/Wolf258/mekami-core-rust" // rust @ v0.2.0`,
	}
	body := string(got)
	prev := -1
	for _, line := range want {
		idx := strings.Index(body, line)
		if idx < 0 {
			t.Fatalf("missing %q in:\n%s", line, body)
		}
		if idx <= prev {
			t.Errorf("line %q appeared before previous line; order is not stable", line)
		}
		prev = idx
	}
}

func TestGenerateAllGen_EmptyVersionOmitsAnnotation(t *testing.T) {
	in := map[string]string{
		"go": "",
	}
	got, err := GenerateAllGen(in)
	if err != nil {
		t.Fatalf("GenerateAllGen: %v", err)
	}
	want := `_ "github.com/Wolf258/mekami-core-go" // go`
	if !bytes.Contains(got, []byte(want)) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
	if bytes.Contains(got, []byte("@ ")) {
		t.Errorf("did not expect @ annotation for empty version, got:\n%s", got)
	}
}

func TestGenerateAllGen_RejectsInvalidName(t *testing.T) {
	in := map[string]string{
		"go":  "v0.1.0",
		"Go!": "v0.1.0", // invalid: uppercase + punctuation
	}
	if _, err := GenerateAllGen(in); err == nil {
		t.Errorf("expected error for invalid language name, got nil")
	} else if !strings.Contains(err.Error(), "Go!") {
		t.Errorf("error should name the bad entry, got: %v", err)
	}
}

func TestWriteAllGen_Idempotent(t *testing.T) {
	dir := t.TempDir()
	// Synthesize a work tree with mekami-core/frontend/all_gen/.
	target := filepath.Join(dir, "mekami-core", "frontend", "all_gen")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "all_gen.go"), []byte("package all_gen\nimport ()\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	in := map[string]string{"go": "v0.1.0", "rust": "v0.2.0"}
	if err := WriteAllGen(dir, in); err != nil {
		t.Fatalf("WriteAllGen: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(target, "all_gen.go"))
	if err != nil {
		t.Fatalf("read after first write: %v", err)
	}
	// Second call must produce identical bytes and not error.
	if err := WriteAllGen(dir, in); err != nil {
		t.Fatalf("WriteAllGen (second): %v", err)
	}
	second, err := os.ReadFile(filepath.Join(target, "all_gen.go"))
	if err != nil {
		t.Fatalf("read after second write: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("WriteAllGen not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !strings.Contains(string(first), "mekami-core-rust") {
		t.Errorf("expected rust import in generated file, got:\n%s", first)
	}
}

func TestFindWorkDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26.3\n"), 0o644); err != nil {
		t.Fatalf("write go.work: %v", err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := FindWorkDir(nested)
	if err != nil {
		t.Fatalf("FindWorkDir: %v", err)
	}
	if got != root {
		t.Errorf("FindWorkDir = %q, want %q", got, root)
	}
}
