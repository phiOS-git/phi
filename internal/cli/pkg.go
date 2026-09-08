package cli

import (
	"context"
	"fmt"
	"io"

	"phi/internal/pkg"
	"phi/internal/view"
)

const pkgUsage = `usage: phi pkg <verb>

Verbs:
  list   every explicitly-installed package, split into T0/AUR/T4/phi-packages
  check  the same, with pacman -Qu's available-update column overlaid

T4 (manual build outside pacman) can never be listed by either verb —
pacman has no record of software it was never told about. Q-01 forbids
AUR/T4 entirely for now, so a non-empty AUR row is a policy violation, not
routine information.
`

func runPkg(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, pkgUsage)
		return 1
	}

	switch args[0] {
	case "list":
		entries, _, err := pkg.List(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "%s: pkg: %v\n", progName, err)
			return 1
		}
		fmt.Fprint(stdout, view.PkgList(entries))
		return 0
	case "check":
		result, err := pkg.Check(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "%s: pkg: %v\n", progName, err)
			return 1
		}
		fmt.Fprint(stdout, view.PkgCheck(result))
		return 0
	case "-h", "--help":
		fmt.Fprint(stdout, pkgUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: pkg: unknown verb %q\n", progName, args[0])
		fmt.Fprint(stderr, pkgUsage)
		return 1
	}
}
