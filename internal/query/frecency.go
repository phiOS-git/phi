package query

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"

	"phi/internal/state"
)

// Frecency stores selection history as one JSON file at
// $XDG_STATE_HOME/phi/frecency.json. Unlike phi's state scalars (theme.variant,
// toggles), this is a collection with one writer (phi query) and needs no
// defined-keys validation — launcher frecency, like notification and clipboard
// history, is not part of the state key set.
type frecencyEntry struct {
	Count    int   `json:"count"`
	LastUsed int64 `json:"lastUsed"` // unix seconds
}

type Frecency struct {
	path    string
	entries map[string]frecencyEntry
	now     func() time.Time // overridable in tests; time.Now in real use
}

// FrecencyPath is $XDG_STATE_HOME/phi/frecency.json.
func FrecencyPath() (string, error) {
	dir, err := state.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "frecency.json"), nil
}

// LoadFrecency reads path, or starts empty if it does not exist yet — the
// initial state of every id, same as `phi state`'s own Get on an unset key.
func LoadFrecency(path string) (*Frecency, error) {
	f := &Frecency{path: path, entries: map[string]frecencyEntry{}, now: time.Now}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(data, &f.entries); err != nil {
		return nil, err
	}
	return f, nil
}

// Record marks id as used right now, persisting immediately: `phi query
// record <id>` (internal/cli) is a separate, deliberate invocation the
// shell makes only when the user actually selects a result, never on every
// keystroke the way ranking itself runs.
func (f *Frecency) Record(id string) error {
	e := f.entries[id]
	e.Count++
	e.LastUsed = f.now().Unix()
	f.entries[id] = e
	return f.save()
}

func (f *Frecency) save() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(f.entries)
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, data, 0o644)
}

// frecencyHalfLife is a placeholder, the same way rank.go's tiers are: no
// document fixes a real number, one week is a reasonable starting guess for
// "still feels recent," and this is the one function real usage should
// tune first once the user reports a query that ranked wrong.
const frecencyHalfLife = 7 * 24 * 60 * 60.0 // seconds

// Score is in roughly [0, 1]: highest for an id used often and recently,
// decaying toward 0 as the last use recedes past frecencyHalfLife.
// Frequency saturates at 20 uses so one very common selection cannot keep
// permanently outranking everything else the moment its own recency fades.
func (f *Frecency) Score(id string) float64 {
	e, ok := f.entries[id]
	if !ok {
		return 0
	}
	ageSeconds := float64(f.now().Unix() - e.LastUsed)
	if ageSeconds < 0 {
		ageSeconds = 0
	}
	decay := math.Pow(0.5, ageSeconds/frecencyHalfLife)
	frequency := math.Min(float64(e.Count), 20) / 20.0
	return frequency * decay
}
