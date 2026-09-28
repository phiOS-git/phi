package agent

import (
	"os/exec"
)

// unitActive reports whether a systemd user unit is active, tolerating a
// missing systemctl (reported as not active — callers turn that into their
// own clear error rather than a bare exec failure).
func unitActive(unit string) bool {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return false
	}
	cmd := exec.Command(path, "--user", "is-active", "--quiet", unit)
	return cmd.Run() == nil
}
