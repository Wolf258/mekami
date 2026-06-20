//go:build windows

package supervisor

import (
	"net"
	"path/filepath"
)

// listenIPCLocal binds a local IPC endpoint for test fakes on
// Windows. Production code already routes through listenIPC
// (named pipe), so fakes must use the same transport or the
// supervisor's Client (dialIPC) won't be able to reach them.
//
// We derive the pipe name from the same path the Unix version
// would have used: the basename keeps the name unique per
// test (each test uses t.TempDir()) and matches what the
// production listenIPC does.
func listenIPCLocal(path string) (net.Listener, error) {
	return net.Listen("pipe", `\\.\pipe\`+filepath.Base(path))
}
