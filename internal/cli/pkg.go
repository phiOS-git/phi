package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"phi/internal/pkg"
	"phi/internal/view"
)

const pkgUsage = `usage: phi pkg <verb>

Verbs:
  list [--manager NAME] [--json]
         every explicitly-installed package, split into T0/AUR/T4/phi-packages;
         with --manager, just that manager's list
         (phi|pacman|aur|npm|flatpak|appimage — npm/flatpak are placeholders)
  check  the same as bare list, with pacman -Qu's available-update column
  state [--json]
         component versions: phi, phios-dotfiles, the installed phi-* packages

T4 (manual build outside pacman) can never be listed — pacman has no record
of software it was never told about. Q-01 forbids AUR/T4 entirely for now,
so a non-empty AUR row is a policy violation, not routine information.
`

func runPkg(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, pkgUsage)
		return 1
	}

	switch args[0] {
	case "list":
		manager, asJSON := "", false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--json":
				asJSON = true
			case "--manager":
				if i+1 < len(args) {
					i++
					manager = args[i]
				}
			default:
				fmt.Fprintf(stderr, "%s: pkg: unknown flag %q\n", progName, args[i])
				return 1
			}
		}
		if manager != "" {
			listing, err := pkg.ListManager(context.Background(), pkg.Manager(manager))
			if err != nil {
				fmt.Fprintf(stderr, "%s: pkg: %v\n", progName, err)
				return 1
			}
			if asJSON {
				b, _ := json.Marshal(listing)
				fmt.Fprintln(stdout, string(b))
				return 0
			}
			fmt.Fprint(stdout, view.PkgManager(listing))
			return 0
		}
		entries, _, err := pkg.List(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "%s: pkg: %v\n", progName, err)
			return 1
		}
		if asJSON {
			b, _ := json.Marshal(entries)
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprint(stdout, view.PkgList(entries))
		return 0

	case "state":
		asJSON := false
		for _, a := range args[1:] {
			if a == "--json" {
				asJSON = true
			}
		}
		components := pkg.SystemState(context.Background())
		if asJSON {
			b, _ := json.Marshal(components)
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprint(stdout, view.PkgState(components))
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
