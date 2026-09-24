package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"phi/internal/query"
	"phi/internal/view"
)

const queryUsage = `usage: phi query [--prefix <key>] <text>
       phi query record <id> [<result-json>]

Ranks launcher results for <text> across every provider (applications,
open windows, calculator, unit + currency conversion, directory jump via
zoxide, SSH hosts, shell commands, web search, files, system actions,
clipboard history) and prints them, most relevant first. JSON when stdout
is redirected (the shape phi-shell's Launcher parses); a plain list on a
terminal, for testing ranking by hand.

An empty <text> with no --prefix returns every application, ordered by
frecency then title — this is the launcher's own browse-everything list,
not an absence of results; every other provider deliberately answers
nothing for it. --prefix <key> locked with <text> equal to just that
keyword (nothing typed after it yet) similarly returns that category's own
"common usage" defaults instead of nothing: the user's own selection
history for it, then a provider-specific fallback (every application,
recently-used files and common directories, every phi verb, or LibreWolf
bookmarks/history/search terms for web and the per-site keywords below) —
see internal/query/query.go's lockedTagDefaults.

A phi verb typed on its own, without the leading "phi ", is recognised and
run as one ("theme set dark" runs "phi theme set dark") — see the
Commands list in "phi help" for the full verb set.

A leading keyword ("web ", "phi ", "wiki ", "copy "/"clip "/"cp ", ...) in
<text> boosts that category to the top, ranking still applied to
everything else. --prefix <key> additionally restricts the results to
that one category — phi-shell's Launcher sends this once the user has
pressed Tab to "lock" a prefix; <text> is unchanged either way, keyword
included. See internal/query/query.go's prefixProviders for the full
keyword list.

The calculator understands arithmetic ("2+2*3", "sqrt(2)!", "2^10"),
constants and functions, unit conversion in free form ("100km to m",
"-40 C to F", "2 GiB to MB"), percentages ("20% of 150"), equations and
inequalities ("solve x^2-4=0", "x^2-4 < 0"), calculus ("d/dx sin(x)",
"integrate x^2 from 0 to 3") and plots ("plot sin(x)"). Results that need
more than one line carry a "rich" payload (steps, roots, a sampled curve)
the shell renders as a card.

"copy "/"clip "/"cp " search clipboard history phi-shell itself records
(entries and pins under $XDG_STATE_HOME/phi/clipboard/) — pinned first,
then newest first, filtered by the rest of <text>. Selecting one copies
that entry back onto the clipboard; this provider never writes an entry
itself.

phi query record <id> marks a result as used, for frecency ranking on
future queries. The shell calls this once, when the user actually selects
a result — never on every keystroke the way ranking itself runs. Given a
second argument — that Result's own JSON as phi query printed it — the
selection is additionally kept as a snapshot for that id, seeding the
locked-tag "common usage" default described above; a malformed second
argument is ignored and the plain one-argument form still applies. A
clipboard result's snapshot is never stored (its entries are transient
files, not history worth remembering the shape of).

phi query refresh-currency <FROM> <TO> is an internal, hidden sub-verb
(deliberately fails the usual verb-admission test — it is plumbing for
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

	lockedPrefix := ""
	if args[0] == "--prefix" {
		if len(args) < 2 {
			fmt.Fprint(stderr, queryUsage)
			return 1
		}
		lockedPrefix = args[1]
		args = args[2:]
	}

	q := strings.Join(args, " ")

	frecency := loadFrecencyOrNil()
	providers := query.Providers(frecency, phiVerbSet())
	results := query.Run(context.Background(), providers, q, frecency, lockedPrefix)
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
	if len(args) < 1 || len(args) > 2 {
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

	var snapshot *query.Result
	if len(args) == 2 {
		var r query.Result
		if err := json.Unmarshal([]byte(args[1]), &r); err == nil {
			snapshot = &r
		}
		// A malformed second argument still records the plain selection
		// (snapshot stays nil) rather than failing the whole call — the
		// shell's own frecency bump must not break because one snapshot
		// payload was bad.
	}
	if err := f.RecordResult(args[0], snapshot); err != nil {
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
