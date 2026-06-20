//go:build integration
// +build integration

package integration_test

import (
	"context"
	"bytes"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-core/ingest"
)

func TestProgress_NonTTYOneLinePerEvent(t *testing.T) {
	var buf bytes.Buffer
	p := ingest.NewProgress(context.Background(), &buf, false)
	p.Event("ingest", "a.go")
	p.Event("delete", "b.go")
	p.Event("skip", "c.go: oops")
	p.Done()
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), out)
	}
	want := []string{"ingest a.go", "delete b.go", "skip c.go: oops"}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d: got %q, want %q", i, lines[i], w)
		}
	}
}

func TestProgress_QuietSuppresses(t *testing.T) {
	var buf bytes.Buffer
	p := ingest.NewProgress(context.Background(), &buf, true)
	p.Event("ingest", "a.go")
	p.Done()
	if buf.Len() != 0 {
		t.Fatalf("quiet should suppress output, got %q", buf.String())
	}
}

func TestProgress_TTYUsesCarriageReturn(t *testing.T) {
	var buf bytes.Buffer
	p := &ingest.Progress{Ctx: context.Background(), Out: &buf, Tty: true}
	p.Event("ingest", "a.go")
	p.Event("delete", "b.go")
	p.Done()
	out := buf.String()
	if !strings.HasPrefix(out, "\ringest  a.go\033[K") {
		t.Errorf("expected CR + ingest event, got %q", out)
	}
	if !strings.Contains(out, "\rdelete  b.go\033[K") {
		t.Errorf("expected CR + delete event, got %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("expected trailing newline after done(), got %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("TTY mode should emit exactly one newline (from done), got %d in %q", strings.Count(out, "\n"), out)
	}
}

func TestProgress_CancelledContextSuppresses(t *testing.T) {
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := ingest.NewProgress(ctx, &buf, false)
	p.Event("ingest", "a.go")
	p.Done()
	if buf.Len() != 0 {
		t.Fatalf("cancelled context should suppress output, got %q", buf.String())
	}
}
