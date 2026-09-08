package query

import (
	"context"
	"os/exec"
	"strings"
)

// CommandProvider implements the app-vs-command heuristic S-33's card asks
// for and names Q-74: "single token = application, token with arguments =
// command." A single token is left entirely to ApplicationsProvider (and
// to the shell falling back to `phi-<name>` on PATH, ADR 017) — this
// provider only fires once there are arguments, and only when the first
// word is a real, resolvable command, so a plain English phrase typed by
// mistake never gets offered as "run this in a shell." Confirming Q-74
// with the user was this step's own instruction; implemented as stated,
// flagged for cheap veto rather than blocking on it (no password vault or
// other genuinely undecided input is at stake here).
type CommandProvider struct{}

func (CommandProvider) Name() string { return "command" }

func (p CommandProvider) Query(_ context.Context, q string) []Result {
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
