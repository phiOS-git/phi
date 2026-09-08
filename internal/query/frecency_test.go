package query

import (
	"path/filepath"
	"testing"
	"time"
)

// newTestFrecency backs a Frecency with a temp file so tests never touch a
// real $XDG_STATE_HOME, and pins the clock so decay math is deterministic.
func newTestFrecency(t *testing.T) *Frecency {
	t.Helper()
	path := filepath.Join(t.TempDir(), "frecency.json")
	f, err := LoadFrecency(path)
	if err != nil {
		t.Fatalf("LoadFrecency: %v", err)
	}
	fixed := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return fixed }
	return f
}

func TestFrecencyUnknownIDScoresZero(t *testing.T) {
	f := newTestFrecency(t)
	if got := f.Score("never-used"); got != 0 {
		t.Errorf("Score(never-used) = %v, want 0", got)
	}
}

func TestFrecencyRecordIncreasesScore(t *testing.T) {
	f := newTestFrecency(t)
	before := f.Score("app:firefox")
	if err := f.Record("app:firefox"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	after := f.Score("app:firefox")
	if !(after > before) {
		t.Errorf("Score after Record = %v, want > %v", after, before)
	}
}

func TestFrecencyPersistsAcrossLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frecency.json")
	f1, err := LoadFrecency(path)
	if err != nil {
		t.Fatalf("LoadFrecency: %v", err)
	}
	if err := f1.Record("app:kitty"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	f2, err := LoadFrecency(path)
	if err != nil {
		t.Fatalf("second LoadFrecency: %v", err)
	}
	if got := f2.Score("app:kitty"); got <= 0 {
		t.Errorf("Score after reload = %v, want > 0", got)
	}
}

func TestFrecencyDecaysWithAge(t *testing.T) {
	f := newTestFrecency(t)
	fixed := f.now()
	if err := f.Record("app:old"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	recentScore := f.Score("app:old")

	// Jump the clock forward by several half-lives.
	f.now = func() time.Time { return fixed.Add(time.Duration(4 * frecencyHalfLife * float64(time.Second))) }
	oldScore := f.Score("app:old")

	if !(oldScore < recentScore) {
		t.Errorf("Score did not decay: recent=%v old=%v", recentScore, oldScore)
	}
	if oldScore < 0 {
		t.Errorf("Score went negative: %v", oldScore)
	}
}

func TestFrecencyFrequencySaturates(t *testing.T) {
	f := newTestFrecency(t)
	for i := 0; i < 50; i++ {
		if err := f.Record("app:heavy"); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	if got := f.Score("app:heavy"); got > 1.0001 {
		t.Errorf("Score = %v, want capped near 1.0 after saturation", got)
	}
}
