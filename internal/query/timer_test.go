package query

import (
	"context"
	"strings"
	"testing"
)

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in       string
		seconds  int
		consumed int
		ok       bool
	}{
		{"5m", 300, 2, true},
		{"5m tea", 300, 2, true},
		{"1h30m", 5400, 5, true},
		{"90s", 90, 3, true},
		{"2h", 7200, 2, true},
		{"", 0, 0, false},
		{"tea", 0, 0, false},
		{"5", 0, 0, false},  // no unit — must not guess one
		{"0m", 0, 0, false}, // zero-length duration rejected
	}
	for _, c := range cases {
		seconds, consumed, ok := parseDuration(c.in)
		if ok != c.ok {
			t.Errorf("parseDuration(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && (seconds != c.seconds || consumed != c.consumed) {
			t.Errorf("parseDuration(%q) = (%d, %d), want (%d, %d)", c.in, seconds, consumed, c.seconds, c.consumed)
		}
	}
}

func TestParseClockTime(t *testing.T) {
	cases := []struct {
		in           string
		hour, minute int
		consumed     int
		ok           bool
	}{
		{"7:30", 7, 30, 4, true},
		{"19:45 wake up", 19, 45, 5, true},
		{"23:59", 23, 59, 5, true},
		{"24:00", 0, 0, 0, false}, // hour out of range
		{"7:60", 0, 0, 0, false},  // minute out of range
		{"7:305", 0, 0, 0, false}, // not a clean HH:MM boundary
		{"tea", 0, 0, 0, false},
	}
	for _, c := range cases {
		hour, minute, consumed, ok := parseClockTime(c.in)
		if ok != c.ok {
			t.Errorf("parseClockTime(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && (hour != c.hour || minute != c.minute || consumed != c.consumed) {
			t.Errorf("parseClockTime(%q) = (%d, %d, %d), want (%d, %d, %d)", c.in, hour, minute, consumed, c.hour, c.minute, c.consumed)
		}
	}
}

func TestTimerProviderQuery(t *testing.T) {
	p := TimerProvider{}

	if got := p.Query(context.Background(), "timer"); got != nil {
		t.Errorf("bare 'timer' with no duration must return no result, got %+v", got)
	}
	if got := p.Query(context.Background(), "timer tea"); got != nil {
		t.Errorf("'timer' with no unit must return no result, got %+v", got)
	}
	if got := p.Query(context.Background(), "hello"); got != nil {
		t.Errorf("unrelated query must return no result, got %+v", got)
	}

	got := p.Query(context.Background(), "timer 5m tea")
	if len(got) != 1 {
		t.Fatalf("expected exactly one result, got %d", len(got))
	}
	if got[0].Score <= 0 {
		// A zero Score falls through to Rank's own title-vs-query fuzzy
		// match (rank.go), which a generated title like "Set a timer for
		// 5m" does not pass against a raw query like "timer 5m tea" —
		// Rank then excludes the result entirely, not just ranks it low.
		t.Errorf("Score must be explicitly positive or Rank() silently drops this result, got %v", got[0].Score)
	}
	if got[0].Action.Kind != ActionExec {
		t.Errorf("Action.Kind = %q, want %q", got[0].Action.Kind, ActionExec)
	}
	cmd := got[0].Action.Data["command"]
	if !strings.Contains(cmd, "ipc call timer add 300 'tea'") {
		t.Errorf("command = %q, want it to contain the ipc call with seconds and quoted label", cmd)
	}

	got = p.Query(context.Background(), "alarm 7:30 wake up")
	if len(got) != 1 {
		t.Fatalf("expected exactly one result, got %d", len(got))
	}
	cmd = got[0].Action.Data["command"]
	if !strings.Contains(cmd, "ipc call timer addAlarm 7 30 'wake up'") {
		t.Errorf("command = %q, want it to contain the ipc call with hour, minute and quoted label", cmd)
	}
}

// TestTimerProviderSurvivesRanking is the end-to-end check for the exact
// bug a manual `phi query "timer 5m tea"` run first caught: the provider's
// own Query returning a result is not enough — Rank() independently drops
// any result whose Score is left at zero and whose Title does not fuzzy-
// match the raw query text, which a generated title like "Set a timer for
// 5m" never will against "timer 5m tea". Only Rank(), not TimerProvider
// alone, can prove the result actually reaches the launcher.
func TestTimerProviderSurvivesRanking(t *testing.T) {
	p := TimerProvider{}
	results := p.Query(context.Background(), "timer 5m tea")
	ranked := Rank(results, "timer 5m tea", nil)
	if len(ranked) != 1 {
		t.Fatalf("Rank() dropped the timer result: got %d results, want 1", len(ranked))
	}
}

func TestTimerProviderQuoteInjection(t *testing.T) {
	p := TimerProvider{}
	got := p.Query(context.Background(), "timer 5m tea'; rm -rf ~")
	if len(got) != 1 {
		t.Fatalf("expected exactly one result, got %d", len(got))
	}
	cmd := got[0].Action.Data["command"]
	// The label must be one single-quoted POSIX sh word with the embedded
	// single quote escaped as '\'' (close, escaped literal quote, reopen)
	// — Data["command"] runs through `sh -c` (Launcher.qml), so an
	// unescaped label would let a typed "'; rm -rf ~" break out of the
	// quoting and run as a second command. The substring "; rm -rf"
	// legitimately still appears in the output — safely, inside quotes —
	// so the real check is the exact quoted form, not its absence.
	if !strings.HasSuffix(cmd, `'tea'\''; rm -rf ~'`) {
		t.Errorf("label was not shell-quoted correctly, command = %q", cmd)
	}
}
