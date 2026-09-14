package query

import (
	"context"
	"strings"
)

// PhiCommandProvider recognises phi's own verbs typed without the leading
// "phi " (docs/TODO.md: "runner bar should read phi commands without
// writing the phi prefix (eg. 'theme set dark' is recognised as 'phi theme
// set dark')"). CommandProvider already runs "phi theme set dark" typed in
// full, by resolving "phi" via exec.LookPath like any other binary on
// PATH — this provider only adds the bare-verb recognition that needs,
// since "theme", "state", "doctor" and so on are not binaries themselves.
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
	// docs/TODO.md's runner-bar prefix feature: typing the literal word
	// "phi" first (as its own prefix, "phi theme set dark") must work
	// exactly like typing the bare verb ("theme set dark") — this provider
	// was the reported bug, since it only ever matched the bare form.
	// CommandProvider still answers the literal "phi ..." text too (it
	// resolves via exec.LookPath like any other binary), at a lower tier;
	// this makes the same input resolve here as well, at the tier meant
	// for it.
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
