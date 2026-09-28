package agent

import (
	"os"
	"testing"
)

func TestDefaultPrefs(t *testing.T) {
	p := DefaultPrefs()
	if p.DefaultProfile != "general" {
		t.Errorf("DefaultProfile = %q, want general", p.DefaultProfile)
	}
	if p.Models == nil || p.Thinking == nil {
		t.Error("Models/Thinking must be non-nil empty maps")
	}
	if p.IdleMinutes != 15 || p.DialogTimeoutSeconds != 600 {
		t.Errorf("IdleMinutes/DialogTimeoutSeconds = %d/%d, want 15/600", p.IdleMinutes, p.DialogTimeoutSeconds)
	}
	if p.Scheduler.Enabled || p.Scheduler.DailyCap != 1.0 {
		t.Errorf("Scheduler = %+v, want {false 1}", p.Scheduler)
	}
}

func TestLoadPrefsMissingFileReturnsDefaults(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := LoadPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultProfile != "general" || p.IdleMinutes != 15 {
		t.Errorf("LoadPrefs on a missing file = %+v, want defaults", p)
	}
}

func TestSaveLoadPrefsRoundTripsAndOverlaysPartialFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p := DefaultPrefs()
	if err := p.Set("idleMinutes", "30"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("models.general", "anthropic/claude"); err != nil {
		t.Fatal(err)
	}
	if err := SavePrefs(p); err != nil {
		t.Fatal(err)
	}

	path, err := PrefsPath()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("prefs.json mode = %v, want 0600", fi.Mode().Perm())
	}

	got, err := LoadPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if got.IdleMinutes != 30 {
		t.Errorf("IdleMinutes = %d, want 30", got.IdleMinutes)
	}
	if got.Models["general"] != "anthropic/claude" {
		t.Errorf(`Models["general"] = %q`, got.Models["general"])
	}
	// A field the file didn't touch keeps its default.
	if got.DialogTimeoutSeconds != 600 {
		t.Errorf("DialogTimeoutSeconds = %d, want the default 600", got.DialogTimeoutSeconds)
	}
}

func TestLoadPrefsClampsOutOfRangeDurations(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if err := os.MkdirAll(dir+"/phi-agent", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/phi-agent/prefs.json",
		[]byte(`{"idleMinutes":99999,"dialogTimeoutSeconds":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if p.IdleMinutes != 1440 {
		t.Errorf("IdleMinutes = %d, want clamped to 1440", p.IdleMinutes)
	}
	if p.DialogTimeoutSeconds != 10 {
		t.Errorf("DialogTimeoutSeconds = %d, want clamped to 10", p.DialogTimeoutSeconds)
	}
}

func TestPrefsSetGetValidation(t *testing.T) {
	p := DefaultPrefs()

	cases := []struct {
		key, value string
		wantErr    bool
	}{
		{"defaultProfile", "academic", false},
		{"defaultProfile", "coding", true}, // not a chat profile
		{"models.coding", "", false},
		{"models.coding", "anthropic/claude", false},
		{"models.coding", "no-slash", true},
		{"models.coding", "/missing-provider", true},
		{"models.coding", "missing-id/", true},
		{"models.bogus", "x", true},
		{"thinking.general", "high", false},
		{"thinking.general", "extreme", true},
		{"idleMinutes", "45", false},
		{"idleMinutes", "not-a-number", true},
		{"scheduler.enabled", "true", false},
		{"scheduler.enabled", "sure", true},
		{"scheduler.dailyCap", "2.5", false},
		{"scheduler.dailyCap", "-1", true},
		{"nonsense.key", "x", true},
	}
	for _, c := range cases {
		err := p.Set(c.key, c.value)
		if (err != nil) != c.wantErr {
			t.Errorf("Set(%q, %q) err = %v, wantErr %v", c.key, c.value, err, c.wantErr)
		}
	}

	if err := p.Set("models.general", "anthropic/claude"); err != nil {
		t.Fatal(err)
	}
	got, err := p.Get("models.general")
	if err != nil || got != "anthropic/claude" {
		t.Errorf("Get(models.general) = %v, %v", got, err)
	}
	if _, err := p.Get("nonsense.key"); err == nil {
		t.Error("Get on an unknown key should fail")
	}
}

func TestPrefsModelForThinkingFor(t *testing.T) {
	p := DefaultPrefs()
	if err := p.Set("models.coding", "anthropic/claude"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("thinking.coding", "high"); err != nil {
		t.Fatal(err)
	}
	if p.ModelFor(Coding) != "anthropic/claude" {
		t.Errorf("ModelFor(Coding) = %q", p.ModelFor(Coding))
	}
	if p.ThinkingFor(Coding) != "high" {
		t.Errorf("ThinkingFor(Coding) = %q", p.ThinkingFor(Coding))
	}
	if p.ModelFor(General) != "" {
		t.Errorf("ModelFor(General) = %q, want \"\" (unset)", p.ModelFor(General))
	}
}
