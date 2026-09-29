package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCodingTranscript writes a minimal two-line session JSONL: a header
// and one user+assistant exchange. stopReason becomes the assistant
// message's own field, since that is the only thing CodingRowFor's
// waiting/idle split reads off the last turn.
func writeCodingTranscript(t *testing.T, path, stopReason string) {
	t.Helper()
	lines := []string{
		`{"type":"session","version":3,"id":"11111111-1111-1111-1111-111111111111","timestamp":"2024-01-01T00:00:00Z","cwd":"/proj"}`,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2024-01-01T00:00:01Z","message":{"role":"user","content":"hello there, please help","timestamp":1700000000000}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2024-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"stopReason":"` + stopReason + `","timestamp":1700000001000}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestCodingRowForStates(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-1 * time.Hour)

	// working: an active session whose transcript was just touched.
	workingPath := filepath.Join(dir, "working.jsonl")
	writeCodingTranscript(t, workingPath, "tool_use")
	idWorking := NewSessionID()
	if err := RecordSessionStart(SessionRecord{ID: idWorking, Dir: "/proj", TranscriptPath: workingPath, PID: 0}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(workingPath, now, now); err != nil {
		t.Fatal(err)
	}
	recWorking, err := GetSession(idWorking)
	if err != nil {
		t.Fatal(err)
	}
	row := CodingRowFor(recWorking, now)
	if row.Status != "active" || row.State != "working" {
		t.Errorf("working: status=%q state=%q", row.Status, row.State)
	}

	// waiting: stale mtime, last assistant turn stopped for the user.
	waitingPath := filepath.Join(dir, "waiting.jsonl")
	writeCodingTranscript(t, waitingPath, "stop")
	idWaiting := NewSessionID() + "-w"
	if err := RecordSessionStart(SessionRecord{ID: idWaiting, Dir: "/proj", TranscriptPath: waitingPath, PID: 0}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(waitingPath, old, old); err != nil {
		t.Fatal(err)
	}
	recWaiting, err := GetSession(idWaiting)
	if err != nil {
		t.Fatal(err)
	}
	row = CodingRowFor(recWaiting, now)
	if row.State != "waiting" {
		t.Errorf("waiting: state = %q", row.State)
	}
	if row.Activity != "" {
		t.Errorf("waiting: activity = %q, want empty (no trailing tool call)", row.Activity)
	}

	// idle: stale mtime, last turn stopped for a reason other than "stop".
	idlePath := filepath.Join(dir, "idle.jsonl")
	writeCodingTranscript(t, idlePath, "max_tokens")
	idIdle := NewSessionID() + "-i"
	if err := RecordSessionStart(SessionRecord{ID: idIdle, Dir: "/proj", TranscriptPath: idlePath, PID: 0}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(idlePath, old, old); err != nil {
		t.Fatal(err)
	}
	recIdle, err := GetSession(idIdle)
	if err != nil {
		t.Fatal(err)
	}
	row = CodingRowFor(recIdle, now)
	if row.State != "idle" {
		t.Errorf("idle: state = %q", row.State)
	}
	if row.Title != "hello there, please help" {
		t.Errorf("idle: title = %q", row.Title)
	}

	// ended: RecordSessionEnd forces "ended" regardless of transcript
	// content, and Activity is never computed for an ended session.
	if err := RecordSessionEnd(idIdle, "done"); err != nil {
		t.Fatal(err)
	}
	recEnded, err := GetSession(idIdle)
	if err != nil {
		t.Fatal(err)
	}
	row = CodingRowFor(recEnded, now)
	if row.Status != "ended" || row.State != "ended" {
		t.Errorf("ended: status=%q state=%q", row.Status, row.State)
	}
	if row.Ended == nil {
		t.Errorf("ended: Ended = nil, want the recorded end time")
	}
	if row.Activity != "" {
		t.Errorf("ended: activity = %q, want empty", row.Activity)
	}

	// No transcript at all: still valid, just the zero value beyond
	// status/state — never an error, since a just-launched session has not
	// had pi write a line yet.
	idBare := NewSessionID() + "-bare"
	if err := RecordSessionStart(SessionRecord{ID: idBare, Dir: "/proj", PID: 0}); err != nil {
		t.Fatal(err)
	}
	recBare, err := GetSession(idBare)
	if err != nil {
		t.Fatal(err)
	}
	row = CodingRowFor(recBare, now)
	if row.State != "idle" || row.Title != "" || row.Plan != nil {
		t.Errorf("bare: state=%q title=%q plan=%v", row.State, row.Title, row.Plan)
	}
}

func TestCodingRowsAndSnapshotChanges(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	now := time.Now()

	path := filepath.Join(dir, "s.jsonl")
	writeCodingTranscript(t, path, "stop")
	id := NewSessionID()
	if err := RecordSessionStart(SessionRecord{ID: id, Dir: "/proj", TranscriptPath: path, PID: 0}); err != nil {
		t.Fatal(err)
	}

	rows, err := CodingRows(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("CodingRows = %+v", rows)
	}

	snap1 := SnapshotCoding(rows)
	if _, ok := snap1[id]; !ok {
		t.Fatalf("SnapshotCoding missing active row %q", id)
	}
	if unchanged := ChangedCoding(snap1, snap1); len(unchanged) != 0 {
		t.Errorf("ChangedCoding(x, x) = %v, want none", unchanged)
	}

	// Append a line so the transcript's size (and mtime) move; the diff
	// must pick that up as a changed row.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"message","id":"a2","parentId":"a1","timestamp":"2024-01-01T00:00:03Z","message":{"role":"user","content":"more","timestamp":1700000003000}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rows2, err := CodingRows(now)
	if err != nil {
		t.Fatal(err)
	}
	snap2 := SnapshotCoding(rows2)
	changed := ChangedCoding(snap1, snap2)
	if len(changed) != 1 || changed[0] != id {
		t.Fatalf("ChangedCoding after append = %v, want [%s]", changed, id)
	}

	// An ended session is dropped from the snapshot entirely (its
	// transcript will never change again).
	if err := RecordSessionEnd(id, ""); err != nil {
		t.Fatal(err)
	}
	rows3, err := CodingRows(now)
	if err != nil {
		t.Fatal(err)
	}
	snap3 := SnapshotCoding(rows3)
	if _, ok := snap3[id]; ok {
		t.Errorf("SnapshotCoding kept an ended row")
	}
}

func TestCodingTimeline(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()

	path := filepath.Join(dir, "t.jsonl")
	writeCodingTranscript(t, path, "stop")
	id := NewSessionID()
	if err := RecordSessionStart(SessionRecord{ID: id, Dir: "/proj", TranscriptPath: path, PID: 0}); err != nil {
		t.Fatal(err)
	}
	tl, err := CodingTimeline(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Items) == 0 {
		t.Errorf("CodingTimeline: no items from a real transcript")
	}

	// Recorded, but pi has not written to it and the data model has no
	// matching session either: CodingTimeline degrades to an empty
	// timeline rather than an error.
	idBare := NewSessionID() + "-bare"
	if err := RecordSessionStart(SessionRecord{ID: idBare, Dir: "/proj", PID: 0}); err != nil {
		t.Fatal(err)
	}
	tl2, err := CodingTimeline(idBare)
	if err != nil {
		t.Fatal(err)
	}
	if tl2.Items == nil || len(tl2.Items) != 0 {
		t.Errorf("CodingTimeline (no transcript) = %+v, want empty Items", tl2)
	}
}

// TestCodingRowsCachesEnrichment: listing twice with no transcript change
// parses each transcript once — the watcher lists every two seconds.
func TestCodingRowsCachesEnrichment(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"message","id":"a","parentId":null,"timestamp":"2026-09-28T10:00:00Z","message":{"role":"user","content":"hi","timestamp":1}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordSessionStart(SessionRecord{ID: "cache-1", Dir: "/w", TranscriptPath: path}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	before := codingParses
	if _, err := CodingRows(now); err != nil {
		t.Fatal(err)
	}
	if _, err := ActiveCodingRows(now); err != nil {
		t.Fatal(err)
	}
	if got := codingParses - before; got != 1 {
		t.Fatalf("parses = %d, want 1 (second listing must hit the cache)", got)
	}
	if err := os.WriteFile(path, []byte(`{"type":"message","id":"a","parentId":null,"timestamp":"2026-09-28T10:00:00Z","message":{"role":"user","content":"hello again","timestamp":1}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, _ := CodingRows(now)
	if codingParses-before != 2 || rows[0].Title != "hello again" {
		t.Fatalf("after a change: parses %d, title %q", codingParses-before, rows[0].Title)
	}
}
