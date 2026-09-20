package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"phi/internal/external"
	"phi/internal/pkg"
	"phi/internal/view"
)

const pkgUsage = `usage: phi pkg <verb>

Verbs:
  list [--manager NAME] [--json]
         every explicitly-installed package, split into T0/AUR/T4/phi-packages;
         with --manager, just that manager's list
         (phi|pacman|aur|npm|flatpak|appimage|external —
         npm/flatpak are placeholders)
  check  the same as bare list, with pacman -Qu's available-update column
  state [--json]
         component versions: phi, phios-dotfiles, the installed phi-* packages
  audit [--json]
         drift, leak and integrity checks against every active profile's
         external.txt (tiers TC/T2/T3/T4); exits non-zero when it finds
         anything
  accept <root>
         re-baseline one fingerprint root (applications|opt|flatpak|
         local-bin) after a legitimate change

T4 (manual build outside pacman) can never be listed — pacman has no record
of software it was never told about. Policy forbids AUR packages entirely,
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

	case "audit":
		asJSON := false
		for _, a := range args[1:] {
			switch a {
			case "--json":
				asJSON = true
			default:
				fmt.Fprintf(stderr, "%s: pkg: unknown flag %q\n", progName, a)
				return 1
			}
		}
		report, err := pkg.Audit(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "%s: pkg: %v\n", progName, err)
			return 1
		}
		if asJSON {
			b, _ := json.Marshal(report)
			fmt.Fprintln(stdout, string(b))
		} else {
			fmt.Fprint(stdout, view.PkgAudit(report))
		}
		if len(report.Findings) > 0 {
			return 1
		}
		return 0

	case "accept":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "%s: pkg: accept requires a root name (applications|opt|flatpak|local-bin)\n", progName)
			return 1
		}
		if err := external.Accept(args[1]); err != nil {
			fmt.Fprintf(stderr, "%s: pkg: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "accepted %s\n", args[1])
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
