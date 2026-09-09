package agent

import (
	"fmt"
	"os/exec"
)

// RestartA1 restarts the A1 engine user service so the containment is
// rebuilt for the newly-selected project (phios-agente.md §4.3: "il cambio
// di progetto ricostruisce il contenimento e riavvia il servizio"). A
// failure here is reported, not fatal: the active-project marker is
// already written, and the user can restart the unit by hand.
func RestartA1() error {
	return restartUnit("phi-agent-a1.service")
}

func restartUnit(unit string) error {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return fmt.Errorf("systemctl not found; restart %s by hand", unit)
	}
	cmd := exec.Command(path, "--user", "restart", unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user restart %s: %v: %s", unit, err, string(out))
	}
	return nil
}
