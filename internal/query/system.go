package query

import "context"

// SystemActionsProvider offers the launcher's own system actions (S-33
// AGENT: "system actions (lock, suspend, log out)"). These are exactly
// ADR 021's own named counter-examples for a phi verb — reboot, shutdown,
// volume, brightness, screenshot never become verbs — so this provider
// hands the shell a plain action name to perform itself (ActionSystem),
// never a command phi runs. Reboot/shutdown are deliberately absent from
// the list below: only S-33's own three are named, and a destructive
// system-wide action is not this agent's call to add un-asked.
type SystemActionsProvider struct{}

func (SystemActionsProvider) Name() string { return "system" }

var systemActions = []struct{ title, action string }{
	{"Lock", "lock"},
	{"Suspend", "suspend"},
	{"Log out", "logout"},
}

func (p SystemActionsProvider) Query(_ context.Context, q string) []Result {
	if q == "" {
		return nil
	}
	var results []Result
	for _, a := range systemActions {
		results = append(results, Result{
			ID: "system:" + a.action, Provider: p.Name(),
			Title: a.title, Subtitle: "System action",
			Action: Action{Kind: ActionSystem, Data: map[string]string{"action": a.action}},
		})
	}
	return results
}
