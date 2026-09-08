// Package view renders phi's output as plain strings. It has no knowledge
// of terminals, files, or exit codes — internal/cli decides when a terminal
// is attached and which string to print. Keeping that decision out of this
// package is what lets it survive a rewrite of the CLI layer unchanged.
package view

import (
	"fmt"
	"strings"
)

// mark is the Φ identity glyph (U+03A6, capital), Role A: Tier 0, monochrome,
// static. Its only other permitted appearances are the boot splash, the
// TTY/login and SSH banners, and the "about phiOS" panel (closed list).
const mark = "Φ"

// Command is one top-level verb internal/cli.Run dispatches directly.
// Commands is the single source Help, ZshCompletion, and Man (S-15) all
// render from — before S-15, every verb added after S-10 meant hand-editing
// Help's table and ZshCompletion's array separately (S-12's theme, S-13's
// state, S-14's doctor each did); a new verb now means adding one row here.
type Command struct {
	Name    string
	Summary string // one line: the help table, the man page's COMMANDS section, and zsh's completion description all use it verbatim
}

// Commands excludes --version on purpose: it is a flag, not a verb
// internal/cli.Run dispatches to by name, so Help still lists it by hand.
var Commands = []Command{
	{"help", "show this help"},
	{"completion", "print a zsh completion script"},
	{"man", "print a generated man page"},
	{"theme", "render, set, preview, list and check design tokens"},
	{"state", "get, set and list runtime state ($XDG_STATE_HOME/phi)"},
	{"doctor", "disk, systemd, dotfiles drift, SMART, services, packages"},
	{"query", "rank launcher results (applications, windows, calculator, ...)"},
}

// Version renders the --version output. It is identical whether or not
// stdout is a terminal, so scripts can parse it unconditionally.
func Version(version string) string {
	return "phi " + version
}

// Help renders the help text. styled selects the Role A mark for a terminal;
// redirected output drops it, per the styled/structured output contract —
// the mark is decoration, not information, so structured output has no need
// of it.
func Help(styled bool, version string) string {
	var b strings.Builder

	if styled {
		b.WriteString(mark + " ")
	}
	b.WriteString("phi " + version + "\n\n")
	b.WriteString("The unified phiOS command-line interface.\n\n")
	b.WriteString("Usage:\n")
	b.WriteString("  phi <command> [arguments]\n\n")
	b.WriteString("Commands:\n")

	const versionName = "--version"
	width := len(versionName)
	for _, c := range Commands {
		if len(c.Name) > width {
			width = len(c.Name)
		}
	}
	for _, c := range Commands {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, c.Name, c.Summary)
	}
	fmt.Fprintf(&b, "  %-*s  %s\n\n", width, versionName, "print the version")
	b.WriteString("A command not listed above is looked up as phi-<command> on PATH.\n")

	return b.String()
}

// ZshCompletion renders a zsh completion script for the commands Help lists.
// It does not vary with styled/structured, since it is always redirected
// into zsh's completion system, never read by a person.
func ZshCompletion() string {
	var b strings.Builder
	b.WriteString("#compdef phi\n\n_phi() {\n  local -a commands\n  commands=(\n")
	for _, c := range Commands {
		fmt.Fprintf(&b, "    '%s:%s'\n", c.Name, c.Summary)
	}
	b.WriteString("  )\n  _describe 'command' commands\n}\n\n_phi \"$@\"\n")
	return b.String()
}
