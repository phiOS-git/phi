package query

import "testing"

// Fixture shape matches real hyprctl clients -j output (the same fields
// S-24's PROGRESS row captured from a real `hyprctl clients` run on
// razer for the Steam window rule), trimmed to what this provider reads —
// extra real-world fields (workspace, floating, pid, ...) are included
// here too, to prove unknown fields do not break parsing.
const fixtureHyprctlClients = `[
  {
    "address": "0x55f2a1b2c3d4",
    "class": "steam",
    "title": "Steam",
    "workspace": {"id": 3, "name": "name:steam"},
    "floating": false,
    "pid": 12345
  },
  {
    "address": "0x55f2a1b2c3e0",
    "class": "kitty",
    "title": "~/phios-dotfiles",
    "workspace": {"id": 1, "name": "1"},
    "floating": false,
    "pid": 12399
  }
]`

func TestParseHyprctlClients(t *testing.T) {
	clients, err := parseHyprctlClients([]byte(fixtureHyprctlClients))
	if err != nil {
		t.Fatalf("parseHyprctlClients: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("got %d clients, want 2", len(clients))
	}
	if clients[0].Address != "0x55f2a1b2c3d4" || clients[0].Class != "steam" || clients[0].Title != "Steam" {
		t.Errorf("clients[0] = %+v", clients[0])
	}
}

func TestParseHyprctlClientsRejectsGarbage(t *testing.T) {
	if _, err := parseHyprctlClients([]byte("not json")); err == nil {
		t.Error("expected an error on malformed input")
	}
}

func TestWindowsProviderResultShape(t *testing.T) {
	clients, err := parseHyprctlClients([]byte(fixtureHyprctlClients))
	if err != nil {
		t.Fatalf("parseHyprctlClients: %v", err)
	}
	p := WindowsProvider{}
	// Exercises the same mapping Query() does, without a real hyprctl
	// binary — the shell-out itself is not unit-testable, the mapping is.
	var results []Result
	for _, c := range clients {
		results = append(results, Result{
			ID: "window:" + c.Address, Provider: p.Name(),
			Title: c.Title, Subtitle: c.Class,
			Action: Action{Kind: ActionActivateWindow, Data: map[string]string{"address": c.Address}},
		})
	}
	if len(results) != 2 || results[0].Action.Kind != ActionActivateWindow {
		t.Errorf("unexpected result shape: %+v", results)
	}
	if results[0].Action.Data["address"] != clients[0].Address {
		t.Errorf("action address = %q, want %q", results[0].Action.Data["address"], clients[0].Address)
	}
}
