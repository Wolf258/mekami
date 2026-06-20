package path_test

import (
	"errors"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/path"
)

// TestWrapError covers the three outcomes WrapError maps to:
// nil in -> nil out, ErrSameSymbol -> PathSameSymbol, *ErrSymbolNotFound
// -> PathSymbolNotFound, anything else -> PathOther with the
// underlying error preserved via Unwrap.
func TestWrapError(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := path.WrapError(nil); got != nil {
			t.Fatalf("nil in: got %v, want nil", got)
		}
	})
	t.Run("same_symbol", func(t *testing.T) {
		err := path.WrapError(path.ErrSameSymbol)
		var pe *path.Error
		if !errors.As(err, &pe) {
			t.Fatalf("expected *path.Error, got %T", err)
		}
		if pe.Kind != path.PathSameSymbol {
			t.Fatalf("kind: got %v, want PathSameSymbol", pe.Kind)
		}
	})
	t.Run("symbol_not_found", func(t *testing.T) {
		err := path.WrapError(&path.ErrSymbolNotFound{QName: "foo.Bar"})
		var pe *path.Error
		if !errors.As(err, &pe) {
			t.Fatalf("expected *path.Error, got %T", err)
		}
		if pe.Kind != path.PathSymbolNotFound {
			t.Fatalf("kind: got %v, want PathSymbolNotFound", pe.Kind)
		}
		if pe.QName != "foo.Bar" {
			t.Fatalf("qname: got %q, want foo.Bar", pe.QName)
		}
	})
	t.Run("other", func(t *testing.T) {
		other := errors.New("boom")
		err := path.WrapError(other)
		var pe *path.Error
		if !errors.As(err, &pe) {
			t.Fatalf("expected *path.Error, got %T", err)
		}
		if pe.Kind != path.PathOther {
			t.Fatalf("kind: got %v, want PathOther", pe.Kind)
		}
		if !errors.Is(err, other) {
			t.Fatalf("expected Unwrap to expose the original error")
		}
	})
}
