package query

import (
	"context"
	"os/exec"
	"strings"
)

// CommandProvider implements the app-vs-command heuristic: single token is
// an application (left to ApplicationsProvider), multiple tokens form a
// command. This provider only fires when the first word is a real, resolvable
// command on PATH, so plain English phrases never get offered as shell commands.
type CommandProvider struct{}

func (CommandProvider) Name() string { return "command" }

func (p CommandProvider) Query(_ context.Context, q string) []Result {
	// runner-bar prefix feature: "run <anything>" is an
	// explicit request to run <anything> as a shell command — it bypasses
	// the exec.LookPath gate below, since the whole point of the prefix is
	// to force this provider's answer even when the first word alone can't
	// be confirmed to resolve on PATH (it may need the rest of the line,
	// e.g. a shell builtin or an alias this process doesn't expand).
	if len(q) >= 4 && strings.EqualFold(q[:4], "run ") {
		cmd := strings.TrimSpace(q[4:])
		if cmd == "" {
			return nil
		}
		return []Result{{
			ID: "command:" + cmd, Provider: p.Name(),
			Title: "Run: " + cmd, Subtitle: "shell command", Score: 90,
			Action: Action{Kind: ActionExecTerminal, Data: map[string]string{"command": cmd}},
		}}
	}

	fields := strings.Fields(q)
	if len(fields) < 2 {
		return nil
	}
	if _, err := exec.LookPath(fields[0]); err != nil {
		return nil
	}
	return []Result{{
		ID: "command:" + q, Provider: p.Name(),
		Title: "Run: " + q, Subtitle: "shell command", Score: 90,
		Action: Action{Kind: ActionExecTerminal, Data: map[string]string{"command": q}},
	}}
}
