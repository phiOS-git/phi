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
	fields := strings.Fields(q)
	if len(fields) == 0 || len(p.Verbs) == 0 || !p.Verbs[strings.ToLower(fields[0])] {
		return nil
	}
	command := "phi " + q
	return []Result{{
		ID: "phi:" + q, Provider: p.Name(),
		Title: command, Subtitle: "run phi command",
		Score:  90, // trusted as-is, same confidence as CommandProvider's own match
		Action: Action{Kind: ActionExecTerminal, Data: map[string]string{"command": command}},
	}}
}
