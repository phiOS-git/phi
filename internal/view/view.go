// Package view renders phi's output as plain strings. It has no knowledge
// of terminals, files, or exit codes — internal/cli decides when a terminal
// is attached and which string to print. Keeping that decision out of this
// package is what lets it survive a rewrite of the CLI layer unchanged.
package view

import "strings"

// mark is the Φ identity glyph (U+03A6, capital), Role A: Tier 0, monochrome,
// static. Its only other permitted appearances are the boot splash, the
// TTY/login and SSH banners, and the "about phiOS" panel (closed list).
const mark = "Φ"

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
	b.WriteString("  help              show this help\n")
	b.WriteString("  completion zsh    print a zsh completion script\n")
	b.WriteString("  theme             render, set, preview, list and check design tokens\n")
	b.WriteString("  --version         print the version\n\n")
	b.WriteString("A command not listed above is looked up as phi-<command> on PATH.\n")

	return b.String()
}

// ZshCompletion renders a zsh completion script for the commands Help lists.
// It does not vary with styled/structured, since it is always redirected
// into zsh's completion system, never read by a person.
func ZshCompletion() string {
	return `#compdef phi

_phi() {
  local -a commands
  commands=(
    'help:show help'
    'completion:print a shell completion script'
    'theme:render, set, preview, list and check design tokens'
  )
  _describe 'command' commands
}

_phi "$@"
`
}
