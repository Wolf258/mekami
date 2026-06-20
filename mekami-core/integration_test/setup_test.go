//go:build integration

package integration_test

import (
	"os"
	"testing"

	_ "github.com/Wolf258/mekami-core-go"
)

// TestMain ensures the real Go frontend is registered before any
// integration test runs. This file is only compiled with the
// `integration` build tag, so mekami-core-go is a test-only
// dependency. To run the integration suite locally:
//
//	go get github.com/Wolf258/mekami-core-go
//	go test -tags=integration ./integration_test/...
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
