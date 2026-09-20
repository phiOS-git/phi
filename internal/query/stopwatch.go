package query

import (
	"context"
	"regexp"
	"strings"
)

// StopwatchProvider answers "stopwatch [start|pause|resume|stop|reset|lap]"
// runner queries (Requested: "the timer, alarm and stopwatch features
// need to be implemented: they should appear in the status bar overlay and
// can be called from the runner as well"). Timer/alarm already had a
// runner path (TimerProvider, this package) — a stopwatch is a separate
// provider, not a third branch there, matching Services/Stopwatch.qml's
// own reasoning on the phi-shell side for why it is a separate service:
// genuinely different shape (no target time, no ringtone/overlay, just a
// running/paused counter), so nothing here shares parsing logic with
// TimerProvider beyond the same ActionExec/`qs ipc call` handoff shape
// every shell-owned action in this package already uses (SystemActionsProvider,
// TimerProvider itself).
//
// Same cold-start reasoning as TimerProvider: this provider never tracks
// whether a stopwatch is currently running — `phi` is a fresh process on
// every keystroke and has no session to hold that in, so a bare
// "stopwatch" query always offers "start or pause" (mapped to the shell's
// own `toggle` IPC call, which already knows its own live state) rather
// than a guessed-wrong "Start"/"Pause" label.
type StopwatchProvider struct{}

func (StopwatchProvider) Name() string { return "stopwatch" }

// Requires a word boundary after "stopwatch" (`\b`, not just a prefix
// match) so "stopwatcher" or similar does not misfire — same discipline
// TimerProvider's own `timerQueryRe`/`alarmQueryRe` already apply.
var stopwatchQueryRe = regexp.MustCompile(`(?i)^stopwatch\b\s*(.*)$`)

func (p StopwatchProvider) Query(_ context.Context, q string) []Result {
	q = strings.TrimSpace(q)
	m := stopwatchQueryRe.FindStringSubmatch(q)
	if m == nil {
		return nil
	}
	rest := strings.ToLower(strings.TrimSpace(m[1]))

	var title, verb string
	switch rest {
	case "":
		title, verb = "Start or pause the stopwatch", "toggle"
	case "start", "resume":
		title, verb = "Start the stopwatch", "start"
	case "pause", "stop":
		title, verb = "Pause the stopwatch", "pause"
	case "reset":
		title, verb = "Reset the stopwatch", "reset"
	case "lap":
		title, verb = "Record a stopwatch lap", "lap"
	default:
		return nil
	}

	return []Result{{
		ID: "stopwatch:" + verb, Provider: p.Name(),
		Title: title,
		// Trusted as-is — same reasoning TimerProvider's own branches give:
		// exact-syntax recognised input, not a fuzzy guess Rank should
		// second-guess against the raw query text.
		Score: 100,
		Action: Action{Kind: ActionExec, Data: map[string]string{
			"command": "qs -p ~/.config/quickshell/phi ipc call stopwatch " + verb,
		}},
	}}
}
