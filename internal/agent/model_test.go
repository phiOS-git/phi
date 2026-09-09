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
		t.Fatalf("Ensure created %d files, want 2 (§8.3)", len(created))
	}
	ps, _ := m.Personalities()
	if strings.Join(ps, ",") != "general,technical" {
		t.Errorf("personalities = %v", ps)
	}
	// Idempotent: a second Ensure creates nothing and does not overwrite.
	general := filepath.Join(m.personalitaDir(), "general.md")
	if err := os.WriteFile(general, []byte("EDITED BY USER"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, _ := m.Ensure()
	if len(again) != 0 {
		t.Errorf("second Ensure created %v, want nothing", again)
	}
	got, _ := os.ReadFile(general)
	if string(got) != "EDITED BY USER" {
		t.Error("Ensure overwrote a user-edited personality")
	}
}

func TestModelProjectLifecycle(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("study"); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("study"); err == nil {
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
		if err := m.NewProject(bad); err == nil {
			t.Errorf("NewProject(%q) should be rejected", bad)
		}
	}
}

func TestModelProposalNameTraversal(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("p"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../memoria.md", "a/b", "..", ".", ""} {
		if _, err := m.ProposalText("p", bad); err == nil {
			t.Errorf("ProposalText(%q) should be rejected", bad)
		}
		if err := m.AcceptProposal("p", bad); err == nil {
			t.Errorf("AcceptProposal(%q) should be rejected", bad)
		}
		if err := m.RejectProposal("p", bad); err == nil {
			t.Errorf("RejectProposal(%q) should be rejected", bad)
		}
	}
}

func TestModelAcceptProposalAppendsLiteral(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("p"); err != nil {
		t.Fatal(err)
	}
	prop := filepath.Join(m.projectDir("p"), "proposte", "note.md")
	if err := os.WriteFile(prop, []byte("fact one\nfact two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.AcceptProposal("p", "note.md"); err != nil {
		t.Fatal(err)
	}
	if fileExists(prop) {
		t.Error("accepted proposal should be removed from proposte/")
	}
	mem, _ := m.MemoryText("p")
	if !strings.Contains(mem, "fact one\nfact two\n") {
		t.Errorf("memoria.md missing the literal proposal text:\n%s", mem)
	}
}
