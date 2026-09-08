// Package state is the defined home for phi's runtime state (master plan
// §5.6): distinct from phios-dotfiles' versioned configuration, and never
// entering any repository. It lives at $XDG_STATE_HOME/phi as one flat text
// file per key — the format bin/lib/tokens.sh's phios_variant() already
// reads for theme-variant, so nothing here may change how that file looks on
// disk, only who else can read and write it.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Keys is the closed set §5.6 defines. "Nothing else may be stored there"
// (S-13 AGENT contract) — Get and Set both reject anything not listed here
// before touching the filesystem, which is also what keeps a key from ever
// escaping the state directory.
//
// Two §5.6 rows are deliberately absent: launcher frecency and clipboard/
// notification history are collections, not scalars, and each already has
// an owning step (S-33/S-73, S-32, S-30) that has not run yet. Modelling
// them as a single string value here would be a guess this step has no
// grounds for; they get real keys, or a different storage shape entirely,
// when their step defines one.
var Keys = map[string]bool{
	"theme.variant":     true, // §5.6 "variante di tema attiva" — written by phi theme set until the settings panel exists
	"monitor.config":    true, // §5.6 "configurazione monitor" (ADR 077)
	"wallpaper.path":    true, // §5.6 "percorso dello sfondo attivo"
	"toggle.night-mode": true, // §5.6 "stato dei toggle runtime"
	"toggle.dnd":        true,
	"toggle.spotlight":  true,
	"toggle.chroma":     true,

	// Added at S-40 (settings panel, master plan §9.12): each of these is a
	// VALUE the Theme/Devices sections need a key for, not a new toggle
	// category — §5.6 lists "stato dei toggle runtime" and "variante di
	// tema" as examples, not an exhaustive enumeration, and phi-shell/
	// CLAUDE.md's own S-20 precedent (checking the roadmap before reading a
	// boundary strictly) applies the same way here: S-42/S-43/S-46 already
	// plan exactly this kind of scalar. Batched into this one phi change
	// rather than three separate version bumps across M4, since every one
	// needs the same rebuild-in-chroot-then-reinstall cycle from the user
	// (master plan §3.1) regardless of which step's QML first reads it.
	"nightmode.temp":   true, // target Kelvin for the manual (non-True-Tone) night-shift profile, consumed at S-42
	"toggle.true-tone": true, // ambient-light-driven night shift instead of the clock profile, consumed at S-42 — distinct from toggle.night-mode, which is the feature's own on/off
	"spotlight.size":   true, // "small" | "medium" | "large", consumed at S-43
	"chroma.color":     true, // hex string for the static-colour override, consumed at S-46
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
// that is not an error, it is the initial state of everything in §5.6.
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
// under Dir(), which §5.6 places outside every one of them.
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
