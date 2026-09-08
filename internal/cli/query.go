package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"phi/internal/query"
	"phi/internal/view"
)

const queryUsage = `usage: phi query <text>
       phi query record <id>

Ranks launcher results for <text> across every provider (applications,
open windows, calculator, currency conversion, directory jump via zoxide,
SSH hosts, shell commands, web search, files, system actions) and prints
them, most relevant first. JSON when stdout is redirected (the shape
phi-shell's Launcher parses); a plain list on a terminal, for testing
ranking by hand.

phi query record <id> marks a result as used, for frecency ranking on
future queries. The shell calls this once, when the user actually selects
a result — never on every keystroke the way ranking itself runs.
`

func runQuery(args []string, stdout, stderr io.Writer, styled bool) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, queryUsage)
		return 1
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, queryUsage)
		return 0
	}
	if args[0] == "record" {
		return runQueryRecord(args[1:], stdout, stderr)
	}

	q := strings.Join(args, " ")

	frecency := loadFrecencyOrNil()
	results := query.Run(context.Background(), query.Providers(frecency), q, frecency)
	fmt.Fprint(stdout, view.QueryResults(results, styled))
	return 0
}

func runQueryRecord(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, queryUsage)
		return 1
	}
	path, err := query.FrecencyPath()
	if err != nil {
		fmt.Fprintf(stderr, "%s: query: %v\n", progName, err)
		return 1
	}
	f, err := query.LoadFrecency(path)
	if err != nil {
		fmt.Fprintf(stderr, "%s: query: %v\n", progName, err)
		return 1
	}
	if err := f.Record(args[0]); err != nil {
		fmt.Fprintf(stderr, "%s: query: %v\n", progName, err)
		return 1
	}
	return 0
}

// loadFrecencyOrNil never turns a frecency problem into a query failure:
// ranking without frecency (query.Run tolerates a nil *query.Frecency,
// simply skipping that term) is a strictly better outcome for a launcher
// than refusing to answer at all because its history file could not be
// read.
func loadFrecencyOrNil() *query.Frecency {
	path, err := query.FrecencyPath()
	if err != nil {
		return nil
	}
	f, err := query.LoadFrecency(path)
	if err != nil {
		return nil
	}
	return f
}
