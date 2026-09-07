package cli

import "os"

// IsTerminal reports whether f is connected to a terminal. Its only use is
// choosing between the styled and structured branches of the output
// contract — never whether to emit colour, which Role A forbids outright
// regardless of what this reports.
//
// This is a coarse, dependency-free check: it also reports true for
// character devices that are not terminals, /dev/null included. Harmless
// today since styled only adds the Φ mark, but it is not sufficient once a
// verb wants to emit colour — that needs a real ioctl-based check (or
// golang.org/x/term) before it ships.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
