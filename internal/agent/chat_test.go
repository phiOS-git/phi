package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSession drops a minimal pi session JSONL plus phi sidecar into dir,
// backdated by age so ordering tests are deterministic.
func writeFakeSession(t *testing.T, dir, id string, age time.Duration, sidecar Sidecar) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(dir, "20260101-000000_"+id+".jsonl")
	line := `{"type":"message","id":"a1","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"hello there","timestamp":1}}` + "\n"
	if err := os.WriteFile(jsonl, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar.ID = id
	if err := WriteSidecar(dir, sidecar); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(jsonl, mt, mt); err != nil {
		t.Fatal(err)
	}
	return jsonl
}

func TestChatListOrdering(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("study", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	dir, err := m.SessionsDir("study")
	if err != nil {
		t.Fatal(err)
	}

	writeFakeSession(t, dir, "old", 2*time.Hour, Sidecar{Profile: "general", Project: "study"})
	writeFakeSession(t, dir, "new", time.Minute, Sidecar{Profile: "general", Project: "study"})
	writeFakeSession(t, dir, "pinned-old", 3*time.Hour, Sidecar{Profile: "general", Project: "study", Pinned: true})

	list, err := m.ListTranscripts("study", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("list = %+v", list)
	}
	// Pinned first, regardless of age; then newest first among the rest.
	if list[0].ID != "pinned-old" {
		t.Errorf("list[0] = %q, want pinned-old", list[0].ID)
	}
	if list[1].ID != "new" || list[2].ID != "old" {
		t.Errorf("unpinned order = [%s %s], want [new old]", list[1].ID, list[2].ID)
	}
}

func TestChatTitleResolution(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	dir := m.sessionsDirSystem()

	// 1. Explicit sidecar title wins.
	writeFakeSession(t, dir, "titled", 0, Sidecar{Profile: "general", Title: "Custom Title"})
	// 2. No sidecar title: falls back to the first user message, truncated.
	writeFakeSession(t, dir, "untitled", 0, Sidecar{Profile: "general"})
	// 3. No user message and no title at all: falls back to the id.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(dir, "20260101-000000_bare.jsonl")
	if err := os.WriteFile(bare, []byte(`{"type":"usage","id":"u1","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","kind":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := m.ListTranscripts("", true)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]TranscriptMeta{}
	for _, tr := range list {
		byID[tr.ID] = tr
	}
	if byID["titled"].Title != "Custom Title" {
		t.Errorf("titled.Title = %q", byID["titled"].Title)
	}
	if byID["untitled"].Title != "hello there" {
		t.Errorf("untitled.Title = %q, want the first user message", byID["untitled"].Title)
	}
	if byID["bare"].Title != "bare" {
		t.Errorf("bare.Title = %q, want the id", byID["bare"].Title)
	}
}

func TestChatPinTitleDelete(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	dir := m.sessionsDirSystem()
	jsonl := writeFakeSession(t, dir, "s1", 0, Sidecar{Profile: "general"})

	if err := m.SetChatPinned("s1", true); err != nil {
		t.Fatal(err)
	}
	_, _, sc, err := m.FindTranscript("s1")
	if err != nil || !sc.Pinned {
		t.Fatalf("pinned = %v, err %v", sc.Pinned, err)
	}

	if err := m.SetChatTitle("s1", "  Renamed  "); err != nil {
		t.Fatal(err)
	}
	meta, _, err := m.LoadTranscript("s1")
	if err != nil || meta.Title != "Renamed" {
		t.Fatalf("title = %q, err %v", meta.Title, err)
	}

	if err := m.DeleteChat("s1"); err != nil {
		t.Fatal(err)
	}
	if fileExists(jsonl) {
		t.Error("jsonl not deleted")
	}
	if _, _, _, err := m.FindTranscript("s1"); err == nil {
		t.Error("FindTranscript should fail after delete")
	}
}
