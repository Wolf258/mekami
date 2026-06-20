//go:build windows

package watch

import (
	"net"
	"path/filepath"
	"time"
)

// listenIPC binds a local IPC endpoint as a Windows named pipe.
// path is the same shape the daemon uses on Unix (an absolute
// filesystem path); we strip it down to a basename and use it as
// the pipe name. The full name becomes \\.\pipe\<basename>.
//
// Named pipes and Unix domain sockets expose the same net.Listener
// / net.Conn surface, so the rest of the watch package does not
// need to know which transport is in use.
func listenIPC(path string) (net.Listener, error) {
	return net.Listen("pipe", pipeName(path))
}

// dialIPC connects to a local IPC endpoint created by listenIPC.
func dialIPC(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("pipe", pipeName(path), timeout)
}

// pipeName maps a filesystem-style path to a Windows named-pipe
// address (\\.\pipe\<basename>). See the equivalent in
// internal/supervisor/ipc_transport_windows.go for the full
// rationale.
func pipeName(path string) string {
	return `\\.\pipe\` + filepath.Base(path)
}
