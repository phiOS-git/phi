package theme

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"phi/internal/tokens"
)

// State is what SetResult found (or would find, under --dry-run) about one
// adapter's destination file.
type State string

const (
	StateOK     State = "ok"     // on disk already matches the rendered content
	StateCreate State = "create" // destination does not exist yet
	StateUpdate State = "update" // destination exists with different content
)

// ReloadOutcome says what Set did about a changed target's reload.
type ReloadOutcome string

const (
	ReloadNone    ReloadOutcome = "none"    // Class C, or reload is "-": no reload path exists
	ReloadUnwired ReloadOutcome = "unwired" // adapters.txt still has "[unknown]" for this row
	ReloadRan     ReloadOutcome = "ran"     // the reload command ran and exited 0
	ReloadFailed  ReloadOutcome = "failed"  // the reload command ran and exited non-zero
)

// AdapterResult is one adapter's outcome from Set.
type AdapterResult struct {
	Adapter       Adapter
	State         State
	ReloadOutcome ReloadOutcome
	ReloadError   error
}

// SetResult is the whole of one `phi theme set` run.
type SetResult struct {
	Variant  string
	DryRun   bool
	Adapters []AdapterResult
	// PortalPreference is PortalNone under --dry-run: writing the live
	// preference is exactly the kind of side effect --dry-run promises not
	// to perform (S-01 AGENT contract, "touch nothing").
	PortalPreference PortalOutcome
}

// Changed reports the adapters this run rendered (or would render).
func (r SetResult) Changed() []AdapterResult {
	var out []AdapterResult
	for _, a := range r.Adapters {
		if a.State != StateOK {
			out = append(out, a)
		}
	}
	return out
}

// RestartRequired lists the changed Class C adapters — collected, never
// restarted (S-12 AGENT contract).
func (r SetResult) RestartRequired() []AdapterResult {
	var out []AdapterResult
	for _, a := range r.Changed() {
		if a.Adapter.Class == ClassC {
			out = append(out, a)
		}
	}
	return out
}

// Set renders every design/adapters.txt target for variant, reloads Class A
// and B targets that changed, and — unless dryRun — writes the destination
// files and records the active variant (bin/lib/tokens.sh's phios_variant
// reads exactly what RecordVariant writes). It is idempotent: an adapter
// whose destination already holds the rendered bytes is left untouched and
// never reloaded.
func Set(root, variant string, dryRun bool) (SetResult, error) {
	adapters, err := ParseAdapters(root)
	if err != nil {
		return SetResult{}, err
	}
	tk, err := tokens.Load(root, variant)
	if err != nil {
		return SetResult{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return SetResult{}, err
	}

	result := SetResult{Variant: variant, DryRun: dryRun}
	for _, a := range adapters {
		src, err := os.ReadFile(filepath.Join(root, a.Template))
		if err != nil {
			return SetResult{}, fmt.Errorf("%s: %w", a.Template, err)
		}
		rendered := Substitute(tk, src)
		dest := filepath.Join(home, a.Destination)

		state := StateUpdate
		if existing, err := os.ReadFile(dest); err == nil {
			if string(existing) == string(rendered) {
				state = StateOK
			}
		} else if os.IsNotExist(err) {
			state = StateCreate
		} else {
			return SetResult{}, fmt.Errorf("%s: %w", dest, err)
		}

		ar := AdapterResult{Adapter: a, State: state, ReloadOutcome: ReloadNone}

		if state != StateOK {
			if !dryRun {
				if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
					return SetResult{}, fmt.Errorf("%s: %w", dest, err)
				}
				if err := os.WriteFile(dest, rendered, 0o644); err != nil {
					return SetResult{}, fmt.Errorf("%s: %w", dest, err)
				}
			}

			if a.Class == ClassA || a.Class == ClassB {
				switch a.Reload {
				case "-":
					ar.ReloadOutcome = ReloadNone
				case "[unknown]":
					ar.ReloadOutcome = ReloadUnwired
				default:
					if dryRun {
						ar.ReloadOutcome = ReloadNone
					} else {
						cmd := exec.Command("sh", "-c", a.Reload)
						cmd.Dir = home
						if err := cmd.Run(); err != nil {
							ar.ReloadOutcome = ReloadFailed
							ar.ReloadError = err
						} else {
							ar.ReloadOutcome = ReloadRan
						}
					}
				}
			}
		}

		result.Adapters = append(result.Adapters, ar)
	}

	if !dryRun {
		if err := RecordVariant(variant); err != nil {
			return SetResult{}, fmt.Errorf("recording active variant: %w", err)
		}
		result.PortalPreference = setPortalPreference(variant)
		if err := writeQtPlatformThemeConfig(home); err != nil {
			return SetResult{}, fmt.Errorf("writing qt5ct/qt6ct config: %w", err)
		}
	}

	return result, nil
}

