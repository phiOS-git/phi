// Package cli parses arguments and dispatches to a verb. A verb it does not
// know is looked up as phi-<verb> on PATH and run in place (ADR 017): the
// core stays a monolith, the edges stay extensible.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"phi/internal/build"
	"phi/internal/view"
)

const progName = "phi"

// Run parses args (os.Args[1:]) and executes the matching verb, writing to
// stdout/stderr, and returns the process exit code. styled is whether stdout
// is a terminal; callers pass it in rather than Run inspecting os.Stdout
// itself, so this package never touches a process global — main.go is the
// only place that does, which is what keeps the view layer swappable.
func Run(args []string, stdout, stderr io.Writer, styled bool) int {
	if len(args) == 0 {
		// Error output is always structured, regardless of styled: it may
		// still reach a terminal, but a usage error is not the styled path.
		fmt.Fprint(stderr, view.Help(false, build.Version))
		return 1
	}

	switch args[0] {
	case "--version":
		fmt.Fprintln(stdout, view.Version(build.Version))
		return 0

	case "help":
		fmt.Fprint(stdout, view.Help(styled, build.Version))
		return 0

	case "completion":
		if len(args) < 2 || args[1] != "zsh" {
			fmt.Fprintf(stderr, "%s: completion requires a shell name (zsh)\n", progName)
			return 1
		}
		fmt.Fprint(stdout, view.ZshCompletion())
		return 0

	case "man":
		fmt.Fprint(stdout, view.Man(build.Version))
		return 0

	case "theme":
		return runTheme(args[1:], stdout, stderr)

	case "wallpaper":
		return runWallpaper(args[1:], stdout, stderr)

	case "vpn":
		return runVpn(args[1:], stdout, stderr)

	case "state":
		return runState(args[1:], stdout, stderr)

	case "doctor":
		return runDoctor(args[1:], stdout, stderr)

	case "query":
		return runQuery(args[1:], stdout, stderr, styled)

	case "pkg":
		return runPkg(args[1:], stdout, stderr)

	case "update":
		return runUpdate(args[1:], stdout, stderr)

	case "agent":
		return runAgent(args[1:], stdout, stderr, styled)

	default:
		return runFallback(args, stdout, stderr)
	}
}

// runFallback looks up phi-<verb> on PATH and runs it with the remaining
// arguments, forwarding stdin and the exit code.
func runFallback(args []string, stdout, stderr io.Writer) int {
	verb := args[0]
	name := progName + "-" + verb

	path, err := exec.LookPath(name)
	if err != nil {
		fmt.Fprintf(stderr, "%s: unknown command %q\n", progName, verb)
		fmt.Fprintf(stderr, "Run '%s help' for a list of commands.\n", progName)
		return 1
	}

	cmd := exec.Command(path, args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "%s: %v\n", progName, err)
		return 1
	}
	return 0
}
