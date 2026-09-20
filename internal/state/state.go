// Package state manages phi's runtime state at $XDG_STATE_HOME/phi (one
// file per key, matching bin/lib/tokens.sh's existing theme-variant format).
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Keys is the closed set of valid state keys (all others rejected).
var Keys = map[string]bool{
	"theme.variant":     true, // active theme variant — written by phi theme set until the settings panel exists
	"monitor.config":    true, // monitor configuration
	"wallpaper.path":    true, // active wallpaper image path
	"toggle.night-mode": true, // runtime toggle state
	"toggle.dnd":        true,
	"toggle.spotlight":  true,
	"toggle.chroma":     true,

	// Settings-panel scalars (Theme/Devices sections).
	"nightmode.temp":   true, // target Kelvin for the manual (non-True-Tone) night-shift profile
	"toggle.true-tone": true, // ambient-light-driven night shift instead of the clock profile
	"spotlight.size":   true, // "small" | "medium" | "large"
	"chroma.color":     true, // hex string for the static-colour override

	// Wallpaper compositing parameters (shell's background layer).
	"wallpaper.color":             true, // "#rrggbb" solid base — the visible layer when there is no image, or the image is not cover/stretch
	"wallpaper.mode":              true, // "cover" | "contain" | "stretch" | "repeat"
	"wallpaper.scale":             true, // float, image zoom for contain/repeat (ignored for cover/stretch)
	"wallpaper.texture":           true, // "" | a phi wallpaper texture mode name
	"wallpaper.texture-intensity": true, // 0-100

	// Night mode scheduling (off/auto/custom + custom hours).
	"nightmode.schedule":       true, // "off" | "auto" | "custom"
	"nightmode.schedule-start": true, // hour 0-23, start of the "custom" window
	"nightmode.schedule-end":   true, // hour 0-23, end of the "custom" window

	// Theme scheduling (off/auto/custom + custom hours, like nightmode).
	"theme.schedule":       true, // "off" | "auto" | "custom"
	"theme.schedule-start": true, // hour 0-23, dark variant starts
	"theme.schedule-end":   true, // hour 0-23, light variant starts

	// UI scalars (battery percent display, terminal padding).
	"bar.battery-percent": true, // "true" | "false" — show the numeric charge next to the battery bar icon
	"terminal.padding":    true, // integer px, kitty's own window_padding_width — default 40 per the request, user-editable
}

// Dir returns $XDG_STATE_HOME/phi (or $HOME/.local/state/phi if unset).
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

// filename maps a key to its filename (replace '.' with '-').
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