// writeQtPlatformThemeConfig writes the [Appearance] section of
// ~/.config/qt5ct/qt5ct.conf and ~/.config/qt6ct/qt6ct.conf, pointing
// color_scheme_path at the palette file the adapters.txt rows above already
// rendered. This is NOT an adapters.txt row: that mechanism only knows
// PHI_* token substitution (internal/theme/render.go's own contract), and
// color_scheme_path needs the resolved $HOME path this function already has
// in scope, which is not a design token. Format confirmed against a real
// qt6ct.conf (github.com/dusklinux/dusky, read for the [Appearance] KEY
// SHAPE only, no content copied — same practice as the qt6ct colour-scheme
// template's own note). Every run rewrites only this one section: like
// every Class C destination in design/adapters.txt, this file is generated,
// not hand-edited — a real qt5ct/qt6ct GUI run would still work for
// [Fonts]/[Interface], which this function never touches, but a change to
// [Appearance] made through that GUI is overwritten on the next
// `phi theme set`, the same contract every other themed target already has.
func writeQtPlatformThemeConfig(home string) error {
	content := "[Appearance]\n" +
		"custom_palette=true\n" +
		"standard_dialogs=xdgdesktopportal\n" +
		"style=Fusion\n"

	for _, name := range []string{"qt5ct", "qt6ct"} {
		dir := filepath.Join(home, ".config", name)
		colorPath := filepath.Join(dir, "colors", "phi.conf")
		full := content + "color_scheme_path=" + colorPath + "\n"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name+".conf"), []byte(full), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// PortalOutcome says what Set did about the live light/dark preference a
// portal-aware app (GTK4/libadwaita, and Qt apps behind xdg-desktop-portal)
// reads to switch WITHOUT a restart — distinct from the generated GTK3/GTK4/
// Qt colour files below, which are Class C (master plan §6.7's own class
// table: "tema GTK/Qt per app native" is C, restart required). This is the
// one live-application path S-41's AGENT bullet asks for ("set the
// colour-scheme preference through the portal so native apps follow").
type PortalOutcome string

const (
	PortalNone      PortalOutcome = "none" // gsettings not on PATH — degrades, does not fail Set
	PortalSet       PortalOutcome = "set"  // gsettings ran and exited 0
	PortalSetFailed PortalOutcome = "failed"
)

// setPortalPreference writes org.gnome.desktop.interface color-scheme via
// gsettings. xdg-desktop-portal-gtk (already in profiles/desktop/
// packages.txt since S-20) implements org.freedesktop.impl.portal.Settings
// by reading exactly this GSettings key and emitting SettingChanged over
// D-Bus when it changes — the mechanism every portal-aware toolkit's own
// docs describe for the light/dark preference specifically, not guessed.
// Needs gsettings-desktop-schemas (S-41 packages.txt addition) for the
// schema to exist at all; absence degrades to PortalNone; the GTK3/GTK4/Qt
// colour FILES above are unaffected either way — an app that ignores the
// portal signal still gets the right colours after its own Class C restart.
func setPortalPreference(variant string) PortalOutcome {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return PortalNone
	}
	value := "default"
	if variant == "dark" {
		value = "prefer-dark"
	}
	cmd := exec.Command("gsettings", "set", "org.gnome.desktop.interface", "color-scheme", value)
	if err := cmd.Run(); err != nil {
		return PortalSetFailed
	}
	return PortalSet
}
