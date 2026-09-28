package agent

import (
	"os"
	"testing"
)

func TestSessionStore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// ListSessions now looks up transcripts lazily via a Model (session.go),
	// so it needs a sandboxed data root too — otherwise it would touch the
	// real ~/.local/share/phi-agent on a machine with no XDG_DATA_HOME set.
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	id := NewSessionID()
	if err := RecordSessionStart(SessionRecord{ID: id, Profile: "coding", Dir: "/home/x/proj", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	got, err := GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" || got.Dir != "/home/x/proj" || got.Profile != "coding" {
		t.Errorf("record = %+v", got)
	}

	list, _ := ListSessions()
	if len(list) != 1 {
		t.Fatalf("ListSessions = %v", list)
	}

	if err := RecordSessionEnd(id, "done"); err != nil {
		t.Fatal(err)
	}
	got, _ = GetSession(id)
	if got.Status != "ended" || got.ExitNote != "done" || got.Ended.IsZero() {
		t.Errorf("after end: %+v", got)
	}

	// A record for a dead pid is reconciled to ended on read.
	dead := NewSessionID() + "-x"
	_ = RecordSessionStart(SessionRecord{ID: dead, Dir: "/tmp/z", PID: 999999})
	list, _ = ListSessions()
	for _, r := range list {
		if r.ID == dead && r.Status != "ended" {
			t.Errorf("dead-pid session not reconciled: %+v", r)
		}
	}
}

func TestNewSessionIDFormat(t *testing.T) {
	id := NewSessionID()
	// YYYYMMDD-HHMMSS-xxxxxx: 15 + 1 + 6 = 22 chars, two hyphens.
	if len(id) != 22 {
		t.Fatalf("NewSessionID() = %q, want 22 chars", id)
	}
	if err := checkSegment(id); err != nil {
		t.Errorf("NewSessionID() not a valid filename segment: %v", err)
	}
}
