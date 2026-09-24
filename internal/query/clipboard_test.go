package query

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// withFakeClipboard points XDG_STATE_HOME at a fresh temp dir and writes
// one text or image entry per fake given, returning the clipboard
// directory itself for tests that also want to write pins.json directly.
type fakeClipEntry struct {
	id      string
	mime    string
	content string // ignored for image/png
}

func withFakeClipboard(t *testing.T, entries ...fakeClipEntry) string {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	dir := filepath.Join(stateHome, "phi", "clipboard")
	entriesDir := filepath.Join(dir, "entries")
	if err := os.MkdirAll(entriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.WriteFile(filepath.Join(entriesDir, e.id+".mime"), []byte(e.mime), 0o644); err != nil {
			t.Fatal(err)
		}
		content := e.content
		if e.mime == "image/png" && content == "" {
			content = "not a real png, content is opaque to this provider"
		}
		if err := os.WriteFile(filepath.Join(entriesDir, e.id+".data"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func nsID(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

func TestStripClipboardKeyword(t *testing.T) {
	cases := []struct {
		q             string
		wantOK        bool
		wantRemainder string
	}{
		{"copy", true, ""},
		{"clip", true, ""},
		{"cp", true, ""},
		{"COPY", true, ""}, // case-insensitive
		{"copy ", true, ""},
		{"copy receipt", true, "receipt"},
		{"clip   receipt", true, "receipt"}, // extra whitespace trimmed
		{"copying", false, ""},              // keyword must be a whole word, not a prefix of a longer one
		{"copier tool", false, ""},
		{"", false, ""},
		{"other text", false, ""},
	}
	for _, c := range cases {
		remainder, ok := stripClipboardKeyword(c.q)
		if ok != c.wantOK || (ok && remainder != c.wantRemainder) {
			t.Errorf("stripClipboardKeyword(%q) = %q, %v, want %q, %v", c.q, remainder, ok, c.wantRemainder, c.wantOK)
		}
	}
}

func TestClipboardProviderNonKeywordYieldsNothing(t *testing.T) {
	withFakeClipboard(t, fakeClipEntry{id: nsID(time.Now()), mime: "text/plain", content: "hello"})
	if got := (ClipboardProvider{}).Query(context.Background(), "firefox"); got != nil {
		t.Errorf("Query(%q) = %v, want nil — no clipboard keyword present", "firefox", got)
	}
}

func TestClipboardProviderPinnedFirstThenNewest(t *testing.T) {
	base := time.Now()
	oldID := nsID(base.Add(-2 * time.Hour))
	midID := nsID(base.Add(-1 * time.Hour))
	newID := nsID(base)
	dir := withFakeClipboard(t,
		fakeClipEntry{id: oldID, mime: "text/plain", content: "oldest entry, pinned"},
		fakeClipEntry{id: midID, mime: "text/plain", content: "middle entry"},
		fakeClipEntry{id: newID, mime: "text/plain", content: "newest entry"},
	)
	if err := os.WriteFile(filepath.Join(dir, "pins.json"), []byte(`["`+oldID+`"]`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := (ClipboardProvider{}).Query(context.Background(), "clip")
	if len(got) != 3 {
		t.Fatalf("Query(\"clip\") = %v, want 3 entries", got)
	}
	wantIDs := []string{"clip:" + oldID, "clip:" + newID, "clip:" + midID}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Fatalf("Query(\"clip\")[%d].ID = %q, want %q (pinned first, then newest) — full: %v", i, got[i].ID, id, got)
		}
	}
	for i, r := range got {
		if r.Score <= 0 {
			t.Errorf("result[%d].Score = %v, want > 0 so Rank never drops it", i, r.Score)
		}
		if i > 0 && r.Score >= got[i-1].Score {
			t.Errorf("Scores are not strictly decreasing: %v", got)
		}
	}
}

func TestClipboardProviderFiltersTextByContent(t *testing.T) {
	base := time.Now()
	withFakeClipboard(t,
		fakeClipEntry{id: nsID(base), mime: "text/plain", content: "the quick brown fox"},
		fakeClipEntry{id: nsID(base.Add(-time.Minute)), mime: "text/plain", content: "lorem ipsum dolor"},
	)
	got := (ClipboardProvider{}).Query(context.Background(), "copy fox")
	if len(got) != 1 || got[0].Title != "the quick brown fox" {
		t.Fatalf("Query(\"copy fox\") = %v, want only the matching entry", got)
	}
}

func TestClipboardProviderImageOnlyMatchesEmptyOrImageWord(t *testing.T) {
	withFakeClipboard(t, fakeClipEntry{id: nsID(time.Now()), mime: "image/png"})

	if got := (ClipboardProvider{}).Query(context.Background(), "copy image"); len(got) != 1 || got[0].Title != "Image" {
		t.Fatalf("Query(\"copy image\") = %v, want the image entry", got)
	}
	if got := (ClipboardProvider{}).Query(context.Background(), "copy"); len(got) != 1 {
		t.Fatalf("Query(\"copy\") (empty remainder) = %v, want the image entry", got)
	}
	if got := (ClipboardProvider{}).Query(context.Background(), "copy receipt"); got != nil {
		t.Errorf("Query(\"copy receipt\") = %v, want nil — an image never matches a text filter", got)
	}
}

func TestClipboardProviderSkipsInvalidMime(t *testing.T) {
	dir := withFakeClipboard(t)
	entriesDir := filepath.Join(dir, "entries")
	id := nsID(time.Now())
	if err := os.WriteFile(filepath.Join(entriesDir, id+".mime"), []byte("application/x-evil; rm -rf /"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entriesDir, id+".data"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (ClipboardProvider{}).Query(context.Background(), "clip"); got != nil {
		t.Errorf("Query(\"clip\") with an invalid .mime = %v, want nil — never trust it into a shell command", got)
	}
}

func TestClipboardProviderCommandIsShellSafe(t *testing.T) {
	dir := withFakeClipboard(t, fakeClipEntry{id: nsID(time.Now()), mime: "text/plain", content: "hi"})
	got := (ClipboardProvider{}).Query(context.Background(), "clip")
	if len(got) != 1 {
		t.Fatalf("Query(\"clip\") = %v, want 1 entry", got)
	}
	wantPathPart := shellQuote(filepath.Join(dir, "entries", got[0].ID[len("clip:"):]+".data"))
	if cmd := got[0].Action.Data["command"]; cmd == "" || !strings.Contains(cmd, "wl-copy --type text/plain") || !strings.Contains(cmd, wantPathPart) {
		t.Errorf("command = %q, want a wl-copy invocation with a shell-quoted path", cmd)
	}
}

func TestClipboardProviderTagDefaultsIsQueryWithBareKeyword(t *testing.T) {
	withFakeClipboard(t, fakeClipEntry{id: nsID(time.Now()), mime: "text/plain", content: "hello"})
	got := (ClipboardProvider{}).TagDefaults(context.Background(), "clip")
	want := (ClipboardProvider{}).Query(context.Background(), "clip")
	if len(got) != len(want) || len(got) != 1 {
		t.Fatalf("TagDefaults(\"clip\") = %v, want the same as Query(\"clip\") = %v", got, want)
	}
}

func TestClipboardProviderCapsAtMax(t *testing.T) {
	base := time.Now()
	var entries []fakeClipEntry
	for i := 0; i < clipboardMaxResults+10; i++ {
		entries = append(entries, fakeClipEntry{
			id: nsID(base.Add(-time.Duration(i) * time.Second)), mime: "text/plain",
			content: fmt.Sprintf("entry %d", i),
		})
	}
	withFakeClipboard(t, entries...)
	got := (ClipboardProvider{}).Query(context.Background(), "clip")
	if len(got) != clipboardMaxResults {
		t.Fatalf("Query(\"clip\") returned %d entries, want the cap of %d", len(got), clipboardMaxResults)
	}
}

func TestLoadClipboardPinsAcceptsStringsAndNumbers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pins.json"), []byte(`["123", 456]`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadClipboardPins(dir)
	if !got["123"] || !got["456"] {
		t.Errorf("loadClipboardPins = %v, want both the string and numeric id accepted", got)
	}
}

func TestLoadClipboardPinsMissingFile(t *testing.T) {
	if got := loadClipboardPins(t.TempDir()); len(got) != 0 {
		t.Errorf("loadClipboardPins(no pins.json) = %v, want empty", got)
	}
}

func TestClipboardTitleFirstLineTrimmed(t *testing.T) {
	if got := clipboardTitle("first line\nsecond line"); got != "first line" {
		t.Errorf("clipboardTitle = %q, want %q", got, "first line")
	}
	if got := clipboardTitle("   "); got != "(empty)" {
		t.Errorf("clipboardTitle(blank) = %q, want %q", got, "(empty)")
	}
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	got := clipboardTitle(long)
	if got == long {
		t.Errorf("clipboardTitle did not truncate a %d-rune line", len(long))
	}
}

func TestClipboardAge(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		age  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{2 * 24 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		id := strconv.FormatInt(now.Add(-c.age).UnixNano(), 10)
		if got := clipboardAge(id, now); got != c.want {
			t.Errorf("clipboardAge(%v ago) = %q, want %q", c.age, got, c.want)
		}
	}
	if got := clipboardAge("not-a-number", now); got != "" {
		t.Errorf("clipboardAge(garbage id) = %q, want empty", got)
	}
}
