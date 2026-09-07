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
	}

	return result, nil
}
