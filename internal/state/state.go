// Package state is the defined home for phi's runtime state: distinct from
// phios-dotfiles' versioned configuration, and never entering any repository.
// It lives at $XDG_STATE_HOME/phi as one flat text file per key — the format
// bin/lib/tokens.sh's phios_variant() already reads for theme-variant, so
// nothing here may change how that file looks on disk, only who else can
// read and write it.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Keys is the closed set of valid state keys. Get and Set both reject
// anything not listed here before touching the filesystem, which also prevents
// a key from ever escaping the state directory.
//
// Notably absent: launcher frecency and clipboard/notification history.
// These are collections, not scalars, and need their own storage model when
// those features are built; storing them as a single string value would be
// premature speculation.
var Keys = map[string]bool{
	"theme.variant":     true, // active theme variant — written by phi theme set until the settings panel exists
	"monitor.config":    true, // monitor configuration
	"wallpaper.path":    true, // active wallpaper image path
	"toggle.night-mode": true, // runtime toggle state
	"toggle.dnd":        true,
	"toggle.spotlight":  true,
	"toggle.chroma":     true,

	// Settings-panel additions: scalars the Theme/Devices sections need keys
	// for, consistent with the existing toggle and theme-variant pattern.
	"nightmode.temp":   true, // target Kelvin for the manual (non-True-Tone) night-shift profile
	"toggle.true-tone": true, // ambient-light-driven night shift instead of the clock profile
	"spotlight.size":   true, // "small" | "medium" | "large"
	"chroma.color":     true, // hex string for the static-colour override

	// Added by Out-of-plan: settings-overhaul (batch D — the full wallpaper
	// section). wallpaper.path already existed as "the active image"; the
	// rest are the compositing parameters the shell's background layer
	// reads. All scalars, same category as the toggles above.
	"wallpaper.color":             true, // "#rrggbb" solid base — the visible layer when there is no image, or the image is not cover/stretch
	"wallpaper.mode":              true, // "cover" | "contain" | "stretch" | "repeat"
	"wallpaper.scale":             true, // float, image zoom for contain/repeat (ignored for cover/stretch)
	"wallpaper.texture":           true, // "" | a phi wallpaper texture mode name
	"wallpaper.texture-intensity": true, // 0-100

	// Requested: "add option for automated night mode (automatic time
	// at nighttime or manual hours range), with settings" — whether
	// toggle.night-mode is flipped by hand or on a clock, and the window
	// used when it's on a clock. Scalars, same category as the toggles
	// and nightmode.temp above.
	"nightmode.schedule":       true, // "off" | "auto" | "custom"
	"nightmode.schedule-start": true, // hour 0-23, start of the "custom" window
	"nightmode.schedule-end":   true, // hour 0-23, end of the "custom" window

	// Requested: "the settings should allow an 'auto' theme option, where
	// it changes from dark to light based on the time of day." Same shape
	// as nightmode.schedule above (off/auto/custom + a custom hour window)
	// — deliberately mirrored rather than inventing a second convention for
	// the same kind of scalar. "auto" reads as "light during daylight
	// hours, dark otherwise"; the exact daylight source (fixed hours vs.
	// something location-aware) is the shell's own concern, not this
	// file's — phi only stores the toggle and the custom-window scalars.
	"theme.schedule":       true, // "off" | "auto" | "custom"
	"theme.schedule-start": true, // hour 0-23, dark variant starts
	"theme.schedule-end":   true, // hour 0-23, light variant starts

	// Both scalars, same
	// category as every toggle/value above.
	"bar.battery-percent": true, // "true" | "false" — show the numeric charge next to the battery bar icon
	"terminal.padding":    true, // integer px, kitty's own window_padding_width — default 40 per the request, user-editable
}

// Dir is $XDG_STATE_HOME/phi, or $HOME/.local/state/phi when XDG_STATE_HOME
// is unset — the same rule bin/lib/common.sh's phios_runtime_dir uses.
func Dir() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "phi"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "phi"), nil
}

// filename maps a dotted key to the flat file that holds it. theme.variant
// has to land on the literal name theme-variant — the file bin/lib/tokens.sh
// already reads — so the rule is exactly the substitution that produces it:
// replace every '.' with '-'. No key needs a different rule; none collide.
func filename(key string) string {
	return strings.ReplaceAll(key, ".", "-")
}

func checkKey(key string) error {
	if !Keys[key] {
		var names []string
		for k := range Keys {
			names = append(names, k)
		}
		sort.Strings(names)
		return fmt.Errorf("unknown state key: %s (defined keys: %s)", key, strings.Join(names, ", "))
	}
	return nil
}

// Get reads key. ok is false when the key is valid but has never been set —
// that is not an error, it is the initial state of every key.
func Get(key string) (value string, ok bool, err error) {
	if err := checkKey(key); err != nil {
		return "", false, err
	}
	dir, err := Dir()
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, filename(key)))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(data)), true, nil
}

// Set writes key. It has nothing to do with any repository: it writes only
// under Dir(), which is always outside every repository.
func Set(key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, filename(key)), []byte(value+"\n"), 0o644)
}

// Entry is one row of List.
type Entry struct {
	Key   string
	Value string
	Set   bool
}

// List reports every defined key and, for the ones a previous Set wrote,
// its value — never a fabricated value for a key that was never set.
func List() ([]Entry, error) {
	var names []string
	for k := range Keys {
		names = append(names, k)
	}
	sort.Strings(names)

	out := make([]Entry, 0, len(names))
	for _, k := range names {
		v, ok, err := Get(k)
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Key: k, Value: v, Set: ok})
	}
	return out, nil
}
