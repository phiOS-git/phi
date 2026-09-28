package agent

import (
	"os"
	"sort"
	"time"
)

// The coding-session row the shell panel's overview polls (plan §7's coding
// list): one entry per `phi agent code`/`tui` terminal session, combining
// session.go's process-level record with timeline.go's transcript-derived
// state. Building it is read-only and re-derived on every call — nothing
// here is persisted beyond what session.go and pi's own JSONL already keep.

// PlanCount is a plan tool call's step progress, or nil on CodingRow when
// the session has made no plan call at all (PlanProgress's ok==false).
type PlanCount struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// CodingRow is one terminal session as the panel's coding list shows it.
type CodingRow struct {
	ID      string `json:"id"`
	Dir     string `json:"dir"`
	Project string `json:"project"`

	Status string `json:"status"` // "active" | "ended" (SessionRecord.Status, verbatim)
	State  string `json:"state"`  // "working" | "waiting" | "idle" | "ended"

	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended"`

	WindowAddr string `json:"windowAddr"`
	Activity   string `json:"activity"`
	Title      string `json:"title"`

	Plan  *PlanCount `json:"plan"`
	Stats Stats      `json:"stats"`

	// transcriptPath is not part of the wire shape; SnapshotCoding needs it
	// to stat the file without re-deriving it from the id.
	transcriptPath string
}

// CodingRows lists every terminal session as a CodingRow, in ListSessions's
// own order (active sessions first, then newest-started). now is threaded
// through explicitly (rather than each row calling time.Now()) so a caller
// polling on an interval derives every row's "working" cutoff from the same
// instant.
func CodingRows(now time.Time) ([]CodingRow, error) {
	recs, err := ListSessions()
	if err != nil {
		return nil, err
	}
	rows := make([]CodingRow, len(recs))
	for i, rec := range recs {
		rows[i] = CodingRowFor(rec, now)
	}
	return rows, nil
}

// CodingRowFor derives one CodingRow from a session record. A transcript
// that is missing or fails to load leaves Stats/Activity/Plan/Title at their
// zero value and State at "idle" (or "ended", if the record already says
// so) — a session phi has just launched, before pi has written its first
// line, is not an error.
func CodingRowFor(rec SessionRecord, now time.Time) CodingRow {
	row := CodingRow{
		ID: rec.ID, Dir: rec.Dir, Project: rec.Project,
		Status: rec.Status, State: "idle",
		Started: rec.Started, WindowAddr: rec.WindowAddr,
		transcriptPath: rec.TranscriptPath,
	}
	if rec.Status == "ended" {
		row.State = "ended"
	}
	if !rec.Ended.IsZero() {
		ended := rec.Ended
		row.Ended = &ended
	}

	if rec.TranscriptPath == "" {
		return row
	}
	fi, err := os.Stat(rec.TranscriptPath)
	if err != nil {
		return row
	}
	tl, entries, err := LoadTimelineFile(rec.TranscriptPath)
	if err != nil {
		return row
	}

	row.Stats = ComputeStats(entries, tl)
	row.Title = truncate60(firstUserText(tl))
	if done, total, ok := PlanProgress(tl); ok {
		row.Plan = &PlanCount{Done: done, Total: total}
	}
	if rec.Status == "ended" {
		return row
	}
	row.Activity = ActivityFromTimeline(tl)

	switch {
	case now.Sub(fi.ModTime()) <= 10*time.Second:
		row.State = "working"
	case lastAssistantItem(tl) != nil && lastAssistantItem(tl).StopReason == "stop":
		row.State = "waiting"
	default:
		row.State = "idle"
	}
	return row
}

// firstUserText returns the first user item's text on the active branch, ""
// if the branch has none — the source for CodingRow.Title, mirroring §2's
// title fallback (first user message) at the session-list level.
func firstUserText(t Timeline) string {
	for _, it := range t.Items {
		if it.Kind == "user" {
			return it.Text
		}
	}
	return ""
}

// CodingSnap is the cheap-to-compute part of a CodingRow a poller diffs
// against its previous snapshot to decide which rows changed, without
// re-deriving the full Timeline for every session on every tick.
type CodingSnap struct {
	Size    int64
	ModTime time.Time
	State   string
}

// SnapshotCoding captures active rows only — an ended session's transcript
// never changes again, so there is nothing for a poller to watch there.
func SnapshotCoding(rows []CodingRow) map[string]CodingSnap {
	snap := make(map[string]CodingSnap, len(rows))
	for _, r := range rows {
		if r.Status != "active" {
			continue
		}
		var size int64
		var modTime time.Time
		if r.transcriptPath != "" {
			if fi, err := os.Stat(r.transcriptPath); err == nil {
				size = fi.Size()
				modTime = fi.ModTime()
			}
		}
		snap[r.ID] = CodingSnap{Size: size, ModTime: modTime, State: r.State}
	}
	return snap
}

// ChangedCoding reports the ids that appeared, disappeared or changed
// between two SnapshotCoding calls, sorted for a deterministic diff.
func ChangedCoding(prev, next map[string]CodingSnap) []string {
	ids := map[string]bool{}
	for id, n := range next {
		if p, ok := prev[id]; !ok || p != n {
			ids[id] = true
		}
	}
	for id := range prev {
		if _, ok := next[id]; !ok {
			ids[id] = true
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// CodingTimeline loads one session's timeline by id, for the panel's
// session-detail view. It tries the terminal record's own TranscriptPath
// first; a session started before pi named its file (session.go's
// ListSessions doc) has none recorded yet, so it falls back to the data
// model's own lookup. Neither locating a transcript is itself an error — a
// session pi has not written to yet simply has an empty timeline.
func CodingTimeline(id string) (Timeline, error) {
	rec, err := GetSession(id)
	if err != nil {
		return Timeline{}, err
	}
	path := rec.TranscriptPath
	if path == "" {
		if m, merr := OpenModel(); merr == nil {
			if _, jsonlPath, _, ferr := m.FindTranscript(id); ferr == nil {
				path = jsonlPath
			}
		}
	}
	if path == "" {
		return Timeline{Items: []Item{}}, nil
	}
	tl, _, err := LoadTimelineFile(path)
	if err != nil {
		return Timeline{}, err
	}
	return tl, nil
}
