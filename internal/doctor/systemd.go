package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// failedUnits queries systemd directly — the boundary bin/lib/system.sh's
// phios_services_report explicitly never crosses ("querying systemd state
// is exactly the boundary this installer does not cross... at S-05 or
// ever"). phi doctor is a different tool with a different contract (S-14
// AGENT): asking systemd how it is doing is this command's entire job.
func failedUnits(ctx context.Context) Check {
	const name = "failed systemd units"

	sysOut, avail, _ := run(ctx, "systemctl", "--failed", "--plain", "--no-legend")
	if !avail {
		return Check{Name: name, Status: Unknown, Detail: "systemctl not available"}
	}
	usrOut, _, _ := run(ctx, "systemctl", "--user", "--failed", "--plain", "--no-legend")

	var failed []string
	for _, line := range strings.Split(sysOut, "\n") {
		if f := firstField(line); f != "" {
			failed = append(failed, f+" (system)")
		}
	}
	for _, line := range strings.Split(usrOut, "\n") {
		if f := firstField(line); f != "" {
			failed = append(failed, f+" (user)")
		}
	}

	if len(failed) == 0 {
		return Check{Name: name, Status: OK, Detail: "none"}
	}
	return Check{Name: name, Status: Problem, Detail: strings.Join(failed, ", ")}
}

func firstField(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	return strings.Fields(line)[0]
}

// serviceStatus checks the actual systemd state of every unit
// profiles/*/services-{user,system}.txt declares for host — the read side
// of the boundary phios_services_report only ever prints (bin/lib/
// system.sh): that function states what should exist and performs none of
// it; this reports what actually does. "inactive" is not on its own a
// Problem — profiles/desktop/services-user.txt's own comment documents that
// Arch never auto-enables user units on install, so a freshly declared unit
// sitting inactive until enabled by hand is the expected state, not drift.
// Only "failed" is.
func serviceStatus(ctx context.Context, root, host string, profiles []string, found bool, profilesErr error) Check {
	const name = "service status"

	if profilesErr != nil {
		return Check{Name: name, Status: Unknown, Detail: fmt.Sprintf("cannot read hosts/%s.txt: %v", host, profilesErr)}
	}
	if !found {
		return Check{Name: name, Status: Unknown, Detail: fmt.Sprintf("hosts/%s.txt not found; %s is not a phiOS-declared host", host, host)}
	}

	type unit struct{ scope, name, profile string }
	var units []unit
	for _, scope := range []string{"user", "system"} {
		for _, profile := range profiles {
			list, err := readList(filepath.Join(root, "profiles", profile, "services-"+scope+".txt"))
			if err != nil {
				return Check{Name: name, Status: Unknown, Detail: err.Error()}
			}
			for _, u := range list {
				units = append(units, unit{scope, u, profile})
			}
		}
	}
	if len(units) == 0 {
		return Check{Name: name, Status: OK, Detail: "none declared"}
	}

	var lines []string
	status := OK
	for _, u := range units {
		var out string
		var avail bool
		if u.scope == "user" {
			out, avail, _ = run(ctx, "systemctl", "--user", "is-active", u.name)
		} else {
			out, avail, _ = run(ctx, "systemctl", "is-active", u.name)
		}
		if !avail {
			return Check{Name: name, Status: Unknown, Detail: "systemctl not available"}
		}
		state := out
		if state == "" {
			state = "unknown"
		}
		if state == "failed" {
			status = Problem
		}
		lines = append(lines, fmt.Sprintf("%s (%s, %s): %s", u.name, u.scope, u.profile, state))
	}
	return Check{Name: name, Status: status, Detail: strings.Join(lines, "\n")}
}
