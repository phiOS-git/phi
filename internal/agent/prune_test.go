package agent

import (
	"testing"
	"time"
)

func TestPruneSessionRecords(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	mustWrite := func(id, status string, ended time.Time) {
		t.Helper()
		if err := writeSession(SessionRecord{ID: id, Status: status, Started: now.Add(-48 * time.Hour), Ended: ended}); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("old-ended", "ended", now.Add(-40*time.Hour))   // older than 24h cutoff -> pruned
	mustWrite("recent-ended", "ended", now.Add(-2*time.Hour)) // within cutoff -> kept
	mustWrite("active", "active", time.Time{})                // never ended -> kept regardless of age

	n, err := PruneSessionRecords(24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("removed = %d, want 1", n)
	}

	if _, err := GetSession("old-ended"); err == nil {
		t.Error("old-ended should have been removed")
	}
	if _, err := GetSession("recent-ended"); err != nil {
		t.Errorf("recent-ended should survive: %v", err)
	}
	if _, err := GetSession("active"); err != nil {
		t.Errorf("active should survive: %v", err)
	}
}

func TestPruneSessionRecordsNoDirYet(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	n, err := PruneSessionRecords(24*time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("removed = %d, want 0", n)
	}
}
