package theme

import "testing"

// askSpy counts calls and returns a fixed answer, so a test can assert not
// just the decision but whether a live prompt would actually have been
// shown — turnOff/proceed alone can't distinguish "schedule was off" from
// "schedule was on but non-interactive", both of which return (false, true).
func askSpy(answer bool) (ask func() bool, calls *int) {
	n := 0
	return func() bool {
		n++
		return answer
	}, &n
}

func TestScheduleConfirmOffNeverPrompts(t *testing.T) {
	ask, calls := askSpy(true)
	turnOff, proceed := ScheduleConfirm(ScheduleOff, false, true, false, ask)
	if turnOff || !proceed {
		t.Errorf("ScheduleConfirm(off, ...) = (%v, %v), want (false, true)", turnOff, proceed)
	}
	if *calls != 0 {
		t.Errorf("ask was called %d times, want 0 — schedule off should never prompt", *calls)
	}
}

func TestScheduleConfirmUnsetTreatedAsOff(t *testing.T) {
	ask, calls := askSpy(true)
	turnOff, proceed := ScheduleConfirm("", false, true, false, ask)
	if turnOff || !proceed {
		t.Errorf("ScheduleConfirm(\"\", ...) = (%v, %v), want (false, true)", turnOff, proceed)
	}
	if *calls != 0 {
		t.Errorf("ask was called %d times, want 0 — an unset key means off", *calls)
	}
}

func TestScheduleConfirmTTYYesTurnsOff(t *testing.T) {
	ask, calls := askSpy(true)
	turnOff, proceed := ScheduleConfirm("auto", false, true, false, ask)
	if !turnOff || !proceed {
		t.Errorf("ScheduleConfirm(auto, tty, yes-answer) = (%v, %v), want (true, true)", turnOff, proceed)
	}
	if *calls != 1 {
		t.Errorf("ask was called %d times, want exactly 1", *calls)
	}
}

func TestScheduleConfirmTTYNoAborts(t *testing.T) {
	ask, calls := askSpy(false)
	turnOff, proceed := ScheduleConfirm("custom", false, true, false, ask)
	if turnOff || proceed {
		t.Errorf("ScheduleConfirm(custom, tty, no-answer) = (%v, %v), want (false, false)", turnOff, proceed)
	}
	if *calls != 1 {
		t.Errorf("ask was called %d times, want exactly 1", *calls)
	}
}

func TestScheduleConfirmFlagSkipsPrompt(t *testing.T) {
	ask, calls := askSpy(false) // would decline if asked — flag must bypass ask entirely
	turnOff, proceed := ScheduleConfirm("auto", false, true, true, ask)
	if !turnOff || !proceed {
		t.Errorf("ScheduleConfirm(auto, tty, --yes) = (%v, %v), want (true, true)", turnOff, proceed)
	}
	if *calls != 0 {
		t.Errorf("ask was called %d times, want 0 — --yes must not prompt", *calls)
	}
}

func TestScheduleConfirmFlagTurnsOffEvenForSameVariant(t *testing.T) {
	// A script pinning "this exact variant, permanently" with --yes even
	// though it already matches what's active.
	ask, calls := askSpy(false)
	turnOff, proceed := ScheduleConfirm("auto", true, true, true, ask)
	if !turnOff || !proceed {
		t.Errorf("ScheduleConfirm(auto, same-variant, tty, --yes) = (%v, %v), want (true, true)", turnOff, proceed)
	}
	if *calls != 0 {
		t.Errorf("ask was called %d times, want 0", *calls)
	}
}

func TestScheduleConfirmNonTTYUnchangedBehavior(t *testing.T) {
	// The pacman regen hook and phi-shell's own scheduled Process calls:
	// no controlling terminal, no --yes. Must apply the variant and leave
	// the schedule exactly as it was — this is the compatibility floor
	// every existing non-interactive caller depends on.
	ask, calls := askSpy(true)
	turnOff, proceed := ScheduleConfirm("auto", false, false, false, ask)
	if turnOff || !proceed {
		t.Errorf("ScheduleConfirm(auto, non-tty) = (%v, %v), want (false, true)", turnOff, proceed)
	}
	if *calls != 0 {
		t.Errorf("ask was called %d times, want 0 — must never block a non-interactive caller", *calls)
	}
}

func TestScheduleConfirmSameVariantSkipsPromptEvenOnTTY(t *testing.T) {
	// Re-rendering the variant that's already active (Theme.qml's
	// terminal-padding row, or the pacman hook if it ever ran through this
	// verb from a real terminal) is not a schedule override.
	ask, calls := askSpy(true)
	turnOff, proceed := ScheduleConfirm("custom", true, true, false, ask)
	if turnOff || !proceed {
		t.Errorf("ScheduleConfirm(custom, same-variant, tty) = (%v, %v), want (false, true)", turnOff, proceed)
	}
	if *calls != 0 {
		t.Errorf("ask was called %d times, want 0 — same variant should never prompt", *calls)
	}
}
