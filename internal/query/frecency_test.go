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

func TestRecordResultStoresSnapshot(t *testing.T) {
	f := newTestFrecency(t)
	snap := Result{ID: "app:firefox.desktop", Provider: "application", Title: "Firefox", Score: 9080}
	if err := f.RecordResult("app:firefox.desktop", &snap); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	got := f.Snapshots(map[string]bool{"application": true})
	if len(got) != 1 || got[0].Title != "Firefox" {
		t.Fatalf("Snapshots = %v, want the stored Firefox snapshot", got)
	}
	// The caller's own fully-ranked Score (tier + frecency already folded
	// in) must never be replayed as if it were an unranked provider score.
	if got[0].Score != 0 {
		t.Errorf("stored snapshot Score = %v, want 0 (zeroed on store)", got[0].Score)
	}
}

func TestRecordResultDropsRich(t *testing.T) {
	f := newTestFrecency(t)
	snap := Result{ID: "calc:2+2", Provider: "calculator", Title: "4", Rich: &RichResult{Kind: "value", Headline: "4"}}
	if err := f.RecordResult("calc:2+2", &snap); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	got := f.Snapshots(map[string]bool{"calculator": true})
	if len(got) != 1 || got[0].Rich != nil {
		t.Fatalf("Snapshots = %v, want Rich stripped from the stored snapshot", got)
	}
}

func TestRecordResultSkipsClipboardSnapshot(t *testing.T) {
	f := newTestFrecency(t)
	snap := Result{ID: "clip:123", Provider: "clipboard", Title: "some clipping"}
	if err := f.RecordResult("clip:123", &snap); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	if got := f.Snapshots(map[string]bool{"clipboard": true}); got != nil {
		t.Errorf("Snapshots = %v, want no snapshot stored for a clipboard result", got)
	}
	// The plain count/lastUsed bump must still have happened.
	if got := f.Score("clip:123"); got <= 0 {
		t.Errorf("Score(clip:123) = %v, want > 0 — RecordResult must still bump count/lastUsed", got)
	}
}

func TestRecordResultNilSnapshotKeepsExisting(t *testing.T) {
	f := newTestFrecency(t)
	snap := Result{ID: "app:firefox.desktop", Provider: "application", Title: "Firefox"}
	if err := f.RecordResult("app:firefox.desktop", &snap); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	// The plain one-argument form (Record, equivalent to RecordResult with
	// a nil snapshot) must not erase the snapshot the JSON form built up.
	if err := f.Record("app:firefox.desktop"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := f.Snapshots(map[string]bool{"application": true})
	if len(got) != 1 || got[0].Title != "Firefox" {
		t.Fatalf("Snapshots after a plain Record = %v, want the snapshot untouched", got)
	}
}

func TestRecordResultRefreshesSnapshot(t *testing.T) {
	f := newTestFrecency(t)
	old := Result{ID: "app:firefox.desktop", Provider: "application", Title: "Firefox (old)"}
	if err := f.RecordResult("app:firefox.desktop", &old); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	fresh := Result{ID: "app:firefox.desktop", Provider: "application", Title: "Firefox (new)"}
	if err := f.RecordResult("app:firefox.desktop", &fresh); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	got := f.Snapshots(map[string]bool{"application": true})
	if len(got) != 1 || got[0].Title != "Firefox (new)" {
		t.Fatalf("Snapshots = %v, want the refreshed Title", got)
	}
}

func TestSnapshotsFiltersByProviderAndRanksByFrecency(t *testing.T) {
	f := newTestFrecency(t)
	appSnap := Result{ID: "app:a", Provider: "application", Title: "A"}
	fileSnap := Result{ID: "file:b", Provider: "file", Title: "B"}
	if err := f.RecordResult("app:a", &appSnap); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	if err := f.RecordResult("file:b", &fileSnap); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	got := f.Snapshots(map[string]bool{"application": true})
	if len(got) != 1 || got[0].ID != "app:a" {
		t.Fatalf("Snapshots(application) = %v, want only the application snapshot", got)
	}
}

func TestSnapshotsNilReceiverIsSafe(t *testing.T) {
	var f *Frecency
	if got := f.Snapshots(map[string]bool{"application": true}); got != nil {
		t.Errorf("Snapshots on a nil *Frecency = %v, want nil", got)
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
