// Package pkg lists installed packages by category and checks for updates
// using pacman queries shared with internal/doctor.
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

// Category is one of four package groups: T0, AUR, T4, phi-packages.
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
	// Update: available version if any (only Check queries this).
	Update string
}

// phiPackageName: regex for "phi" and "phi-<component>" (shared with doctor).
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

// List reports explicitly-installed packages split by category (T4 unverifiable).
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

// Check runs List, overlays pacman -Qu updates (does NOT sync databases).
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

// System state (component versions) split from Packages (by manager) — both read-only.

// Component is a versioned part of phiOS for system state reporting.
type Component struct {
	Name    string
	Version string
	Source  string // how the version was read — "build", "pacman -Q", "git describe"
}

// SystemState reports phiOS component versions (best-effort lookup).
func SystemState(ctx context.Context) []Component {
	var out []Component

	out = append(out, Component{Name: "phi", Version: build.Version, Source: "build"})

	if root, err := tokens.Root(); err == nil {
		if v := gitDescribe(ctx, root); v != "" {
			out = append(out, Component{Name: "phios-dotfiles", Version: v, Source: "git describe"})
		}
	}

	// Installed phi-* packages (as reported by pacman).
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

// Manager is a package manager name.
type Manager string

const (
	ManagerPacman   Manager = "pacman"
	ManagerAUR      Manager = "aur"
	ManagerPhi      Manager = "phi"
	ManagerAppImage Manager = "appimage"
	ManagerNPM      Manager = "npm"
	ManagerFlatpak  Manager = "flatpak"
)

// Managers lists managers in render order.
var Managers = []Manager{ManagerPhi, ManagerPacman, ManagerAUR, ManagerNPM, ManagerFlatpak, ManagerAppImage}

// ManagerListing is one manager's package listing.
type ManagerListing struct {
	Manager     Manager
	Implemented bool
	Note        string // shown when !Implemented, or as extra context
	Entries     []Entry
}

// ListManager returns installed packages for a given manager.
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
