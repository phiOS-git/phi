package query

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// withFakeDesktopEntries points XDG_DATA_HOME/XDG_DATA_DIRS at a fresh temp
// dir containing one real .desktop file, so ApplicationsProvider.Query has
// something deterministic to scan without touching the real machine.
func withFakeDesktopEntries(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	appsDir := filepath.Join(dir, "applications")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[Desktop Entry]\nType=Application\nName=Firefox\nExec=firefox %u\n"
	if err := os.WriteFile(filepath.Join(appsDir, "firefox.desktop"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", dir)
	// desktopEntryDirs() falls back to the real /usr/local/share:/usr/share
	// default when XDG_DATA_DIRS is blank — point it at an empty temp dir
	// instead, so this test only ever sees the one fake entry above, never
	// whatever real .desktop files happen to exist on the machine running it.
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
}

// docs/TODO.md's runner-bar prefix feature: without stripping "app ",
// matchWeight("Firefox", "app firefox") is not a subsequence match at all
// (no 'a' anywhere in "Firefox") and Rank would drop the entry entirely —
// the same failure class TimerProvider's own regression test guards
// against for a different provider.
func TestApplicationsProviderAppPrefixMatches(t *testing.T) {
	withFakeDesktopEntries(t)
	r := ApplicationsProvider{}.Query(context.Background(), "app firefox")
	if len(r) != 1 || r[0].Title != "Firefox" {
		t.Fatalf("Query(%q) = %v, want the Firefox entry", "app firefox", r)
	}
	if r[0].Score <= 0 {
		t.Errorf("Score = %v, want an explicit positive score once prefixed", r[0].Score)
	}
	ranked := Rank(r, "app firefox", nil)
	if len(ranked) != 1 {
		t.Fatalf("Rank(%v, %q) = %v, want the entry to survive ranking", r, "app firefox", ranked)
	}
}

func TestApplicationsProviderAppPrefixExcludesNonMatch(t *testing.T) {
	withFakeDesktopEntries(t)
	if r := (ApplicationsProvider{}).Query(context.Background(), "app zzz-no-such-app"); r != nil {
		t.Errorf("Query(%q) = %v, want nil for a name that matches nothing", "app zzz-no-such-app", r)
	}
}

func TestApplicationsProviderUnprefixedPathUnchanged(t *testing.T) {
	// The existing behavior every other query relies on: every entry
	// returned with Score at its zero default, letting Rank's own
	// matchWeight against the raw (unstripped) query decide.
	withFakeDesktopEntries(t)
	r := ApplicationsProvider{}.Query(context.Background(), "firefox")
	if len(r) != 1 || r[0].Score != 0 {
		t.Fatalf("Query(%q) = %v, want one entry with Score 0 (Rank decides)", "firefox", r)
	}
}

func TestParseDesktopEntry(t *testing.T) {
	content := `[Desktop Entry]
Type=Application
Name=Firefox
Name[it]=Navigatore
Exec=firefox %u
NoDisplay=false
`
	e, ok := parseDesktopEntry(content)
	if !ok {
		t.Fatal("expected a match")
	}
	if e.name != "Firefox" {
		t.Errorf("name = %q, want Firefox (localized Name[it] must not win)", e.name)
	}
	if e.execClean != "firefox" {
		t.Errorf("execClean = %q, want %q (field code stripped)", e.execClean, "firefox")
	}
	if e.noDisplay {
		t.Error("noDisplay should be false")
	}
	if !e.isApplication {
		t.Error("isApplication should be true")
	}
}

func TestParseDesktopEntryRejectsNoDisplay(t *testing.T) {
	content := "[Desktop Entry]\nType=Application\nName=Hidden\nExec=hidden\nNoDisplay=true\n"
	e, ok := parseDesktopEntry(content)
	if !ok {
		t.Fatal("expected a structural match even when NoDisplay=true — the caller filters it")
	}
	if !e.noDisplay {
		t.Error("noDisplay should be true")
	}
}

func TestParseDesktopEntryRejectsNonApplication(t *testing.T) {
	content := "[Desktop Entry]\nType=Link\nName=A Link\nExec=xdg-open https://example.com\n"
	e, _ := parseDesktopEntry(content)
	if e.isApplication {
		t.Error("Type=Link must not be read as an application")
	}
}

func TestParseDesktopEntryRejectsMissingExec(t *testing.T) {
	content := "[Desktop Entry]\nType=Application\nName=No Exec Here\n"
	if _, ok := parseDesktopEntry(content); ok {
		t.Error("an entry with no Exec= must not match")
	}
}

func TestParseDesktopEntryIgnoresOtherSections(t *testing.T) {
	content := `[Desktop Action new-window]
Name=New Window
Exec=firefox --new-window

[Desktop Entry]
Type=Application
Name=Firefox
Exec=firefox
`
	e, ok := parseDesktopEntry(content)
	if !ok {
		t.Fatal("expected a match")
	}
	if e.name != "Firefox" || e.execClean != "firefox" {
		t.Errorf("a value from [Desktop Action ...] leaked into the result: %+v", e)
	}
}

func TestCleanExecString(t *testing.T) {
	cases := map[string]string{
		"firefox %u":           "firefox",
		"code --new-window %F": "code --new-window",
		"kitty -e yazi":        "kitty -e yazi",
	}
	for in, want := range cases {
		if got := cleanExecString(in); got != want {
			t.Errorf("cleanExecString(%q) = %q, want %q", in, got, want)
		}
	}
}
