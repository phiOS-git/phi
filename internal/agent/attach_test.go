package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAttachmentsMissingProject(t *testing.T) {
	m := testModel(t)
	if _, err := m.Attachments("nope"); err == nil {
		t.Error("Attachments on a missing project should fail")
	}
	if _, err := m.AddAttachment("nope", "/tmp/x"); err == nil {
		t.Error("AddAttachment on a missing project should fail")
	}
	if err := m.RemoveAttachment("nope", "x"); err == nil {
		t.Error("RemoveAttachment on a missing project should fail")
	}
}

func TestAttachmentsEmptyProjectReturnsEmptySlice(t *testing.T) {
	m := testModel(t)
	if err := m.NewProject("study", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	got, err := m.Attachments("study")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Attachments on an empty project = %v, want empty non-nil slice", got)
	}
}

func TestAddAttachmentFileAndDir(t *testing.T) {
	m := testModel(t)
	if err := m.NewProject("study", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}

	srcFile := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(srcFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := m.AddAttachment("study", srcFile)
	if err != nil {
		t.Fatal(err)
	}
	if name != "notes.txt" {
		t.Errorf("name = %q, want notes.txt", name)
	}

	srcDir := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirName, err := m.AddAttachment("study", srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if dirName != "bundle" {
		t.Errorf("dirName = %q, want bundle", dirName)
	}

	list, err := m.Attachments("study")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("Attachments = %+v, want 2 entries", list)
	}
	if list[0].Name != "bundle" || !list[0].IsDir {
		t.Errorf("list[0] = %+v, want dir bundle first (sorted)", list[0])
	}
	if list[1].Name != "notes.txt" || list[1].IsDir || list[1].Size != 5 {
		t.Errorf("list[1] = %+v, want file notes.txt size 5", list[1])
	}

	// Nested file survived the directory copy.
	if _, err := os.Stat(filepath.Join(m.ProjectDir("study"), "allegati", "bundle", "sub", "a.txt")); err != nil {
		t.Errorf("nested file missing after copyTree: %v", err)
	}
}

func TestAddAttachmentRefusesExisting(t *testing.T) {
	m := testModel(t)
	if err := m.NewProject("study", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddAttachment("study", src); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddAttachment("study", src); err == nil {
		t.Error("AddAttachment should refuse an existing name")
	}
}

func TestRemoveAttachmentRejectsPathEscape(t *testing.T) {
	m := testModel(t)
	if err := m.NewProject("study", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveAttachment("study", "../../etc/passwd"); err == nil {
		t.Error("RemoveAttachment should reject a name containing a path separator")
	}
}

func TestRemoveAttachmentDeletes(t *testing.T) {
	m := testModel(t)
	if err := m.NewProject("study", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddAttachment("study", src); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveAttachment("study", "notes.txt"); err != nil {
		t.Fatal(err)
	}
	list, err := m.Attachments("study")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("Attachments after remove = %+v, want none", list)
	}
}
