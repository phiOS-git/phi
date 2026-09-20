package query

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TimerProvider answers "timer <duration> [label]" and "alarm <HH:MM>
// [label]" runner queries (Requested: "add a timer and alarm feature to
// phi, also add tools to the runner to quicky setup timers and alarms").
//
// Timers and alarms can only actually fire from something that keeps
// running for the whole session — `phi` itself is a fresh process on
// every keystroke (phi/CLAUDE.md's cold-start constraint: "phi query on
// every keystroke... no heavyweight init"), so it cannot own a schedule to
// wait on. Like reboot/shutdown/volume/brightness/screenshot (system actions),
// timers stay in the shell. This provider recognises the query and hands the
// shell a plain `qs ipc call timer ...` command to run (ActionExec), never
// a command phi runs or waits on itself. Services/Timers.qml (phi-shell)
// owns the actual item list, firing, overlay and ringtone. "add ... to
// phi" is satisfied literally even so: this file is compiled into the phi
// binary, same as every other provider here — there is deliberately no
// new `internal/cli` "timer"/"alarm" verb alongside it, since one would
// have nothing to do that this provider and the shell don't already do
// between them. Flagged for cheap veto if a standalone terminal verb was
// actually wanted instead of (or as well as) the runner integration.
type TimerProvider struct{}

func (TimerProvider) Name() string { return "timer" }

var (
	timerQueryRe = regexp.MustCompile(`(?i)^timer\s+(.+)$`)
	alarmQueryRe = regexp.MustCompile(`(?i)^alarm\s+(.+)$`)
	durationRe   = regexp.MustCompile(`^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?`)
	clockTimeRe  = regexp.MustCompile(`^(\d{1,2}):(\d{2})\b`)
)

func (p TimerProvider) Query(_ context.Context, q string) []Result {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}

	if m := timerQueryRe.FindStringSubmatch(q); m != nil {
		rest := strings.TrimSpace(m[1])
		seconds, consumed, ok := parseDuration(rest)
		if !ok {
			return nil
		}
		label := strings.TrimSpace(rest[consumed:])
		return []Result{{
			ID: "timer:" + q, Provider: p.Name(),
			Title:    "Set a timer for " + formatDuration(seconds),
			Subtitle: timerSubtitle("Timer", label),
			// Trusted as-is (rank.go): the generated Title ("Set a timer
			// for 5m") does not textually resemble the raw query ("timer
			// 5m tea") the way matchWeight expects, the same reason
			// CalculatorProvider sets its own Score rather than leaving
			// Rank to fuzzy-match a computed answer against the
			// expression that produced it. A successful parse here is
			// deliberate, exact-syntax input, not a fuzzy guess.
			Score: 100,
			Action: Action{Kind: ActionExec, Data: map[string]string{
				"command": "qs -p ~/.config/quickshell/phi ipc call timer add " +
					strconv.Itoa(seconds) + " " + shellQuote(label),
			}},
		}}
	}

	if m := alarmQueryRe.FindStringSubmatch(q); m != nil {
		rest := strings.TrimSpace(m[1])
		hour, minute, consumed, ok := parseClockTime(rest)
		if !ok {
			return nil
		}
		label := strings.TrimSpace(rest[consumed:])
		return []Result{{
			ID: "alarm:" + q, Provider: p.Name(),
			Title:    fmt.Sprintf("Set an alarm for %02d:%02d", hour, minute),
			Subtitle: timerSubtitle("Alarm", label),
			Score:    100, // trusted as-is — see the timer branch's own comment above
			Action: Action{Kind: ActionExec, Data: map[string]string{
				"command": "qs -p ~/.config/quickshell/phi ipc call timer addAlarm " +
					strconv.Itoa(hour) + " " + strconv.Itoa(minute) + " " + shellQuote(label),
			}},
		}}
	}

	return nil
}

// parseDuration parses a leading "5m" / "1h30m" / "90s" shape off the
// front of s — h/m/s in that order, each optional, at least one required.
// Returns ok=false for no unit at all ("timer 5", "timer tea") rather than
// guessing a default unit: this project's own glyphs.js history is the
// standing lesson against shipping a guessed-not-confirmed value, and a
// silently-wrong timer length is exactly that class of mistake.
func parseDuration(s string) (seconds int, consumed int, ok bool) {
	loc := durationRe.FindStringSubmatchIndex(s)
	if loc == nil || loc[1] == 0 {
		return 0, 0, false
	}
	m := durationRe.FindStringSubmatch(s)
	total := 0
	if m[1] != "" {
		h, _ := strconv.Atoi(m[1])
		total += h * 3600
	}
	if m[2] != "" {
		mm, _ := strconv.Atoi(m[2])
		total += mm * 60
	}
	if m[3] != "" {
		ss, _ := strconv.Atoi(m[3])
		total += ss
	}
	if total <= 0 {
		return 0, 0, false
	}
	return total, loc[1], true
}

// parseClockTime parses a leading 24-hour "H:MM" / "HH:MM" off the front
// of s. 24-hour only, no am/pm — the TODO names no format, and this
// matches the alarm/clock time everywhere else in this system already
// assumes canonically (Services/NightShift.qml's schedule, phi-shell's own
// 24-hour-by-default clock setting), rather than adding a second parsing
// path this feature alone would need.
func parseClockTime(s string) (hour, minute, consumed int, ok bool) {
	loc := clockTimeRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return 0, 0, 0, false
	}
	m := clockTimeRe.FindStringSubmatch(s)
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	if h > 23 || mi > 59 {
		return 0, 0, 0, false
	}
	return h, mi, loc[1], true
}

func formatDuration(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	var parts []string
	if h > 0 {
		parts = append(parts, strconv.Itoa(h)+"h")
	}
	if m > 0 {
		parts = append(parts, strconv.Itoa(m)+"m")
	}
	if s > 0 || len(parts) == 0 {
		parts = append(parts, strconv.Itoa(s)+"s")
	}
	return strings.Join(parts, " ")
}

func timerSubtitle(kind, label string) string {
	if label == "" {
		return kind
	}
	return kind + " — " + label
}
