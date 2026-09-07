package cli

import (
	"context"
	"fmt"
	"io"

	"phi/internal/doctor"
	"phi/internal/tokens"
	"phi/internal/view"
)

const doctorUsage = `usage: phi doctor

Composes: disk space, failed systemd units, dotfiles drift (bin/phios-
install --check), SMART status, service status (profiles/*/services-*.txt
against the machine), and package categories (pacman -Qm against CLAUDE.md's
T0 + phi-packages-only rule). On mini, also /srv's mount state.

Exit code is non-zero exactly when some check reports PROBLEM — usable from
a timer.
`

// runDoctor never refuses to run for want of a phios-dotfiles checkout —
// doctor.Run reports that through the specific checks that need it, and
// still runs the ones that don't (disk, systemd, SMART, package policy).
func runDoctor(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if args[0] == "-h" || args[0] == "--help" {
			fmt.Fprint(stdout, doctorUsage)
			return 0
		}
		fmt.Fprint(stderr, doctorUsage)
		return 1
	}

	root, rootErr := tokens.Root()
	report := doctor.Run(context.Background(), root, rootErr)
	fmt.Fprint(stdout, view.Doctor(report))
	return report.ExitCode()
}
