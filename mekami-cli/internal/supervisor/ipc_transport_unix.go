//go:build !windows

package supervisor

import (
	"net"
	"time"
)

// listenIPC binds a local IPC endpoint. On Unix this is a Unix
// domain socket at path; on Windows (see ipc_transport_windows.go)
// it is a named pipe derived from the same path.
//
// The returned listener's address is opaque; callers should not
// assume a network or scheme.
func listenIPC(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}

// dialIPC connects to a local IPC endpoint created by listenIPC.
// timeout bounds the dial; once connected, callers can set their
// own read/write deadlines on the returned conn.
func dialIPC(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, timeout)
}
