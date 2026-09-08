package cli

import (
	"fmt"
	"io"
	"os"

	"phi/internal/update"
)

const updateUsage = `usage: phi update

Preventive snapshot (snapper -c root), pacman -Syu, then regenerate every
themed config for the current variant. INTERACTIVE: run this from a real
terminal, not a script or the settings panel — pacman may ask for
confirmation and sudo will ask for a password.
`

func runUpdate(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, updateUsage)
		return 0
	}

	fmt.Fprintln(stdout, "phi update: taking a snapshot before touching anything...")
	// os.Stdin, not the io.Writer stdout/stderr this function received:
	// this verb's own contract (internal/update's own header) is real
	// terminal passthrough, which only the process's actual stdio can give
	// a child sudo prompt — a caller that redirected stdout into a buffer
	// (a test, a future non-interactive wrapper) would silently break
	// pacman's own prompts otherwise.
	result := update.Run(os.Stdin, stdout, stderr)

	if result.SnapshotTaken {
		fmt.Fprintln(stdout, "snapshot: ok")
	} else {
		fmt.Fprintf(stdout, "snapshot: skipped (%v) — proceeding anyway\n", result.SnapshotError)
	}

	if !result.UpgradeRan {
		fmt.Fprintf(stderr, "%s: update: pacman -Syu failed: %v\n", progName, result.UpgradeError)
		fmt.Fprintln(stderr, "themed configs were NOT regenerated — fix the upgrade first")
		return 1
	}
	fmt.Fprintln(stdout, "upgrade: ok")

	if !result.ThemeApplied {
		fmt.Fprintf(stderr, "%s: update: theme regeneration failed: %v\n", progName, result.ThemeError)
		return 1
	}
	fmt.Fprintln(stdout, "theme regenerated: ok")
	return 0
}
