package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectMetaRoundTrip(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("proj", ProjectMeta{Title: "Proj", Description: "d"}); err != nil {
		t.Fatal(err)
	}
	meta, _ := m.LoadProjectMeta("proj")
	meta.Instructions = []string{"be precise", ""}
	meta.Description = "updated"
	if err := m.SaveProjectMeta("proj", meta); err != nil {
		t.Fatal(err)
	}
	got, _ := m.LoadProjectMeta("proj")
	if got.Description != "updated" {
		t.Errorf("description = %q", got.Description)
	}
	if len(got.Instructions) != 1 || got.Instructions[0] != "be precise" {
		t.Errorf("instructions not normalised: %v", got.Instructions)
	}
	instr, _ := m.ProjectInstructionsText("proj")
	if !strings.Contains(instr, "be precise") || !strings.Contains(instr, "updated") {
		t.Errorf("instructions.md stale:\n%s", instr)
	}
}

// TestProjectMetaLegacyConversion feeds the pre-pi project.json shape
// (default_personality, folders as a plain string array) through
// UnmarshalJSON and checks the §4 conversion rules.
func TestProjectMetaLegacyConversion(t *testing.T) {
	raw := `{
		"title": "Notes",
		"default_personality": "general",
		"folders": ["/home/u/Notes", "/home/u/Other Notes/"]
	}`
	var meta ProjectMeta
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.DefaultProfile != General {
		t.Errorf("default_profile = %q, want general", meta.DefaultProfile)
	}
	if len(meta.Folders) != 2 {
		t.Fatalf("folders = %+v", meta.Folders)
	}
	host := HostShortName()
	if meta.Folders[0].Name != "notes" || meta.Folders[0].Mode != "ro" || meta.Folders[0].Paths[host] != "/home/u/Notes" {
		t.Errorf("folder 0 = %+v", meta.Folders[0])
	}
	// De-duplicated sanitised name ("other-notes" from "Other Notes").
	if meta.Folders[1].Name == meta.Folders[0].Name {
		t.Errorf("folder names collided: %+v", meta.Folders)
	}

	// The current shape round-trips unchanged.
	current := `{"title":"T","default_profile":"coding","folders":[{"name":"vault","mode":"rw","paths":{"h":"/p"}}]}`
	var meta2 ProjectMeta
	if err := json.Unmarshal([]byte(current), &meta2); err != nil {
		t.Fatal(err)
	}
	if meta2.DefaultProfile != Coding || len(meta2.Folders) != 1 || meta2.Folders[0].Name != "vault" {
		t.Errorf("current-shape meta = %+v", meta2)
	}
}

func TestResolveFolders(t *testing.T) {
	meta := ProjectMeta{Folders: []Folder{
		{Name: "vault", Mode: "ro", Paths: map[string]string{"zotac": "/z/Notes", "razer": "/r/Notes"}},
		{Name: "code", Mode: "rw", Paths: map[string]string{"zotac": "/z/code"}},
		{Name: "elsewhere", Mode: "ro", Paths: map[string]string{"mini": "/m/x"}},
	}}
	got := meta.ResolveFolders("zotac")
	if len(got) != 2 {
		t.Fatalf("ResolveFolders(zotac) = %+v", got)
	}
	if got[0] != (ResolvedFolder{Name: "vault", Mode: "ro", Path: "/z/Notes"}) {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1] != (ResolvedFolder{Name: "code", Mode: "rw", Path: "/z/code"}) {
		t.Errorf("got[1] = %+v", got[1])
	}
	if len(meta.ResolveFolders("mini")) != 1 {
		t.Errorf("ResolveFolders(mini) should find exactly 'elsewhere'")
	}
	if len(meta.ResolveFolders("nowhere")) != 0 {
		t.Errorf("ResolveFolders(nowhere) should find nothing")
	}
}

func TestProjectFolderCRUD(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("proj", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := m.AddProjectFolder("proj", dir, "vault", "ro"); err != nil {
		t.Fatal(err)
	}
	meta, _ := m.LoadProjectMeta("proj")
	if len(meta.Folders) != 1 || meta.Folders[0].Name != "vault" {
		t.Fatalf("folders after add = %+v", meta.Folders)
	}
	if err := m.SetProjectFolderMode("proj", "vault", "rw"); err != nil {
		t.Fatal(err)
	}
	meta, _ = m.LoadProjectMeta("proj")
	if meta.Folders[0].Mode != "rw" {
		t.Errorf("mode after SetProjectFolderMode = %q", meta.Folders[0].Mode)
	}
	if err := m.RemoveProjectFolder("proj", "vault"); err != nil {
		t.Fatal(err)
	}
	meta, _ = m.LoadProjectMeta("proj")
	if len(meta.Folders) != 0 {
		t.Errorf("folders after remove = %+v", meta.Folders)
	}
	if err := m.RemoveProjectFolder("proj", "vault"); err == nil {
		t.Error("removing a missing folder should fail")
	}
}

func TestFolderOfInterestBlocklist(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("proj", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}

	ok := t.TempDir()
	if err := m.AddProjectFolder("proj", ok, "", ""); err != nil {
		t.Fatalf("adding a plain temp dir should work: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	ssh := filepath.Join(home, ".ssh")
	if err := ValidateFolderOfInterest(ssh); err == nil {
		t.Error("~/.ssh should be blocked as a folder of interest")
	}
}

func TestValidateCodeDir(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	home, err := os.MkdirTemp(realHome, ".phi-codedir-test-")
	if err != nil {
		t.Skipf("cannot make a temp dir under %s: %v", realHome, err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	if _, err := ValidateCodeDir(home); err == nil {
		t.Error("$HOME itself must be refused as a coding working dir")
	}
	if _, err := ValidateCodeDir("/etc"); err == nil {
		t.Error("/etc must be refused")
	}
	work := filepath.Join(home, "work", "proj")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := ValidateCodeDir(work); err != nil || got == "" {
		t.Errorf("a real project dir under $HOME should be allowed: %v", err)
	}
	ssh := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(ssh, 0o700)
	if _, err := ValidateCodeDir(ssh); err == nil {
		t.Error("~/.ssh must be refused")
	}
}
