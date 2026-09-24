package view

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"phi/internal/query"
)

// QueryResults renders `phi query`'s ranked results. JSON when redirected
// — phi-shell's Launcher is the one real consumer. A plain list on a
// terminal for manual testing of the ranking.
func QueryResults(results []query.Result, styled bool) string {
	if !styled {
		// A nil results (the zero value of "no matches", as opposed to an
		// empty-but-non-nil slice) marshals to the JSON literal "null", not
		// "[]" — the shell's Launcher only ever accepts an Array.isArray
		// payload and otherwise leaves whatever it last rendered on screen
		// (e.g. a locked "web" tag with the input cleared keeps showing the
		// previous query's result), so an empty list must still be a JSON
		// array here regardless of which of the two it started as.
		if len(results) == 0 {
			return "[]\n"
		}
		data, err := json.Marshal(results)
		if err != nil {
			// A Result only ever holds strings, a float64 and a
			// map[string]string — every one of those always marshals; this
			// is here so the function still has a defined, sane behaviour
			// if that ever stops being true, not because it is expected.
			return "[]\n"
		}
		return string(data) + "\n"
	}

	if len(results) == 0 {
		return "(no results)\n"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "PROVIDER\tTITLE\tSUBTITLE\tSCORE\n")
	for _, r := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%.1f\n", r.Provider, r.Title, r.Subtitle, r.Score)
	}
	tw.Flush()

	// A calculator/converter result carries a rich card; dump it below the
	// table so `phi query` in a terminal shows the steps, roots and plot
	// range the shell would render graphically.
	for _, r := range results {
		if r.Rich == nil {
			continue
		}
		rc := r.Rich
		fmt.Fprintf(&b, "\n  %s", rc.Headline)
		if rc.Detail != "" {
			fmt.Fprintf(&b, "   (%s)", rc.Detail)
		}
		b.WriteByte('\n')
		for _, s := range rc.Steps {
			fmt.Fprintf(&b, "    · %s\n", s)
		}
		if len(rc.Solutions) > 0 {
			fmt.Fprintf(&b, "    solutions: %s\n", strings.Join(rc.Solutions, ", "))
		}
		for _, kv := range rc.Table {
			fmt.Fprintf(&b, "    %-10s %s\n", kv.Label, kv.Value)
		}
		if rc.Plot != nil {
			fmt.Fprintf(&b, "    plot %s over %s∈[%.3g, %.3g], y∈[%.3g, %.3g], %d samples\n",
				rc.Plot.Expr, rc.Plot.Var, rc.Plot.XMin, rc.Plot.XMax, rc.Plot.YMin, rc.Plot.YMax, len(rc.Plot.Points))
		}
	}
	return b.String()
}
