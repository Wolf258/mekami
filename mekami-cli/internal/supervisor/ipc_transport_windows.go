//go:build windows

package supervisor

import (
	"net"
	"path/filepath"
	"time"
)

// listenIPC binds a local IPC endpoint as a Windows named pipe.
// path is the same shape the supervisor and daemons use on Unix
// (an absolute filesystem path); we strip it down to a basename
// and use it as the pipe name. The full name becomes
// \\.\pipe\<basename>.
//
// Named pipes and Unix domain sockets expose the same net.Listener
// / net.Conn surface, so the rest of the supervisor code does not
// need to know which transport is in use.
func listenIPC(path string) (net.Listener, error) {
	return net.Listen("pipe", pipeName(path))
}

// dialIPC connects to a local IPC endpoint created by listenIPC.
// timeout bounds the dial; once connected, callers can set their
// own read/write deadlines on the returned conn.
func dialIPC(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("pipe", pipeName(path), timeout)
}

// pipeName maps a filesystem-style path (e.g.
// C:\Users\me\AppData\Roaming\mekami\supervisor\supervisor.sock)
// to a Windows named-pipe address (\\.\pipe\supervisor.sock).
//
// The basename is sufficient: pipes are not directory-scoped, so
// only the name has to be unique on the local machine. We use
// the full basename (including the .sock extension on Unix-style
// paths) for symmetry with the Unix path; the Windows pipe
// namespace accepts arbitrary characters except a small set we do
// not use here.
func pipeName(path string) string {
	return `\\.\pipe\` + filepath.Base(path)
}
