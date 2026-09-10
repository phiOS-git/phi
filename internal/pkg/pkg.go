// Package pkg answers "what is installed, in which of the four master plan
// §15/CLAUDE.md categories, and what has an update" (S-45: phi pkg
// list|check, and the Updates settings-panel section). It reuses the same
// pacman queries internal/doctor/packages.go already runs for its own
// "package categories" check, rather than a second implementation of the
// same T0/AUR-T4/phi-packages split — that check only ever needed counts;
// this package needs the actual names too.
package pkg

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"phi/internal/build"
	"phi/internal/tokens"
)

// Category is one of the four groups CLAUDE.md rule 7 and master plan §15
// name: T0 (core/extra, the default), AUR, T4 (manual build), and this
// project's own phi-packages.
type Category string

const (
	CategoryT0          Category = "T0 (core/extra)"
	CategoryAUR         Category = "AUR"
	CategoryT4          Category = "T4 (manual build)"
	CategoryPhiPackages Category = "phi-packages"
)

// Entry is one installed package.
type Entry struct {
	Name     string
	Version  string
	Category Category
	// Update is the available version if pacman -Qu reports one for this
	// package, "" otherwise. Never populated by List — only Check queries
	// this, since it needs a synced pacman database (pacman -Sy, which
	// touches the machine) to mean anything current; List works from the
	// local db alone.
	Update string
}

// phiPackageName matches this project's own naming convention
// (phi-packages/README.md: "phi, phi-<component>") — the identical regex
// internal/doctor/packages.go already uses, not copied independently: a
// second copy that drifted from that one would make doctor's "package
// categories" check and this package disagree about what counts as a phi
// package for no reason.
var phiPackageName = regexp.MustCompile(`^phi(-.+)?$`)

const cmdTimeout = 10 * time.Second

