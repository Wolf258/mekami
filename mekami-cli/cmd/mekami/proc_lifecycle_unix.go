//go:build !windows

package mekami

import (
	"os/exec"
	"syscall"
)

// detachSysProcAttr returns the platform-specific SysProcAttr that
// detaches a child process from the parent (new session on Unix,
// no console window + no inherited handles on Windows). The
// Windows counterpart lives in proc_lifecycle_windows.go.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// configureDetachedProcess applies the standard "fork and forget"
// settings to cmd: the platform-specific detach attributes, and
// stdin/stdout/stderr redirected to os.DevNull so the child cannot
// write to the parent's tty. Caller still needs to call
// cmd.Start() and release the process handle.
func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = detachSysProcAttr()
	cmd.Stdin = devNullReader()
	cmd.Stdout = devNullWriter()
	cmd.Stderr = devNullWriter()
}
