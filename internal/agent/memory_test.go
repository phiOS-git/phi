package agent

import (
	"path/filepath"
	"testing"
)

func TestMemLevelDirPaths(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("p", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		level MemLevel
		want  string
	}{
		{SystemLevel(), m.root},
		{ProfileLevel(General), filepath.Join(m.root, "profiles", "general")},
		{ProfileLevel(Academic), filepath.Join(m.root, "profiles", "academic")},
		{ProfileLevel(Coding), filepath.Join(m.root, "profiles", "coding")},
		{ProjectLevel("p"), filepath.Join(m.root, "projects", "p")},
	}
	for _, c := range cases {
		got, err := m.levelDir(c.level)
		if err != nil {
			t.Errorf("%s: %v", c.level, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: dir = %q, want %q", c.level, got, c.want)
		}
	}

	// inline has no memory level.
	if _, err := m.levelDir(ProfileLevel(Inline)); err == nil {
		t.Error("inline profile should have no memory level")
	}
	if _, err := ParseMemLevel("profile", "inline", ""); err == nil {
		t.Error("ParseMemLevel(profile, inline) should be rejected")
	}
	if _, err := ParseMemLevel("profile", "", ""); err == nil {
		t.Error("ParseMemLevel(profile, \"\") should be rejected")
	}
	if _, err := ParseMemLevel("project", "", ""); err == nil {
		t.Error("ParseMemLevel(project, \"\") should be rejected (no active project any more)")
	}
}

func TestAllProposalsKeys(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("study", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}

	all, err := m.AllProposals()
	if err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"system", "profile:general", "profile:academic", "profile:coding", "project:study"}
	for _, k := range wantKeys {
		props, ok := all[k]
		if !ok {
			t.Errorf("missing key %q in AllProposals", k)
			continue
		}
		if props == nil {
			t.Errorf("key %q has a nil value, want an empty (non-nil) slice", k)
		}
		if len(props) != 0 {
			t.Errorf("key %q = %v, want empty", k, props)
		}
	}
	if len(all) != len(wantKeys) {
		t.Errorf("AllProposals returned %d keys, want %d: %v", len(all), len(wantKeys), all)
	}
}
