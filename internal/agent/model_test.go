package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m, err := OpenModel()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestModelEnsureSeedsTwoPersonalities(t *testing.T) {
	m := testModel(t)
	created, err := m.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("Ensure created %d files, want 2 (§8.3): %v", len(created), created)
	}
	ps, _ := m.Personalities()
	if strings.Join(ps, ",") != "general,technical" {
		t.Errorf("personalities = %v", ps)
	}
	// Idempotent: a second Ensure creates nothing and does not overwrite.
	prompt := filepath.Join(m.personalityDir("general"), "prompt.md")
	if err := os.WriteFile(prompt, []byte("EDITED BY USER"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, _ := m.Ensure()
	if len(again) != 0 {
		t.Errorf("second Ensure created %v, want nothing", again)
	}
	got, _ := os.ReadFile(prompt)
	if string(got) != "EDITED BY USER" {
		t.Error("Ensure overwrote a user-edited personality")
	}
}

func TestModelMigratesFlatPersonalities(t *testing.T) {
	m := testModel(t)
	if err := os.MkdirAll(m.personalitaDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	flat := filepath.Join(m.personalitaDir(), "notes.md")
	if err := os.WriteFile(flat, []byte("notes personality"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if fileExists(flat) {
		t.Error("flat personality file should have been migrated away")
	}
	txt, err := m.PersonalityPrompt("notes")
	if err != nil || txt != "notes personality" {
		t.Errorf("migrated prompt = %q, err %v", txt, err)
	}
	ps, _ := m.Personalities()
	if strings.Join(ps, ",") != "general,notes,technical" {
		t.Errorf("personalities after migration = %v", ps)
	}
}

func TestModelProjectLifecycle(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("study", ProjectMeta{Description: "exam prep"}); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("study", ProjectMeta{}); err == nil {
		t.Error("NewProject on an existing project should fail")
	}
	for _, sub := range projectSubdirs {
		if !dirExists(filepath.Join(m.projectDir("study"), sub)) {
			t.Errorf("missing subdir %s", sub)
		}
	}
	if !fileExists(filepath.Join(m.projectDir("study"), "memoria.md")) {
		t.Error("missing memoria.md")
	}
	meta, err := m.LoadProjectMeta("study")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Description != "exam prep" || meta.DefaultPersonality != "general" {
		t.Errorf("meta = %+v", meta)
	}
	// progetto.md is regenerated from project.json.
	prog, _ := m.ProjectInstructions("study")
	if !strings.Contains(prog, "exam prep") {
		t.Errorf("progetto.md did not carry the description:\n%s", prog)
	}

	if err := m.SetActiveProject("nope"); err == nil {
		t.Error("SetActiveProject on a missing project should fail")
	}
	if err := m.SetActiveProject("study"); err != nil {
		t.Fatal(err)
	}
	if a, _ := m.ActiveProject(); a != "study" {
		t.Errorf("active = %q, want study", a)
	}
}

func TestModelInvalidNames(t *testing.T) {
	m := testModel(t)
	for _, bad := range []string{"../evil", "a/b", "Study", "", strings.Repeat("x", 65), ".hidden"} {
		if err := m.NewProject(bad, ProjectMeta{}); err == nil {
			t.Errorf("NewProject(%q) should be rejected", bad)
		}
	}
}

func TestModelProposalNameTraversal(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("p", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	lvl := ProjectLevel("p")
	for _, bad := range []string{"../memoria.md", "a/b", "..", ".", ""} {
		if _, err := m.ProposalText(lvl, bad); err == nil {
			t.Errorf("ProposalText(%q) should be rejected", bad)
		}
		if err := m.AcceptProposal(lvl, bad); err == nil {
			t.Errorf("AcceptProposal(%q) should be rejected", bad)
		}
		if err := m.RejectProposal(lvl, bad); err == nil {
			t.Errorf("RejectProposal(%q) should be rejected", bad)
		}
	}
}

func TestModelAcceptProposalAppendsLiteralPerLevel(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("p", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}

	levels := map[string]MemLevel{
		"system":      SystemLevel(),
		"personality": PersonalityLevel("general"),
		"project":     ProjectLevel("p"),
	}
	for label, lvl := range levels {
		dir, err := m.proposteDir(lvl)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		prop := filepath.Join(dir, "note.md")
		if err := os.WriteFile(prop, []byte("fact one\nfact two"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := m.AcceptProposal(lvl, "note.md"); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if fileExists(prop) {
			t.Errorf("%s: accepted proposal not removed", label)
		}
		mem, _ := m.MemoryText(lvl)
		if !strings.Contains(mem, "fact one\nfact two\n") {
			t.Errorf("%s: memoria.md missing literal text:\n%s", label, mem)
		}
	}
}

func TestPersonalityCRUD(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.WritePersonality("notes", "work in ~/Notes"); err != nil {
		t.Fatal(err)
	}
	if !m.HasPersonality("notes") {
		t.Fatal("notes personality missing after write")
	}
	if err := m.RenamePersonality("notes", "notebook"); err != nil {
		t.Fatal(err)
	}
	if m.HasPersonality("notes") || !m.HasPersonality("notebook") {
		t.Error("rename did not take")
	}
	// A personality that is a project default cannot be deleted.
	if err := m.NewProject("np", ProjectMeta{DefaultPersonality: "notebook"}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeletePersonality("notebook"); err == nil {
		t.Error("deleting a project's default personality should fail")
	}
}
