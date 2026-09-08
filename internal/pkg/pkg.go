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
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
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
