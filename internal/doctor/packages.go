package doctor

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// phiPackageName matches this project's own naming convention
// (phi-packages/README.md: "phi, phi-<component>") — a heuristic, since
// pacman has no query that answers "which sync repository did this come
// from" for an already-installed package; flagged as such rather than
// hidden, the same way S-04 flagged its device-name heuristics.
var phiPackageName = regexp.MustCompile(`^phi(-.+)?$`)

// packageCategories audits what pacman actually has installed against
// CLAUDE.md rule 6 (T0 + phi-packages only; AUR/T4 deferred under Q-01):
// `pacman -Qm` lists foreign packages, meaning anything pacman did not get
// from a configured sync repository. After S-11 registers [phi] as a sync
// repo, a non-empty result here is exactly what that rule forbids — an
// AUR or manually built package on the machine. This says nothing about
// which declared packages are missing; the dotfiles-drift check already
// owns that, so this does not repeat it.
func packageCategories(ctx context.Context) Check {
	const name = "package categories"

	foreignOut, avail, _ := run(ctx, "pacman", "-Qm")
	if !avail {
		return Check{Name: name, Status: Unknown, Detail: "pacman not available"}
	}
	foreign := nonEmptyLines(foreignOut)

	explicitOut, _, _ := run(ctx, "pacman", "-Qe")
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

	detail := fmt.Sprintf("foreign (AUR/T4, should be none — Q-01 deferred): %d; phi-packages: %d; other T0 explicit: %d",
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
