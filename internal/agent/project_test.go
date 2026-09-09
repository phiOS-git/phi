package agent

import (
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
	prog, _ := m.ProjectInstructions("proj")
	if !strings.Contains(prog, "be precise") || !strings.Contains(prog, "updated") {
		t.Errorf("progetto.md stale:\n%s", prog)
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
	if err := m.AddProjectFolder("proj", ok); err != nil {
		t.Fatalf("adding a plain temp dir should work: %v", err)
	}
	folders, _ := m.ProjectFolders("proj")
	if len(folders) != 1 {
		t.Fatalf("folders = %v", folders)
	}

	// A path inside a blocked root is refused.
	home := t.TempDir()
	t.Setenv("HOME", home)
	ssh := filepath.Join(home, ".ssh")
	if err := ValidateFolderOfInterest(ssh); err == nil {
		t.Error("~/.ssh should be blocked as a folder of interest")
	}
}

func TestValidateCodeDir(t *testing.T) {
	// A fake home that is NOT under a system deny root (/var, /tmp, …); the
	// real user home is such a location on every platform this runs on.
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
		t.Error("$HOME itself must be refused as an A2 working dir")
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
	// The blocklisted ~/.ssh is refused.
	ssh := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(ssh, 0o700)
	if _, err := ValidateCodeDir(ssh); err == nil {
		t.Error("~/.ssh must be refused")
	}
}
