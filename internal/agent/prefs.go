package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// UI-written prefs (plan §8), read on every pi spawn: default profile,
// per-profile model/thinking overrides, idle and dialog timeouts, and the
// scheduler's own on/off and daily spend cap. Regenerable — never the
// source of truth for anything phi itself needs to run, only what the panel
// asked for last.

// SchedulerPrefs is Prefs.Scheduler (plan §5.7's "enabled"/"dailyCap").
type SchedulerPrefs struct {
	Enabled  bool    `json:"enabled"`
	DailyCap float64 `json:"dailyCap"`
}

// Prefs is $XDG_STATE_HOME/phi-agent/prefs.json.
type Prefs struct {
	DefaultProfile       string            `json:"defaultProfile"`
	Models               map[string]string `json:"models"`   // profile -> "provider/id", "" = pi's default
	Thinking             map[string]string `json:"thinking"` // profile -> level, "" = pi's default
	IdleMinutes          int               `json:"idleMinutes"`
	DialogTimeoutSeconds int               `json:"dialogTimeoutSeconds"`
	Scheduler            SchedulerPrefs    `json:"scheduler"`
}

// prefsModelProfiles: the profiles models./thinking. keys may name — every
// profile that actually spawns a pi process with a chosen model (inline
// reuses whichever chat profile invoked it, so it has no override of its own).
var prefsModelProfiles = []Profile{General, Academic, Coding}

// prefsValidThinking: pi's thinking levels, plus "" for "pi's default".
var prefsValidThinking = map[string]bool{
	"": true, "off": true, "minimal": true, "low": true, "medium": true,
	"high": true, "xhigh": true, "max": true,
}

const prefsKeyList = `defaultProfile, models.<general|academic|coding>, thinking.<general|academic|coding>, idleMinutes, dialogTimeoutSeconds, scheduler.enabled, scheduler.dailyCap`

// DefaultPrefs is what a fresh install, or a prefs.json missing a field,
// falls back to.
func DefaultPrefs() Prefs {
	return Prefs{
		DefaultProfile:       string(General),
		Models:               map[string]string{},
		Thinking:             map[string]string{},
		IdleMinutes:          15,
		DialogTimeoutSeconds: 600,
		Scheduler:            SchedulerPrefs{Enabled: false, DailyCap: 1.0},
	}
}

// PrefsPath is $XDG_STATE_HOME/phi-agent/prefs.json.
func PrefsPath() (string, error) {
	s, err := stateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(s, "phi-agent", "prefs.json"), nil
}

// LoadPrefs reads prefs.json overlaid on DefaultPrefs (a field the file
// omits keeps its default, rather than zeroing), clamping the two duration
// fields and never returning nil maps. A missing file is not an error — it
// means nothing has been customised yet.
func LoadPrefs() (Prefs, error) {
	path, err := PrefsPath()
	if err != nil {
		return Prefs{}, err
	}
	prefs := DefaultPrefs()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return prefs, nil
		}
		return Prefs{}, err
	}
	if err := json.Unmarshal(b, &prefs); err != nil {
		return Prefs{}, fmt.Errorf("%s: %w", path, err)
	}
	if prefs.Models == nil {
		prefs.Models = map[string]string{}
	}
	if prefs.Thinking == nil {
		prefs.Thinking = map[string]string{}
	}
	prefs.clamp()
	return prefs, nil
}

