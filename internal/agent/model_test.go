package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testModel points DataRoot/StateDir/ConfigDir at fresh temp dirs and
// returns an opened Model.
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

func TestModelEnsureCreatesSkeleton(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		m.proposteDirSystem(), m.sessionsDirSystem(), m.projectsDir(),
		filepath.Join(m.profileDir(General), "proposte"),
		filepath.Join(m.profileDir(Academic), "proposte"),
		filepath.Join(m.profileDir(Coding), "proposte"),
	} {
		if !dirExists(dir) {
			t.Errorf("missing %s", dir)
		}
	}
	// Idempotent: a second Ensure creates nothing new.
	report, err := m.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if len(report) != 0 {
		t.Errorf("second Ensure reported %v, want nothing", report)
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
	if meta.Description != "exam prep" || meta.DefaultProfile != General {
		t.Errorf("meta = %+v", meta)
	}
	instr, _ := m.ProjectInstructionsText("study")
	if !strings.Contains(instr, "exam prep") {
		t.Errorf("instructions.md did not carry the description:\n%s", instr)
	}

	if err := m.DeleteProject("study"); err != nil {
		t.Fatal(err)
	}
	if m.HasProject("study") {
		t.Error("project still present after DeleteProject")
	}
	if err := m.DeleteProject("study"); err == nil {
		t.Error("deleting a missing project should fail")
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
		"system":  SystemLevel(),
		"profile": ProfileLevel(General),
		"project": ProjectLevel("p"),
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

// TestLegacyMigration builds a fake a1 legacy tree by hand and checks Ensure
// copies it non-destructively into the new layout exactly once.
func TestLegacyMigration(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	legacyRoot := filepath.Join(dataHome, "phi-agent", "a1")
	mustMkdirAll(t, filepath.Join(legacyRoot, "proposte"))
	mustWriteFile(t, filepath.Join(legacyRoot, "memoria.md"), "system fact\n")
	mustWriteFile(t, filepath.Join(legacyRoot, "proposte", "p1.md"), "proposal one\n")
	mustMkdirAll(t, filepath.Join(legacyRoot, "personalita", "general"))
	mustWriteFile(t, filepath.Join(legacyRoot, "personalita", "general", "memoria.md"), "general fact\n")
	mustMkdirAll(t, filepath.Join(legacyRoot, "personalita", "technical"))
	mustWriteFile(t, filepath.Join(legacyRoot, "personalita", "technical", "memoria.md"), "technical fact\n")
	mustMkdirAll(t, filepath.Join(legacyRoot, "personalita", "other"))
	mustWriteFile(t, filepath.Join(legacyRoot, "personalita", "other", "memoria.md"), "other fact\n")

	projDir := filepath.Join(legacyRoot, "projects", "study")
	mustMkdirAll(t, filepath.Join(projDir, "proposte"))
	mustMkdirAll(t, filepath.Join(projDir, "output"))
	mustMkdirAll(t, filepath.Join(projDir, "materiali"))
	mustMkdirAll(t, filepath.Join(projDir, "conversazioni"))
	mustMkdirAll(t, filepath.Join(projDir, "archivio"))
	mustWriteFile(t, filepath.Join(projDir, "memoria.md"), "project fact\n")
	mustWriteFile(t, filepath.Join(projDir, "output", "notes.md"), "notes\n")
	mustWriteFile(t, filepath.Join(projDir, "materiali", "a.pdf"), "pdf\n")
	mustWriteFile(t, filepath.Join(projDir, "conversazioni", "c1.md"), "chat\n")
	mustWriteFile(t, filepath.Join(projDir, "archivio", "arc.md"), "arch\n")
	legacyMeta := map[string]any{
		"title":               "Study",
		"default_personality": "technical",
		"folders":             []string{"/home/u/Notes"},
	}
	b, _ := json.Marshal(legacyMeta)
	mustWriteFile(t, filepath.Join(projDir, "project.json"), string(b))

	m, err := OpenModel()
	if err != nil {
		t.Fatal(err)
	}
	report, err := m.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if len(report) == 0 {
		t.Fatal("Ensure reported nothing for a legacy migration")
	}

	if got, _ := m.MemoryText(SystemLevel()); got != "system fact\n" {
		t.Errorf("system memoria.md = %q", got)
	}
	if !fileExists(filepath.Join(m.proposteDirSystem(), "p1.md")) {
		t.Error("system proposal not migrated")
	}
	if got, _ := m.MemoryText(ProfileLevel(General)); got != "general fact\n" {
		t.Errorf("profile general memoria.md = %q", got)
	}
	if got, _ := m.MemoryText(ProfileLevel(Coding)); got != "technical fact\n" {
		t.Errorf("profile coding memoria.md (from technical) = %q", got)
	}

	if !m.HasProject("study") {
		t.Fatal("project study not migrated")
	}
	meta, err := m.LoadProjectMeta("study")
	if err != nil {
		t.Fatal(err)
	}
	if meta.DefaultProfile != General {
		t.Errorf("migrated default_profile = %q, want general (from default_personality)", meta.DefaultProfile)
	}
	if len(meta.Folders) != 1 || meta.Folders[0].Mode != "ro" {
		t.Errorf("migrated folders = %+v", meta.Folders)
	}
	if !fileExists(filepath.Join(m.projectDir("study"), "output", "notes.md")) {
		t.Error("output/ not migrated")
	}
	if !fileExists(filepath.Join(m.projectDir("study"), "allegati", "a.pdf")) {
		t.Error("materiali/ -> allegati/ not migrated")
	}
	if !fileExists(filepath.Join(m.projectDir("study"), "allegati", "legacy-conversazioni", "c1.md")) {
		t.Error("conversazioni/ -> allegati/legacy-conversazioni/ not migrated")
	}
	if !fileExists(filepath.Join(m.projectDir("study"), "allegati", "legacy-archivio", "arc.md")) {
		t.Error("archivio/ -> allegati/legacy-archivio/ not migrated")
	}

	foundOther := false
	for _, line := range report {
		if strings.Contains(line, "\"other\"") {
			foundOther = true
		}
	}
	if !foundOther {
		t.Errorf("report did not mention the unmigrated 'other' personality: %v", report)
	}

	// Non-destructive: edit the migrated system memoria.md, run Ensure
	// again, confirm it is untouched (marker present -> no second pass).
	sysMem := filepath.Join(m.root, "memoria.md")
	mustWriteFile(t, sysMem, "EDITED BY USER\n")
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(sysMem)
	if string(got) != "EDITED BY USER\n" {
		t.Error("second Ensure re-ran the legacy migration and overwrote user data")
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