func run(ctx context.Context, name string, args ...string) (output string, available bool, err error) {
	if _, lookErr := exec.LookPath(name); lookErr != nil {
		return "", false, lookErr
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	return strings.TrimSpace(buf.String()), true, err
}

// List reports every explicitly-installed package (pacman -Qe — the same
// "what did I actually ask for" set doctor's own check reads), split into
// T0/AUR/phi-packages. T4 is NEVER populated here and never can be: a T4
// package (master plan §2.2 tiering) is built and installed WITHOUT
// pacman's own database ever recording it (`make install` or equivalent) —
// there is no pacman query that can see software pacman was never told
// about, unlike AUR (which still normally installs as a real, if
// "foreign", pacman package pacman -Qm CAN see). Callers should read
// T4Unverifiable, not an empty T4 slice, as "cannot check" rather than
// "checked, found none".
func List(ctx context.Context) (entries []Entry, t4Unverifiable bool, err error) {
	explicitOut, avail, runErr := run(ctx, "pacman", "-Qe")
	if !avail {
		return nil, true, nil
	}
	if runErr != nil && strings.TrimSpace(explicitOut) == "" {
		return nil, true, runErr
	}

	foreignOut, _, _ := run(ctx, "pacman", "-Qm")
	foreign := make(map[string]bool)
	for _, line := range nonEmptyLines(foreignOut) {
		if f := strings.Fields(line); len(f) > 0 {
			foreign[f[0]] = true
		}
	}

	for _, line := range nonEmptyLines(explicitOut) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, version := fields[0], fields[1]
		category := CategoryT0
		switch {
		case phiPackageName.MatchString(name):
			category = CategoryPhiPackages
		case foreign[name]:
			// Cannot distinguish AUR from a manually-built package pacman
			// -U'd into its own database (both are just "not from a
			// configured sync repo") — grouped as AUR, the more common
			// real case, with T4Unverifiable telling the caller this
			// bucket may be incomplete either way.
			category = CategoryAUR
		}
		entries = append(entries, Entry{Name: name, Version: version, Category: category})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, true, nil
}

// Check runs List, then overlays pacman -Qu's available-update column onto
// each entry. It does NOT run `pacman -Sy` first (that touches the sync
// databases — a real, if read-only-ish, network operation this step's own
// card reserves for `phi update`, not a passive "list categories" verb) —
// so a result here is only as fresh as the last sync `phi update` (or the
// user's own pacman) performed. Reported plainly via Stale below, never
// silently assumed current.
type CheckResult struct {
	Entries        []Entry
	T4Unverifiable bool
	Stale          bool // true when pacman -Qu itself could not run
}

func Check(ctx context.Context) (CheckResult, error) {
	entries, t4, err := List(ctx)
	if err != nil {
		return CheckResult{}, err
	}

	updates := make(map[string]string)
	updOut, avail, runErr := run(ctx, "pacman", "-Qu")
	stale := !avail || (runErr != nil && strings.TrimSpace(updOut) == "")
	if !stale {
		for _, line := range nonEmptyLines(updOut) {
			// "name oldver -> newver" — pacman -Qu's own documented format.
			fields := strings.Fields(line)
			if len(fields) >= 4 && fields[2] == "->" {
				updates[fields[0]] = fields[3]
			}
		}
	}

	for i := range entries {
		if u, ok := updates[entries[i].Name]; ok {
			entries[i].Update = u
		}
	}
	return CheckResult{Entries: entries, T4Unverifiable: t4, Stale: stale}, nil
}

// ============================================================
// settings-overhaul batch J — the Updates settings-panel section wants
// "System state" (component versions) split from "Packages" (one list per
// package manager). Both are read-only; `phi update` stays the only thing
// that changes anything and it runs from a terminal, never from here.
// ============================================================

// Component is one versioned piece of phiOS for the "System state" block.
type Component struct {
	Name    string
	Version string
	Source  string // how the version was read — "build", "pacman -Q", "git describe"
}

// SystemState reports the versions of phiOS (the dotfiles checkout), phi
// (baked in at build), and each installed phi-* package. Every lookup is
// best-effort: a component whose version cannot be read is simply omitted,
// never guessed.
func SystemState(ctx context.Context) []Component {
	var out []Component

	out = append(out, Component{Name: "phi", Version: build.Version, Source: "build"})

	if root, err := tokens.Root(); err == nil {
		if v := gitDescribe(ctx, root); v != "" {
			out = append(out, Component{Name: "phios-dotfiles", Version: v, Source: "git describe"})
		}
	}

	// The installed phi-* packages (phi-shell and any others) — the real
	// "phi-packages" version on this machine is whatever pacman has.
	if entries, _, err := List(ctx); err == nil {
		for _, e := range entries {
			if e.Category == CategoryPhiPackages && e.Name != "phi" {
				out = append(out, Component{Name: e.Name, Version: e.Version, Source: "pacman -Q"})
			}
		}
	}
	return out
}

func gitDescribe(ctx context.Context, dir string) string {
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "-C", dir, "describe", "--tags", "--always", "--dirty")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

// Manager is a package-manager name for `phi pkg list --manager <name>`.
type Manager string

const (
	ManagerPacman   Manager = "pacman"
	ManagerAUR      Manager = "aur"
	ManagerPhi      Manager = "phi"
	ManagerAppImage Manager = "appimage"
	ManagerNPM      Manager = "npm"
	ManagerFlatpak  Manager = "flatpak"
)

// Managers is the fixed order the Updates section renders.
var Managers = []Manager{ManagerPhi, ManagerPacman, ManagerAUR, ManagerNPM, ManagerFlatpak, ManagerAppImage}

// ManagerListing is one manager's slice of `phi pkg list --manager`.
type ManagerListing struct {
	Manager     Manager
	Implemented bool
	Note        string // shown when !Implemented, or as extra context
	Entries     []Entry
}

// ListManager returns the installed packages for one manager. pacman / aur /
// phi are filtered out of the existing pacman-backed List; appimage scans
// ~/Applications; npm and flatpak are explicit "not implemented" markers
// (their real listings are a later pass — the shell renders the Note).
func ListManager(ctx context.Context, m Manager) (ManagerListing, error) {
	switch m {
	case ManagerPacman, ManagerAUR, ManagerPhi:
		entries, _, err := List(ctx)
		if err != nil {
			return ManagerListing{Manager: m}, err
		}
		want := map[Manager]Category{
			ManagerPacman: CategoryT0,
			ManagerAUR:    CategoryAUR,
			ManagerPhi:    CategoryPhiPackages,
		}[m]
		var rows []Entry
		for _, e := range entries {
			if e.Category == want {
				rows = append(rows, e)
			}
		}
		return ManagerListing{Manager: m, Implemented: true, Entries: rows}, nil

	case ManagerAppImage:
		rows, err := appImages()
		return ManagerListing{Manager: m, Implemented: true, Entries: rows, Note: appImagesDir()}, err

	case ManagerNPM:
		return ManagerListing{Manager: m, Implemented: false,
			Note: "not implemented — list with `npm ls -g --depth 0`"}, nil
	case ManagerFlatpak:
		return ManagerListing{Manager: m, Implemented: false,
			Note: "not implemented — list with `flatpak list --app`"}, nil
	}
	return ManagerListing{Manager: m}, nil
}

func appImagesDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/Applications"
	}
	return filepath.Join(home, "Applications")
}

// appImages lists ~/Applications/*.AppImage (case-insensitive), by
// filename — an AppImage carries no queryable version, so Version is "".
func appImages() ([]Entry, error) {
	dir := appImagesDir()
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".appimage") {
			out = append(out, Entry{Name: e.Name(), Category: "AppImage"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
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
