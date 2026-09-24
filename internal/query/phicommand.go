package query

import (
	"context"
	"sort"
	"strings"
)

// PhiCommandProvider recognises phi's own verbs typed without the leading
// "phi " — so the runner bar reads a bare "theme set dark" the same as
// "phi theme set dark". CommandProvider already runs "phi theme set dark"
// typed in full, by resolving "phi" via exec.LookPath like any other
// binary on PATH — this provider only adds the bare-verb recognition
// that needs, since "theme", "state", "doctor" and so on are not
// binaries themselves.
//
// Verbs is built by the caller (internal/cli's query verb) from
// view.Commands — the single source Help, ZshCompletion and Man already
// render from — rather than imported here directly: internal/view already
// imports this package (view/query.go renders QueryResults), so this
// package importing internal/view back would be a cycle.
type PhiCommandProvider struct {
	Verbs map[string]bool
}

func (PhiCommandProvider) Name() string { return "phi" }

func (p PhiCommandProvider) Query(_ context.Context, q string) []Result {
	// runner-bar prefix feature: typing the literal word "phi" first (as
	// its own prefix, "phi theme set dark") must work exactly like typing
	// the bare verb ("theme set dark"), so this strips a leading "phi "
	// before checking for a known verb. CommandProvider still answers the
	// literal "phi ..." text too (it resolves via exec.LookPath like any
	// other binary), at a lower tier; this makes the same input resolve
	// here as well, at the tier meant for it.
	rest := q
	if len(q) >= 4 && strings.EqualFold(q[:4], "phi ") {
		rest = strings.TrimSpace(q[4:])
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || len(p.Verbs) == 0 || !p.Verbs[strings.ToLower(fields[0])] {
		return nil
	}
	command := "phi " + rest
	return []Result{{
		ID: "phi:" + rest, Provider: p.Name(),
		Title: command, Subtitle: "run phi command",
		Score:  90, // trusted as-is, same confidence as CommandProvider's own match
		Action: Action{Kind: ActionExecTerminal, Data: map[string]string{"command": command}},
	}}
}

// TagDefaults returns the "phi" tag's default list: every phi verb.
// Query itself only ever recognises one already fully typed out —
// the "phi" tag locked with nothing typed yet has no bare verb for it to
// recognise, so this lists the whole set instead, alphabetized for a
// stable, scannable order.
func (p PhiCommandProvider) TagDefaults(_ context.Context, _ string) []Result {
	verbs := make([]string, 0, len(p.Verbs))
	for v := range p.Verbs {
		verbs = append(verbs, v)
	}
	sort.Strings(verbs)

	results := make([]Result, 0, len(verbs))
	for _, v := range verbs {
		command := "phi " + v
		results = append(results, Result{
			ID: "phi:" + v, Provider: p.Name(),
			Title: command, Subtitle: "run phi command",
			Action: Action{Kind: ActionExecTerminal, Data: map[string]string{"command": command}},
		})
	}
	return results
}
