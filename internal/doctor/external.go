package doctor

import (
	"context"
	"fmt"

	"phi/internal/external"
)

// externalDeclarations audits non-official software — Flatpak, ~/Applications,
// ~/.local/opt, rootless containers, and the leak paths a language package
// manager must never populate — against each active profile's external.txt.
// It calls internal/external.Audit directly as a Go function: unlike
// dotfilesDrift's precedent of shelling out to bin/phios-install, there is
// exactly one implementation of this logic and two callers (this check and
// `phi pkg audit`), so a second process boundary here would buy nothing but
// a slower, harder-to-test duplicate.
//
// packageCategories (packages.go) is deliberately left asserting zero
// foreign pacman packages, not "zero undeclared": external.txt starts at
// T2, and T0/T1 are declared in packages.txt, so nothing can ever
// legitimately declare a foreign pacman package. Relaxing that check to
// "only when undeclared" would make it a no-op dressed as a relaxation.
// This check instead covers the populations pacman -Qm never saw — Flatpak,
// AppImages, ~/.local/opt, containers and the leak paths — so "zero
// undeclared" holds across the union of the two checks rather than inside
// either one.
func externalDeclarations(ctx context.Context, root, host string, profiles []string, found bool, profilesErr error) Check {
	const name = "external declarations"

	if profilesErr != nil {
		return Check{Name: name, Status: Unknown, Detail: fmt.Sprintf("cannot read hosts/%s.txt: %v", host, profilesErr)}
	}
	if !found {
		return Check{Name: name, Status: Unknown, Detail: fmt.Sprintf("hosts/%s.txt not found; %s is not a phiOS-declared host", host, host)}
	}

	report, err := external.Audit(ctx, external.Config{Root: root, Profiles: profiles})
	if err != nil {
		return Check{Name: name, Status: Unknown, Detail: err.Error()}
	}

	status := OK
	if len(report.Problems) > 0 || len(report.Findings) > 0 {
		status = Problem
	}

	detail := fmt.Sprintf("%d declared, %d parse problem(s), %d finding(s)",
		len(report.Entries), len(report.Problems), len(report.Findings))
	for _, p := range report.Problems {
		detail += fmt.Sprintf("\n  %s:%d: %s", p.File, p.Line, p.Detail)
	}
	for _, f := range report.Findings {
		detail += fmt.Sprintf("\n  [%s/%s] %s: %s", f.Check, f.Kind, f.Name, f.Detail)
	}
	return Check{Name: name, Status: status, Detail: detail}
}
