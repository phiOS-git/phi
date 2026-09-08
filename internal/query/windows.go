package query

import (
	"context"
	"encoding/json"
)

// WindowsProvider lists open windows via `hyprctl clients -j` — "switch to
// an open window" is named in S-33's own card as likely the most frequent
// launcher action on a tiling compositor. Shells out directly rather than
// reading phi-shell's own live ToplevelManager state; see query.go's
// package comment for why.
type WindowsProvider struct{}

func (WindowsProvider) Name() string { return "window" }

func (p WindowsProvider) Query(ctx context.Context, q string) []Result {
	if q == "" {
		return nil
	}
	out, available := runCommand(ctx, "hyprctl", "clients", "-j")
	if !available || out == "" {
		return nil
	}
	clients, err := parseHyprctlClients([]byte(out))
	if err != nil {
		return nil
	}
	var results []Result
	for _, c := range clients {
		if c.Address == "" || c.Title == "" {
			continue
		}
		results = append(results, Result{
			ID: "window:" + c.Address, Provider: p.Name(),
			Title: c.Title, Subtitle: c.Class,
			Action: Action{Kind: ActionActivateWindow, Data: map[string]string{"address": c.Address}},
		})
	}
	return results
}

// hyprctlClient is only the fields this provider needs — `hyprctl clients
// -j` reports many more (workspace, floating, pid, size, ...), all
// ignored here by encoding/json's own default behaviour of skipping
// unknown fields rather than erroring on them.
type hyprctlClient struct {
	Address string `json:"address"`
	Class   string `json:"class"`
	Title   string `json:"title"`
}

func parseHyprctlClients(data []byte) ([]hyprctlClient, error) {
	var clients []hyprctlClient
	if err := json.Unmarshal(data, &clients); err != nil {
		return nil, err
	}
	return clients, nil
}
