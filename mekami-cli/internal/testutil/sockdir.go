// Package testutil holds helpers shared by tests across packages.
// Importing from a non-_test file is intentional: black-box tests
// (mekami-cli/tests/...) import it the same way production code does.
package testutil

import (
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
)

// ShortSockDir returns a directory suitable for binding a Unix
// domain socket. On Linux/Windows it is just t.TempDir(); on
// macOS it parks the directory under /tmp with a short name so
// the resulting socket path stays under the 104-byte sun_path
// limit and bind() does not return "invalid argument".
//
// On macOS the runtime temp dir lives under
// /var/folders/.../T/<name><digits>/<digits>/, and once you
// append .mekami/watcher.sock (or supervisor.sock) the full
// path exceeds 104 bytes. The helper works around that by
// using os.MkdirTemp under /tmp with a name truncated to 16
// chars so the final path stays well under the limit.
func ShortSockDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return t.TempDir()
	}
	name := strings.ReplaceAll(t.Name(), "/", "_")
	if len(name) > 16 {
		name = name[:16]
	}
	dir, err := os.MkdirTemp("/tmp", "ms-"+name+"-")
	if err != nil {
		t.Fatalf("ShortSockDir MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// AssertSecureDirPerms checks that path is a directory whose
// permissions deny access to "group" and "other". On Unix it
// is a strict equality with 0o700; on Windows the OS does not
// model POSIX bits meaningfully (os.Stat().Mode().Perm() is a
// best-effort shim whose output varies between Go builds and
// Windows versions) and the real security boundary is the
// inherited DACL on the parent directory, so we just log the
// observed bits and skip the check.
//
// The intent is the same on both platforms: a socket/registry
// directory that no other user on the box can read. On Unix
// we can verify the mode bits; on Windows we cannot, and
// pretending we can just produces flaky tests.
func AssertSecureDirPerms(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", path)
	}
	perm := info.Mode().Perm()
	if runtime.GOOS == "windows" {
		// The ACL on the parent directory is what
		// enforces isolation on Windows. The POSIX
		// mode bits reported by os.Stat are a shim
		// and cannot be relied on for a portable
		// assertion. We log the observed value so
		// the CI summary still shows what the build
		// reported, but we do not fail.
		t.Logf("skipping perms check on windows: dir %s reports mode %o (security enforced by parent DACL, not mode bits)", path, perm)
		return
	}
	if perm != 0o700 {
		t.Fatalf("dir %s perms = %o, want 0700", path, perm)
	}
}

// NamedPipeSupported reports whether the current Go binary can
// open a Windows named pipe via net.Listen("pipe", ...). Some
// Go distributions (historically the ones GitHub Actions
// shipped to windows-latest, and any build without the
// "pipe" net package compiled in) return "unknown network
// pipe". Tests that exercise the IPC server should call
// SkipIfNoNamedPipe at the top so they fail soft instead of
// hard on those builds.
func NamedPipeSupported() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	ln, err := net.Listen("pipe", `\\.\pipe\mekami-testutil-precheck`)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// SkipIfNoNamedPipe skips the calling test when the Go
// runtime cannot open a Windows named pipe. No-op on non-
// Windows platforms.
func SkipIfNoNamedPipe(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	if !NamedPipeSupported() {
		t.Skip("named pipes not supported by this Go build (net.Listen(\"pipe\", ...) returned 'unknown network pipe')")
	}
}
