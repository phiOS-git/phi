package theme

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultVariant is what an absent or empty state file means (master plan
// §5.6, bin/lib/tokens.sh's phios_variant).
const DefaultVariant = "dark"

// stateDir is $XDG_STATE_HOME/phi, the runtime-state home §5.6 assigns to
// phi — never the repository, and never applied to by anything but phi
// itself and the settings panel.
func stateDir() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "phi"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "phi"), nil
}

// CurrentVariant reads the active theme variant that a previous `phi theme
// set` recorded, falling back to DefaultVariant exactly as bin/lib/tokens.sh
// does — a missing or unreadable file is not an error.
func CurrentVariant() string {
	dir, err := stateDir()
	if err != nil {
		return DefaultVariant
	}
	data, err := os.ReadFile(filepath.Join(dir, "theme-variant"))
	if err != nil {
		return DefaultVariant
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return DefaultVariant
	}
	return v
}

// RecordVariant writes the active variant so the next CurrentVariant call —
// by phi itself, and by bin/lib/tokens.sh's phios_variant, which reads this
// exact path — picks it up. Set is what calls this; render and preview never
// do, since they only read a variant, they do not choose one.
func RecordVariant(variant string) error {
	dir, err := stateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "theme-variant"), []byte(variant+"\n"), 0o644)
}
