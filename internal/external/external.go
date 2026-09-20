// Package external parses, audits and reports on phiOS's "monitored
// installation" declarations: software that is not T0 (official Arch) or T1
// (the private [phi] pacman repository), recorded one per line in each
// profile's external.txt so that nothing can exist on a machine that nobody
// declared. See the workspace AGENTS.md's tier ladder (rule 1) for the
// policy this package implements.
//
// This package must never import phi/internal/pkg: pkg composes external
// alongside pacman as one of several package managers, so the dependency
// runs from pkg to external, never the other way.
package external

import "context"

// Entry is one declared line from a profile's external.txt.
type Entry struct {
	Name, Tier, Source, Ref, SHA256, Reason string
	Profile                                 string // the profile whose external.txt declared this entry (the winning one, after override)
	Status                                  string // "ok" | "missing" | "changed" | "unverified" — set by Audit; zero value ("") before that
}

// Problem is one external.txt line that failed validation. It is kept
// separate from Finding because it describes a malformed declaration, not a
// fact about the machine's actual state.
type Problem struct {
	File   string
	Line   int
	Detail string
}

// Finding is one thing Audit noticed that a clean run would not have.
type Finding struct {
	Check  string // "drift" | "leak" | "integrity"
	Kind   string // "undeclared" | "missing" | "leak" | "checksum" | "changed"
	Name   string
	Detail string
}

// RootState is one fingerprinted root's last-known and current shape.
type RootState struct {
	Root    string
	Digest  string
	Entries int
	Changed bool
}

// Report is one Audit run.
type Report struct {
	Entries  []Entry
	Problems []Problem
	Findings []Finding
	Roots    []RootState
}

// Config is Audit's entire universe: no host detection or environment
// sniffing happens inside Audit itself, so every fact it needs to reach a
// verdict is a field here.
type Config struct {
	Root     string     // the phios-dotfiles checkout
	Profiles []string   // the host's active profiles, in declaration order
	Home     string     // the user's home directory; defaults to os.UserHomeDir() when empty
	Enum     Enumerator // injected; nil means the real system (systemEnumerator)
}

// Enumerator reports actual machine state for the populations external.txt
// can declare. Injected so Audit is testable on a machine that has none of
// pacman, flatpak, podman or a real ~/Applications — all four are absent on
// the darwin build host this package is developed on.
type Enumerator interface {
	Flatpak(ctx context.Context) ([]string, error)    // installed Flatpak app ids
	AppImages() ([]string, error)                     // file names in ~/Applications
	Opt() ([]string, error)                           // directory names in ~/.local/opt
	Containers(ctx context.Context) ([]string, error) // rootless container names; empty when podman is absent
	ListDir(path string) ([]string, error)            // entry names in path, for the leak checks; a missing path is an empty, non-error result
	Manifest() ([]string, error)                      // targets recorded in the phios installer manifest, paths relative to $HOME
}
