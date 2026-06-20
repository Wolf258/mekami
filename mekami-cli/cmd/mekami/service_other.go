//go:build !linux && !darwin

package mekami

import (
	"fmt"
	"runtime"
)

func serviceInstall() error {
	return fmt.Errorf("service install: unsupported platform %q", runtime.GOOS)
}

func serviceUninstall() error {
	return fmt.Errorf("service uninstall: unsupported platform %q", runtime.GOOS)
}

// serviceStatusOS on platforms without an init-system
// implementation is always an error: there is no unit to
// inspect. The CLI surfaces this as a clear
// "unsupported platform" message rather than a partial
// report, because a partial report would invite the user
// to trust numbers that do not exist.
func serviceStatusOS() (ServiceStatusReport, error) {
	return ServiceStatusReport{}, fmt.Errorf("service status: unsupported platform %q", runtime.GOOS)
}
