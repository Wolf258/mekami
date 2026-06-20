//go:build windows

package supervisor

import "syscall"

// detachSysProcAttr returns the *syscall.SysProcAttr used to detach
// a child process from the parent's console on Windows. The flags
// below are the Windows equivalents of Unix's "new session + close
// stdio":
//
//   - windows.DETACHED_PROCESS (0x00000008) — the new process does
//     not inherit the parent's console. Without this, a Go binary
//     launched from cmd.exe would pop up a console window.
//   - windows.CREATE_NO_WINDOW (0x00000002) — the process is
//     started without a window. Belt-and-suspenders for tools that
//     sometimes allocate a console for stderr.
//
// On Windows, "syscall.SysProcAttr" carries a CreationFlags field
// (uint32) rather than Setsid. The combination above matches the
// behaviour of `nohup` on Unix: the child survives the parent's
// exit and does not show a console window.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000002, // DETACHED_PROCESS | CREATE_NO_WINDOW
	}
}
