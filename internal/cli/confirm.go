package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// confirmYesNo prints prompt (no trailing newline — the answer is typed on
// the same line) then reads one line from in, defaulting to "no" on
// anything but an explicit y/yes: empty input, EOF, or unrecognised text.
// This is phi's one interactive confirmation path; a future verb that also
// needs to ask reuses this rather than inventing its own prompt style.
func confirmYesNo(out io.Writer, in io.Reader, prompt string) bool {
	fmt.Fprint(out, prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
