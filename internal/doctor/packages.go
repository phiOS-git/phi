package doctor

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// phiPackageName matches this project's own naming convention
// (phi-packages/README.md: "phi, phi-<component>") — a heuristic, since
// pacman has no query that reports which sync repository a package came from.
// Flagged as such rather than hidden.
var phiPackageName = regexp.MustCompile(`^phi(-.+)?$`)

// packageCategories audits what pacman has installed. Only T0 (Arch) and
// phi-packages are allowed; AUR and T4 packages are policy violations. Uses
// `pacman -Qm` to find foreign packages (anything not from a sync repo).
// Does not check which declared packages are missing; that's dotfiles-drift's
// job.
func packageCategories(ctx context.Context) Check {
	const name = "package categories"

	foreignOut, avail, foreignErr := run(ctx, "pacman", "-Qm")
	if !avail {
		return Check{Name: name, Status: Unknown, Detail: "pacman not available"}
	}
	// pacman -Q's filters exit 1 with empty output for "nothing matched" —
	// the normal, expected case here (no foreign packages). A real failure
	// (corrupt db, unreadable config) still exits non-zero but prints
	// something; only that combination is treated as an error, so a clean
	// "zero foreign packages" is never confused with "could not check."
	if foreignErr != nil && strings.TrimSpace(foreignOut) != "" {
		return Check{Name: name, Status: Unknown, Detail: "pacman -Qm: " + lastNonEmptyLine(foreignOut)}
	}
	foreign := nonEmptyLines(foreignOut)

	explicitOut, _, explicitErr := run(ctx, "pacman", "-Qe")
	if explicitErr != nil && strings.TrimSpace(explicitOut) != "" {
		return Check{Name: name, Status: Unknown, Detail: "pacman -Qe: " + lastNonEmptyLine(explicitOut)}
	}
	var phiCount, otherCount int
	for _, line := range nonEmptyLines(explicitOut) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if phiPackageName.MatchString(fields[0]) {
			phiCount++
		} else {
			otherCount++
		}
	}

	detail := fmt.Sprintf("foreign (AUR/T4, should be none): %d; phi-packages: %d; other T0 explicit: %d",
		len(foreign), phiCount, otherCount)
	if len(foreign) > 0 {
		detail += "\n  " + strings.Join(foreign, "\n  ")
	}

	status := OK
	if len(foreign) > 0 {
		status = Problem
	}
	return Check{Name: name, Status: status, Detail: detail}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
