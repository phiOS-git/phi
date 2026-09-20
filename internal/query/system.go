package query

import "context"

// SystemActionsProvider offers the launcher's system actions: lock, suspend,
// log out, hibernate, reboot, shutdown. These never become phi verbs — the
// provider hands the shell a plain action name to perform itself (ActionSystem),
// never a command phi runs. The shell gates reboot/shutdown behind an explicit
// confirm step before running them.
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
