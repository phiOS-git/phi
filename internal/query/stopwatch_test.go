package query

import (
	"context"
	"strings"
	"testing"
)

func TestStopwatchProviderQuery(t *testing.T) {
	p := StopwatchProvider{}

	if got := p.Query(context.Background(), "hello"); got != nil {
		t.Errorf("unrelated query must return no result, got %+v", got)
	}
	if got := p.Query(context.Background(), "stopwatcher"); got != nil {
		t.Errorf("'stopwatcher' must not match on a bare prefix, got %+v", got)
	}
	if got := p.Query(context.Background(), "stopwatch nonsense"); got != nil {
		t.Errorf("an unrecognised verb must return no result, got %+v", got)
	}

	cases := []struct {
		q    string
		verb string
	}{
		{"stopwatch", "toggle"},
		{"Stopwatch", "toggle"},
		{"stopwatch start", "start"},
		{"stopwatch resume", "start"},
		{"stopwatch pause", "pause"},
		{"stopwatch stop", "pause"},
		{"stopwatch reset", "reset"},
		{"stopwatch lap", "lap"},
	}
	for _, c := range cases {
		got := p.Query(context.Background(), c.q)
		if len(got) != 1 {
			t.Fatalf("Query(%q): expected exactly one result, got %d", c.q, len(got))
		}
		if got[0].Score <= 0 {
			t.Errorf("Query(%q): Score must be explicitly positive or Rank() silently drops this result, got %v", c.q, got[0].Score)
		}
		if got[0].Action.Kind != ActionExec {
			t.Errorf("Query(%q): Action.Kind = %q, want %q", c.q, got[0].Action.Kind, ActionExec)
		}
		cmd := got[0].Action.Data["command"]
		want := "ipc call stopwatch " + c.verb
		if !strings.Contains(cmd, want) {
			t.Errorf("Query(%q): command = %q, want it to contain %q", c.q, cmd, want)
		}
	}
}

// TestStopwatchProviderSurvivesRanking mirrors
// TestTimerProviderSurvivesRanking: a provider returning a Result is not
// enough on its own — Rank() independently drops any result whose Score is
// left at zero and whose Title does not fuzzy-match the raw query text,
// which a generated title like "Start or pause the stopwatch" never would
// against a bare "stopwatch". Only Rank(), not the provider alone, proves
// the result actually reaches the launcher.
func TestStopwatchProviderSurvivesRanking(t *testing.T) {
	p := StopwatchProvider{}
	results := p.Query(context.Background(), "stopwatch")
	ranked := Rank(results, "stopwatch", nil)
	if len(ranked) != 1 {
		t.Fatalf("Rank() dropped the stopwatch result: got %d results, want 1", len(ranked))
	}
}