// SavePrefs writes prefs.json atomically (temp file + rename), so a reader
// never sees a partially-written file.
func SavePrefs(p Prefs) error {
	path, err := PrefsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (p *Prefs) clamp() {
	if p.IdleMinutes < 1 {
		p.IdleMinutes = 1
	} else if p.IdleMinutes > 1440 {
		p.IdleMinutes = 1440
	}
	if p.DialogTimeoutSeconds < 10 {
		p.DialogTimeoutSeconds = 10
	} else if p.DialogTimeoutSeconds > 86400 {
		p.DialogTimeoutSeconds = 86400
	}
}

// prefsModelProfileKey validates the "general"/"academic"/"coding" suffix of
// a models.* or thinking.* key.
func prefsModelProfileKey(name string) error {
	for _, p := range prefsModelProfiles {
		if string(p) == name {
			return nil
		}
	}
	return fmt.Errorf("unknown prefs profile %q (want general, academic or coding)", name)
}

// validModelRef checks a models.* value: "" (pi's default) or exactly one
// '/' with non-empty sides.
func validModelRef(v string) error {
	if v == "" {
		return nil
	}
	i := strings.IndexByte(v, '/')
	if i <= 0 || i == len(v)-1 || strings.IndexByte(v[i+1:], '/') >= 0 {
		return fmt.Errorf("model %q must be \"\" or \"provider/id\"", v)
	}
	return nil
}

// Set applies one dotted-key update in place, validating both the key and
// the value against the same set LoadPrefs/SavePrefs round-trip.
func (p *Prefs) Set(key, value string) error {
	switch {
	case key == "defaultProfile":
		if value != string(General) && value != string(Academic) {
			return fmt.Errorf("defaultProfile must be general or academic, not %q", value)
		}
		p.DefaultProfile = value
	case strings.HasPrefix(key, "models."):
		prof := strings.TrimPrefix(key, "models.")
		if err := prefsModelProfileKey(prof); err != nil {
			return err
		}
		if err := validModelRef(value); err != nil {
			return err
		}
		if p.Models == nil {
			p.Models = map[string]string{}
		}
		p.Models[prof] = value
	case strings.HasPrefix(key, "thinking."):
		prof := strings.TrimPrefix(key, "thinking.")
		if err := prefsModelProfileKey(prof); err != nil {
			return err
		}
		if !prefsValidThinking[value] {
			return fmt.Errorf("thinking level %q must be one of off, minimal, low, medium, high, xhigh, max or \"\"", value)
		}
		if p.Thinking == nil {
			p.Thinking = map[string]string{}
		}
		p.Thinking[prof] = value
	case key == "idleMinutes":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("idleMinutes must be an integer 1..1440: %w", err)
		}
		p.IdleMinutes = n
	case key == "dialogTimeoutSeconds":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("dialogTimeoutSeconds must be an integer 10..86400: %w", err)
		}
		p.DialogTimeoutSeconds = n
	case key == "scheduler.enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("scheduler.enabled must be true or false, not %q", value)
		}
		p.Scheduler.Enabled = b
	case key == "scheduler.dailyCap":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 0 {
			return fmt.Errorf("scheduler.dailyCap must be a number >= 0, not %q", value)
		}
		p.Scheduler.DailyCap = f
	default:
		return fmt.Errorf("unknown prefs key %q (want %s)", key, prefsKeyList)
	}
	p.clamp()
	return nil
}

// Get reads one dotted-key value, the same key space as Set.
func (p Prefs) Get(key string) (any, error) {
	switch {
	case key == "defaultProfile":
		return p.DefaultProfile, nil
	case strings.HasPrefix(key, "models."):
		prof := strings.TrimPrefix(key, "models.")
		if err := prefsModelProfileKey(prof); err != nil {
			return nil, err
		}
		return p.Models[prof], nil
	case strings.HasPrefix(key, "thinking."):
		prof := strings.TrimPrefix(key, "thinking.")
		if err := prefsModelProfileKey(prof); err != nil {
			return nil, err
		}
		return p.Thinking[prof], nil
	case key == "idleMinutes":
		return p.IdleMinutes, nil
	case key == "dialogTimeoutSeconds":
		return p.DialogTimeoutSeconds, nil
	case key == "scheduler.enabled":
		return p.Scheduler.Enabled, nil
	case key == "scheduler.dailyCap":
		return p.Scheduler.DailyCap, nil
	default:
		return nil, fmt.Errorf("unknown prefs key %q (want %s)", key, prefsKeyList)
	}
}

// ModelFor is the model override for profile, "" meaning pi's own default.
func (p Prefs) ModelFor(profile Profile) string { return p.Models[string(profile)] }

// ThinkingFor is the thinking-level override for profile, "" meaning pi's
// own default.
func (p Prefs) ThinkingFor(profile Profile) string { return p.Thinking[string(profile)] }
