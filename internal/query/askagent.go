package query

import (
	"context"
	"strings"
)

// AskAgentProvider is the "ask ai agent" category docs/TODO.md's runner
// category order names (rank.go's own header). Like WebSearchProvider, it
// never answers itself — the A1 service can take real time to respond and
// this provider must stay inside providerTimeout — it only offers to hand
// the literal query to `phi agent ask`, which prints the answer in a
// terminal (§10.2, `phi/internal/cli/agent.go`'s runAgentAsk).
type AskAgentProvider struct{}

func (AskAgentProvider) Name() string { return "agent" }

func (p AskAgentProvider) Query(_ context.Context, q string) []Result {
	// Same two-word floor as WebSearchProvider: a single short token is far
	// more likely an app or a command than a real question, and this sits
	// just above web search in the tier order, so it should not crowd out
	// better matches on every keystroke either.
	if len(strings.Fields(q)) < 2 {
		return nil
	}
	return []Result{{
		ID: "agent:ask:" + q, Provider: p.Name(),
		Title: "Ask AI: \"" + q + "\"", Subtitle: q, Score: 10,
		Action: Action{Kind: ActionExecTerminal, Data: map[string]string{"command": "phi agent ask " + shellQuote(q)}},
	}}
}
