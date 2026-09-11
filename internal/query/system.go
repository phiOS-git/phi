package query

import "context"

// SystemActionsProvider offers the launcher's own system actions (S-33
// AGENT: "system actions (lock, suspend, log out)"; docs/TODO.md: "add log
// out, lock, suspend, hibernate, reboot, shutdown commands so that they
// can be quickly referenced in the runner bar"). These are exactly ADR
// 021's own named counter-examples for a phi verb — reboot, shutdown,
// volume, brightness, screenshot never become verbs — so this provider
// hands the shell a plain action name to perform itself (ActionSystem),
// never a command phi runs. Reboot/shutdown were deliberately absent
// until that TODO entry explicitly asked for them; the shell side gates
// both behind an explicit confirm step (Services/PowerActions.qml's
// needsConfirm in phi-shell) before running either — this provider only
// enumerates the action, it does not decide how the shell presents it.
type SystemActionsProvider struct{}

func (SystemActionsProvider) Name() string { return "system" }

var systemActions = []struct{ title, action string }{
	{"Lock", "lock"},
	{"Suspend", "suspend"},
	{"Hibernate", "hibernate"},
	{"Log out", "logout"},
	{"Reboot", "reboot"},
	{"Shut down", "shutdown"},
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
