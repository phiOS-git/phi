package cli

import (
	"fmt"
	"io"

	"phi/internal/state"
	"phi/internal/view"
)

const stateUsage = `usage: phi state <verb> [arguments]

Verbs:
  get KEY          print the value of KEY, or fail if it was never set
  set KEY VALUE    write VALUE for KEY
  list             list every defined key and its value (unset keys show as such)

KEY is one of the runtime-state keys master plan §5.6 defines. This is a
closed set: an unlisted key is rejected, not created. State lives at
$XDG_STATE_HOME/phi and is never written into any repository.
`

func runState(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, stateUsage)
		return 1
	}

	switch args[0] {
	case "get":
		return runStateGet(args[1:], stdout, stderr)
	case "set":
		return runStateSet(args[1:], stdout, stderr)
	case "list":
		return runStateList(stdout, stderr)
	case "-h", "--help":
		fmt.Fprint(stdout, stateUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: state: unknown verb %q\n", progName, args[0])
		fmt.Fprint(stderr, stateUsage)
		return 1
	}
}

func runStateGet(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, stateUsage)
		return 1
	}
	value, ok, err := state.Get(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "%s: state: %v\n", progName, err)
		return 1
	}
	if !ok {
		fmt.Fprintf(stderr, "%s: state: %s is not set\n", progName, args[0])
		return 1
	}
	fmt.Fprintln(stdout, value)
	return 0
}

func runStateSet(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprint(stderr, stateUsage)
		return 1
	}
	if err := state.Set(args[0], args[1]); err != nil {
		fmt.Fprintf(stderr, "%s: state: %v\n", progName, err)
		return 1
	}
	return 0
}

func runStateList(stdout, stderr io.Writer) int {
	entries, err := state.List()
	if err != nil {
		fmt.Fprintf(stderr, "%s: state: %v\n", progName, err)
		return 1
	}
	fmt.Fprint(stdout, view.StateList(entries))
	return 0
}
