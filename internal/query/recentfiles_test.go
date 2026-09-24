package query

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseRecentlyUsedXBEL(t *testing.T) {
	data := `<?xml version="1.0"?>
<xbel version="1.0">
  <bookmark href="file:///home/user/older.txt" added="2024-01-01T10:00:00Z" modified="2024-01-01T10:00:00Z" visited="2024-01-01T10:00:00Z"/>
  <bookmark href="file:///home/user/newer.txt" added="2024-05-01T10:00:00Z" modified="2024-06-01T10:00:00.123456Z" visited="2024-05-01T10:00:00Z"/>
  <bookmark href="https://example.com/not-a-file" modified="2024-07-01T10:00:00Z"/>
  <bookmark href="file:///home/user/older.txt" modified="2024-08-01T10:00:00Z" visited="2024-08-01T10:00:00Z"/>
</xbel>`
	got := parseRecentlyUsedXBEL([]byte(data))
	// "older.txt" appears twice; its second occurrence (2024-08-01) is
	// later than "newer.txt"'s own modified time (2024-06-01), so despite
	// its name it must sort first once the duplicate's later timestamp is
	// taken into account. The non-file:// href must not appear at all.
	want := []string{"/home/user/older.txt", "/home/user/newer.txt"}
	if len(got) != len(want) {
		t.Fatalf("parseRecentlyUsedXBEL = %v, want %v", got, want)
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("parseRecentlyUsedXBEL[%d] = %q, want %q (full: %v)", i, got[i], p, got)
		}
	}
}

func TestParseRecentlyUsedXBELMalformed(t *testing.T) {
	if got := parseRecentlyUsedXBEL([]byte("not xml at all")); got != nil {
		t.Errorf("parseRecentlyUsedXBEL(malformed) = %v, want nil", got)
	}
}

func TestParseRecentlyUsedXBELEmptyDocument(t *testing.T) {
	if got := parseRecentlyUsedXBEL([]byte(`<xbel version="1.0"></xbel>`)); got != nil {
		t.Errorf("parseRecentlyUsedXBEL(empty) = %v, want nil", got)
	}
}

func TestLaterOf(t *testing.T) {
	a := "2024-01-01T10:00:00Z"
	b := "2024-06-01T10:00:00Z"
	if got := laterOf(a, b); !got.Equal(mustParseTime(t, b)) {
		t.Errorf("laterOf(%q, %q) = %v, want %v", a, b, got, b)
	}
	if got := laterOf(b, a); !got.Equal(mustParseTime(t, b)) {
		t.Errorf("laterOf(%q, %q) = %v, want %v", b, a, got, b)
	}
	if got := laterOf("", a); !got.Equal(mustParseTime(t, a)) {
		t.Errorf("laterOf(\"\", %q) = %v, want %v (fall back to the one that parses)", a, got, a)
	}
	if got := laterOf("garbage", "also garbage"); !got.IsZero() {
		t.Errorf("laterOf(garbage, garbage) = %v, want the zero Time", got)
	}
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("time.Parse(%q): %v", s, err)
	}
	return tm
}

func TestParseUserDirs(t *testing.T) {
	data := `# This file is written by xdg-user-dirs-update
# If you want to change or add directories, just edit the line you're
# interested in. All local changes will be retained on the next run.
XDG_DESKTOP_DIR="$HOME/Desktop"
XDG_DOCUMENTS_DIR="$HOME/Documents"
XDG_DOWNLOADS_DIR="$HOME/"
`
	got := parseUserDirs([]byte(data), "/home/user")
	want := map[string]string{
		"XDG_DESKTOP_DIR":   "/home/user/Desktop",
		"XDG_DOCUMENTS_DIR": "/home/user/Documents",
		"XDG_DOWNLOADS_DIR": "/home/user/", // a disabled dir points bare at $HOME — commonDirectories dedupes this
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parseUserDirs[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["XDG_MUSIC_DIR"]; ok {
		t.Errorf("parseUserDirs listed XDG_MUSIC_DIR, which the fixture never set: %v", got)
	}
}

func TestCommonDirectoriesDedupesDisabledDir(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{"Desktop", "Documents"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configHome := t.TempDir()
	// XDG_DOWNLOADS_DIR disabled, pointing bare at $HOME — must not
	// duplicate the home entry already in the list.
	dirsFile := `XDG_DESKTOP_DIR="$HOME/Desktop"
XDG_DOCUMENTS_DIR="$HOME/Documents"
XDG_DOWNLOADS_DIR="$HOME/"
`
	if err := os.WriteFile(filepath.Join(configHome, "user-dirs.dirs"), []byte(dirsFile), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", configHome)

	got := commonDirectories(home)
	want := []string{home, filepath.Join(home, "Desktop"), filepath.Join(home, "Documents")}
	if len(got) != len(want) {
		t.Fatalf("commonDirectories = %v, want %v", got, want)
	}
	for i, d := range want {
		if got[i] != d {
			t.Errorf("commonDirectories[%d] = %q, want %q (full: %v)", i, got[i], d, got)
		}
	}
}

func TestCommonDirectoriesFallsBackWithoutUserDirsFile(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "Music"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Every other fallback subdirectory is deliberately left absent —
	// only directories that actually exist are returned.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	got := commonDirectories(home)
	want := []string{home, filepath.Join(home, "Music")}
	if len(got) != len(want) {
		t.Fatalf("commonDirectories = %v, want %v", got, want)
	}
	for i, d := range want {
		if got[i] != d {
			t.Errorf("commonDirectories[%d] = %q, want %q (full: %v)", i, got[i], d, got)
		}
	}
}

func TestFilesProviderTagDefaults(t *testing.T) {
	home := t.TempDir()
	dataHome := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)

	recentFile := filepath.Join(home, "report.txt")
	if err := os.WriteFile(recentFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A recent entry whose target no longer exists must be skipped.
	goneFile := filepath.Join(home, "gone.txt")

	xbel := `<xbel version="1.0">
  <bookmark href="file://` + recentFile + `" modified="2024-06-01T10:00:00Z" visited="2024-06-01T10:00:00Z"/>
  <bookmark href="file://` + goneFile + `" modified="2024-07-01T10:00:00Z" visited="2024-07-01T10:00:00Z"/>
</xbel>`
	if err := os.WriteFile(filepath.Join(dataHome, "recently-used.xbel"), []byte(xbel), 0o644); err != nil {
		t.Fatal(err)
	}

	got := FilesProvider{}.TagDefaults(context.Background(), "file")
	if len(got) == 0 {
		t.Fatal("TagDefaults(\"file\") = empty, want the recent file plus at least $HOME")
	}
	if got[0].Subtitle != recentFile {
		t.Errorf("TagDefaults(\"file\")[0].Subtitle = %q, want the recent file %q first", got[0].Subtitle, recentFile)
	}
	var sawHome bool
	for _, r := range got {
		if r.Subtitle == filepath.Clean(home) {
			sawHome = true
		}
		if r.Subtitle == goneFile {
			t.Errorf("TagDefaults(\"file\") included a recent entry whose file no longer exists: %+v", r)
		}
	}
	if !sawHome {
		t.Errorf("TagDefaults(\"file\") = %v, want $HOME among the common directories", got)
	}
}
