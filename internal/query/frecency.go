package query

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"phi/internal/state"
)

// Frecency stores selection history as one JSON file at
// $XDG_STATE_HOME/phi/frecency.json. Unlike phi's state scalars (theme.variant,
// toggles), this is a collection with one writer (phi query) and needs no
// defined-keys validation — launcher frecency, like notification and clipboard
// history, is not part of the state key set.
type frecencyEntry struct {
	Count    int     `json:"count"`
	LastUsed int64   `json:"lastUsed"`         // unix seconds
	Result   *Result `json:"result,omitempty"` // optional snapshot of the selected Result — see RecordResult
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
// keystroke the way ranking itself runs. Equivalent to RecordResult(id, nil).
func (f *Frecency) Record(id string) error {
	return f.RecordResult(id, nil)
}

// RecordResult is Record plus, when snapshot is non-nil, storing or
// refreshing a "common usage" snapshot of the selected Result itself — what
// a locked runner-bar tag with nothing typed yet shows (query.go's
// lockedTagDefaults). A nil snapshot leaves any snapshot already on file
// untouched, so the plain `phi query record <id>` form (no JSON argument)
// keeps working exactly as before rather than erasing history the JSON form
// built up. The stored copy always has Rich stripped (a snapshot only ever
// seeds a plain suggestion row, never a stale calculator card) and Score
// zeroed (the caller sends back its own fully-ranked score — tier and
// frecency already folded in — which must never be replayed as if it were
// an unranked provider score). ClipboardProvider's own entries are
// transient files on disk, not history worth remembering the shape of once
// they're gone, so a "clipboard" snapshot is never stored even when
// offered — its count and lastUsed still bump normally.
func (f *Frecency) RecordResult(id string, snapshot *Result) error {
	e := f.entries[id]
	e.Count++
	e.LastUsed = f.now().Unix()
	if snapshot != nil && snapshot.Provider != "clipboard" {
		clean := *snapshot
		clean.ID = id
		clean.Rich = nil
		clean.Score = 0
		e.Result = &clean
	}
	f.entries[id] = e
	return f.save()
}

// Snapshots returns every stored Result snapshot (see RecordResult) whose
// Provider is in providers, ranked by frecency score, highest first — the
// "common usage" list for a locked tag with nothing typed yet
// (query.go's lockedTagDefaults). Safe to call on a nil *Frecency (the same
// tolerance loadFrecencyOrNil's callers already rely on for Score), always
// returning nil rather than panicking.
func (f *Frecency) Snapshots(providers map[string]bool) []Result {
	if f == nil {
		return nil
	}
	type candidate struct {
		id     string
		result Result
		score  float64
	}
	var candidates []candidate
	for id, e := range f.entries {
		if e.Result == nil || !providers[e.Result.Provider] {
			continue
		}
		candidates = append(candidates, candidate{id: id, result: *e.Result, score: f.Score(id)})
	}
	// map iteration order is random; break ties on id so the result is
	// deterministic across runs rather than at the mercy of that.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].id < candidates[j].id
	})
	if len(candidates) == 0 {
		return nil
	}
	out := make([]Result, len(candidates))
	for i, c := range candidates {
		out[i] = c.result
	}
	return out
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
	if f == nil {
		return 0
	}
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
