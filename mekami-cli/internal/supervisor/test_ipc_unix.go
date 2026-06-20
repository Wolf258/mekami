//go:build !windows

package supervisor

import "net"

// listenIPCLocal binds a local IPC endpoint for test fakes.
// On Unix this is a Unix domain socket at path. The supervisor
// package's own listenIPC / dialIPC already do the right thing
// per platform, so test fakes that need a peer (startFakeDaemon,
// startFakeSupervisor) reuse them to stay in lockstep with the
// production transport.
func listenIPCLocal(path string) (net.Listener, error) {
	return listenIPC(path)
}
