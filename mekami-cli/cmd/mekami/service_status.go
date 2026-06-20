package mekami

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// ServiceStatusReport is the cross-platform shape of
// `mekami service status`. The OS-specific code in
// service_linux.go / service_darwin.go fills the fields it can
// observe; fields that do not apply on a given platform stay
// empty. The CLI formats the report as a short key/value block
// and exits 0 on success, non-zero only if the platform-specific
// query itself failed.
type ServiceStatusReport struct {
	// Registered is true when the init-system unit/agent file
	// exists on disk. It is the inverse of "service uninstall
	// has been run" — a true here means the user has at least
	// run `service install` once.
	Registered bool
	// UnitPath is the path of the unit/agent file (systemd
	// unit on Linux, launchd plist on macOS). Empty when
	// Registered is false.
	UnitPath string
	// Enabled is true when the init system has the unit marked
	// as enabled (systemd: `is-enabled` returns "enabled";
	// launchd: the agent was loaded with `-w`).
	Enabled bool
	// Active is true when the unit/agent is currently
	// running. systemd: `is-active` returns "active".
	// launchd: `launchctl list` shows a non-"-" PID column.
	Active bool
	// ActiveState is the raw state string from the init
	// system (systemd: "active", "inactive", "failed", etc.;
	// macOS: the launchctl "exit code" field, or "-" if the
	// agent is not loaded).
	ActiveState string
	// Notes is a free-form field for OS-specific
	// observations the user might want to know about
	// ("socket missing", "stale supervisor.pid", ...).
	Notes []string
}

// runServiceStatus is the runner for `mekami service status`.
// It asks the OS-specific code (serviceStatusOS) for the
// registration state of the supervisor, then prints a
// short report. With --json, prints the raw report.
func runServiceStatus(cmd *cobra.Command) error {
	report, err := serviceStatusOS()
	if err != nil {
		return cliError{code: 1, msg: "service status: " + err.Error()}
	}
	jsonMode, _ := cmd.Flags().GetBool("json")
	if jsonMode {
		return printJSON(report)
	}
	yesNo := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	fmt.Printf("registered: %s\n", yesNo(report.Registered))
	if report.UnitPath != "" {
		fmt.Printf("unit path:  %s\n", report.UnitPath)
	}
	fmt.Printf("enabled:    %s\n", yesNo(report.Enabled))
	fmt.Printf("active:     %s", yesNo(report.Active))
	if report.ActiveState != "" {
		fmt.Printf(" (%s)", report.ActiveState)
	}
	fmt.Println()
	for _, n := range report.Notes {
		fmt.Fprintf(os.Stderr, "note: %s\n", n)
	}
	return nil
}
