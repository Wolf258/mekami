//go:build !linux && !darwin

package mekami

import (
	"fmt"
	"runtime"
)

func serviceInstall() error {
	return fmt.Errorf("service-install: unsupported platform %q", runtime.GOOS)
}

func serviceUninstall() error {
	return fmt.Errorf("service-uninstall: unsupported platform %q", runtime.GOOS)
}
