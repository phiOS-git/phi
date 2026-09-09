package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Best-effort Hyprland window lookup for the A2 session store (D-07). A
// terminal `phi agent code` records the address of the terminal it runs in so
// the panel's "Focus terminal" action has a window to raise. This is NOT a
// containment surface — it is a read of the compositor's own client list,
// outside A2's namespace, and an empty result just disables one button.

// selfTerminalWindowAddr returns the Hyprland address of the window this
// process is running under, matched by walking the process ancestry against
// `hyprctl clients -j`. Returns "" when Hyprland is not running, hyprctl is
// absent, or no ancestor owns a window (a service, a pipe, a detached spawn).
// Ancestry match rather than `activewindow` on purpose: a freshly spawned
// terminal is not reliably focused yet when this runs.
func selfTerminalWindowAddr() string {
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
		return ""
	}
	hyprctl, err := exec.LookPath("hyprctl")
	if err != nil {
		return ""
	}
	out, err := exec.Command(hyprctl, "clients", "-j").Output()
	if err != nil {
		return ""
	}
	var clients []struct {
		Address string `json:"address"`
		PID     int    `json:"pid"`
	}
	if err := json.Unmarshal(out, &clients); err != nil {
		return ""
	}
	ancestors := selfPIDChain()
	for _, c := range clients {
		if c.PID > 0 && ancestors[c.PID] {
			return c.Address
		}
	}
	return ""
}

// selfPIDChain walks /proc from this process towards pid 1, returning the set
// of pids on the way (this process included). Empty off Linux.
func selfPIDChain() map[int]bool {
	seen := map[int]bool{}
	pid := os.Getpid()
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			break
		}
		ppid, ok := parseStatPPID(string(b))
		if !ok {
			break
		}
		pid = ppid
	}
	return seen
}

// parseStatPPID pulls field 4 (ppid) out of a /proc/<pid>/stat line. comm
// (field 2) is parenthesised and may itself contain spaces and ')', so the
// scan starts after the last ')'.
func parseStatPPID(stat string) (int, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 || i+2 >= len(stat) {
		return 0, false
	}
	fields := strings.Fields(stat[i+2:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}
