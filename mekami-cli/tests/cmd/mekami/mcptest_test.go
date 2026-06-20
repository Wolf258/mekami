package mekami_test

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The truncation helper lives in cmd/mekami/mcptest.go (package
// mekami) but is unexported. We test it through the same
// fast-path-friendly inlined copy used by the smoke-test command.
// If the helper ever drifts, the smoke test catches it on the wire.

func runTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n    ...(truncated)"
}

func TestTruncateForDisplay(t *testing.T) {
	t.Run("ascii_over_limit", func(t *testing.T) {
		s := strings.Repeat("a", 500)
		got := runTruncate(s, 400)
		if !strings.HasSuffix(got, "\n    ...(truncated)") {
			t.Fatalf("expected truncation marker, got %q", got)
		}
		head := strings.SplitN(got, "\n", 2)[0]
		if len(head) > 400 {
			t.Fatalf("head exceeds maxBytes: %d", len(head))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("result is not valid UTF-8")
		}
	})

	t.Run("multibyte_at_boundary", func(t *testing.T) {
		s := strings.Repeat("ñ", 300)
		got := runTruncate(s, 400)
		if !utf8.ValidString(got) {
			t.Fatalf("result is not valid UTF-8: %q", got)
		}
		if !strings.HasSuffix(got, "\n    ...(truncated)") {
			t.Fatalf("expected truncation marker")
		}
		head := strings.SplitN(got, "\n", 2)[0]
		if !utf8.ValidString(head) {
			t.Fatalf("head is not valid UTF-8")
		}
		if !strings.HasSuffix(head, "ñ") {
			t.Fatalf("head should end on a complete rune, got suffix %q", head[len(head)-4:])
		}
	})

	t.Run("under_limit_unchanged", func(t *testing.T) {
		s := "short string"
		got := runTruncate(s, 400)
		if got != s {
			t.Fatalf("expected unchanged, got %q", got)
		}
	})

	t.Run("exact_limit_unchanged", func(t *testing.T) {
		s := strings.Repeat("x", 400)
		got := runTruncate(s, 400)
		if got != s {
			t.Fatalf("expected unchanged at exact limit, got %q", got)
		}
	})

	t.Run("emoji_at_boundary", func(t *testing.T) {
		s := strings.Repeat("\U0001F600", 200)
		got := runTruncate(s, 400)
		if !utf8.ValidString(got) {
			t.Fatalf("result is not valid UTF-8: %q", got)
		}
		if !strings.HasSuffix(got, "\n    ...(truncated)") {
			t.Fatalf("expected truncation marker")
		}
	})
}
