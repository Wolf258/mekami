//go:build windows

package mekami

import (
	"os/exec"
	"syscall"
)

// detachSysProcAttr returns the platform-specific SysProcAttr that
// detaches a child process from the parent. On Windows there is no
// concept of a session; the equivalent of `setsid` is to start the
// child without a console window and without inheriting the
// parent's console handles.
//
//   - windows.DETACHED_PROCESS (0x00000008): the new process does
//     not inherit the parent's console.
//   - windows.CREATE_NO_WINDOW (0x00000002): the process is started
//     without a window. Belt-and-suspenders for tools that
//     sometimes allocate a console for stderr.
//
// Without these flags, a daemon launched from cmd.exe would pop up
// a console window each time, which is unacceptable for a
// background watcher.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000002, // DETACHED_PROCESS | CREATE_NO_WINDOW
	}
}

// configureDetachedProcess applies the standard "fork and forget"
// settings to cmd: the platform-specific detach attributes, and
// stdin/stdout/stderr redirected to os.DevNull (NUL on Windows) so
// the child cannot write to the parent's console.
func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = detachSysProcAttr()
	cmd.Stdin = devNullReader()
	cmd.Stdout = devNullWriter()
	cmd.Stderr = devNullWriter()
}
