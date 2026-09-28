package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PruneSessionRecords deletes terminal-session records (session.go's own
// bookkeeping at terminalDir()/<id>.json) whose Status is "ended" and whose
// Ended time is older than now-olderThan. It never touches a pi transcript —
// those are the actual conversation history and outlive the terminal
// record that once pointed at them.
func PruneSessionRecords(olderThan time.Duration, now time.Time) (int, error) {
	d, err := terminalDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := now.Add(-olderThan)
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec, err := GetSession(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue // a corrupt or unreadable record: leave it, don't guess
		}
		if rec.Status != "ended" || rec.Ended.IsZero() || !rec.Ended.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(d, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
