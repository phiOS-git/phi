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
open windows, calculator, unit + currency conversion, directory jump via
zoxide, SSH hosts, shell commands, web search, files, system actions) and
prints them, most relevant first. JSON when stdout is redirected (the
shape phi-shell's Launcher parses); a plain list on a terminal, for
testing ranking by hand.

A phi verb typed on its own, without the leading "phi ", is recognised and
run as one ("theme set dark" runs "phi theme set dark") — see the
Commands list in "phi help" for the full verb set.

The calculator understands arithmetic ("2+2*3", "sqrt(2)!", "2^10"),
constants and functions, unit conversion in free form ("100km to m",
"-40 C to F", "2 GiB to MB"), percentages ("20% of 150"), equations and
inequalities ("solve x^2-4=0", "x^2-4 < 0"), calculus ("d/dx sin(x)",
"integrate x^2 from 0 to 3") and plots ("plot sin(x)"). Results that need
more than one line carry a "rich" payload (steps, roots, a sampled curve)
the shell renders as a card.

phi query record <id> marks a result as used, for frecency ranking on
future queries. The shell calls this once, when the user actually selects
a result — never on every keystroke the way ranking itself runs.

phi query refresh-currency <FROM> <TO> is an internal, hidden sub-verb
(fails ADR 021's own admission test on purpose — it is plumbing for
CurrencyProvider, not a user-facing verb): fetches one exchange rate
synchronously and writes it to the on-disk cache. CurrencyProvider's own
Query spawns this as a detached child process rather than calling it
in-process, since phi query itself is too short-lived to ever finish an
HTTP round trip before exiting (currency.go's own header has the story).
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
	if args[0] == "refresh-currency" {
		return runQueryRefreshCurrency(args[1:], stderr)
	}

	q := strings.Join(args, " ")

	frecency := loadFrecencyOrNil()
	providers := query.Providers(frecency, phiVerbSet())
	results := query.Run(context.Background(), providers, q, frecency)
	fmt.Fprint(stdout, view.QueryResults(results, styled))
	return 0
}

// phiVerbSet reduces view.Commands — the single source Help, ZshCompletion
// and Man already render from — to the lookup set query.PhiCommandProvider
// needs, so a verb added there is recognised in the launcher with nothing
// else to keep in sync.
func phiVerbSet() map[string]bool {
	set := make(map[string]bool, len(view.Commands))
	for _, c := range view.Commands {
		set[c.Name] = true
	}
	return set
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

// runQueryRefreshCurrency is the detached child spawnCurrencyRefresh
// (currency.go) starts and never waits on — this is its entire job, run
// synchronously with the real 5s HTTP deadline query.RefreshCurrencyCache
// itself owns, then exit. Silent on every failure (bad args, network
// down, a malformed response): there is no terminal for this process to
// report to, and the NEXT live query's own CurrencyProvider.Query will
// just try spawning another refresh, exactly as if this one had never
// run.
func runQueryRefreshCurrency(args []string, stderr io.Writer) int {
	if len(args) != 2 {
		return 1
	}
	query.RefreshCurrencyCache(args[0], args[1])
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
