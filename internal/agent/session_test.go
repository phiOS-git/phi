package agent

import (
	"os"
	"testing"
)

func TestSessionStore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	id := NewSessionID()
	if err := RecordSessionStart(SessionRecord{ID: id, Dir: "/home/x/proj", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	got, err := GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" || got.Dir != "/home/x/proj" {
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
