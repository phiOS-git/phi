package theme

import "phi/internal/state"

// ScheduleOff is theme.schedule's value meaning "automatic switching is
// disabled" — the only value a confirmed `phi theme set` ever writes itself,
// and what an unset key means (mirrors CurrentVariant's own unset-key
// fallback in variant.go).
const ScheduleOff = "off"

// CurrentSchedule reads theme.schedule, treating an unset key or a read
// error the same as ScheduleOff: nothing to confirm, nothing to turn off.
func CurrentSchedule() string {
	v, ok, err := state.Get("theme.schedule")
	if err != nil || !ok || v == "" {
		return ScheduleOff
	}
	return v
}

// TurnOffSchedule writes theme.schedule=off — what a confirmed `phi theme
// set` does before applying the variant the user actually asked for, so
// phi-shell's Services/ThemeSchedule.qml (which watches this file directly)
// stops overriding that choice at the next scheduled boundary.
func TurnOffSchedule() error {
	return state.Set("theme.schedule", ScheduleOff)
}

// ScheduleConfirm decides what `phi theme set VARIANT` should do about an
// active automatic schedule before applying a variant that may not be the
// schedule's own choice.
//
// This lives here rather than in Set() itself, and is never consulted by
// Set()'s other callers: `phi update` calls Set directly with
// CurrentVariant() (re-rendering whatever is already active, never a
// schedule override), and so does the pacman regen hook
// (phios-dotfiles/profiles/base/system/usr/local/bin/phi-theme-regen-hook.sh,
// via `phi state get theme.variant` piped into `phi theme set`) — both are
// non-interactive re-renders that must never prompt or touch the schedule.
// Only internal/cli's `theme set` verb calls this.
//
//   - schedule is CurrentSchedule()'s result.
//   - sameVariant is whether VARIANT already matches CurrentVariant(): the
//     hook and `phi update` always pass the active variant back to Set, so
//     this alone keeps them silent even if one of them ever went through
//     this verb's CLI path instead of Set directly — re-rendering the
//     variant that's already showing is not an override of anything.
//   - tty is whether the process has a real interactive terminal on both
//     stdin and stdout (see internal/cli.IsTerminal's own note on why both:
//     a redirected stream can still report as a character device on one
//     end alone). The one setting a live prompt makes sense in.
//   - yes is the --yes/-y flag. It always behaves like a confirmed "y",
//     including when sameVariant is true — that combination is how a script
//     or a terminal user pins "this exact variant, permanently" without an
//     interactive prompt.
//   - ask is called only when a live decision is actually needed (schedule
//     on, variant changing, tty, no --yes); it must return whether the user
//     confirmed. Kept as an injected function so the decision stays testable
//     without a real terminal or a real prompt loop.
//
// turnOff reports whether the caller must call TurnOffSchedule before
// applying the variant. proceed reports whether the variant should be
// applied at all — false only when an interactive user was asked and said
// no.
func ScheduleConfirm(schedule string, sameVariant, tty, yes bool, ask func() bool) (turnOff, proceed bool) {
	if schedule == "" || schedule == ScheduleOff {
		return false, true // nothing to turn off
	}
	if yes {
		return true, true // explicit flag: always turn it off, never prompt
	}
	if sameVariant {
		return false, true // re-rendering the active variant is not an override
	}
	if !tty {
		return false, true // scripts, hooks, and the shell's own Process calls: unchanged behavior
	}
	if ask() {
		return true, true
	}
	return false, false // declined: apply nothing, leave the schedule on
}
