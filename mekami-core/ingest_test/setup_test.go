//go:build !integration

package ingest_test

import (
	"os"
	"testing"

	"github.com/Wolf258/mekami-api/api/v1"
)

// TestMain registers the stub Go frontend before any test runs.
// The stub satisfies api.Frontend with a minimal implementation
// (package name + top-level decls only) so ingest.Build can find
// a frontend for "go" without depending on mekami-core-go. Tests
// that need the full Go frontend (imports, refs, call edges) are
// in the integration_test/ directory and run with the
// `integration` build tag.
func TestMain(m *testing.M) {
	api.Register(stubFrontend{})
	os.Exit(m.Run())
}
