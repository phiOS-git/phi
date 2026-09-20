package external

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExternalFileValidFixture(t *testing.T) {
	entries, problems, err := parseExternalFile("testdata/external.valid.txt", "desktop")
	if err != nil {
		t.Fatalf("parseExternalFile: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}

	want := []Entry{
		{Name: "wivrn", Tier: "T2", Source: "flathub:io.github.wivrn.wivrn", Ref: "0.22", SHA256: "-",
			Reason: "VR streaming, no official package", Profile: "desktop"},
		{Name: "prowlarr", Tier: "TC", Source: "docker.io/linuxserver/prowlarr",
			Ref: "sha256:9c1f0a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7", SHA256: "-",
			Reason: "indexer manager", Profile: "desktop"},
		{Name: "schoolapp", Tier: "T4", Source: "https://example.org/schoolapp.AppImage", Ref: "3.1.2",
			SHA256: "a4f9c2d1e0b8a7968574635241302f1e0d9c8b7a695847362514039281706f5e",
			Reason: "required by a course, no alternative", Profile: "desktop"},
		{Name: "helper", Tier: "T3", Source: "https://example.org/helper.tar.gz", Ref: "v1.4.0", SHA256: "-",
			Reason: "contained helper, no official package", Profile: "desktop"},
	}

	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, e := range entries {
		if e != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, e, want[i])
		}
	}
}

func TestParseExternalFileInvalidFixture(t *testing.T) {
	entries, problems, err := parseExternalFile("testdata/external.invalid.txt", "desktop")
	if err != nil {
		t.Fatalf("parseExternalFile: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want none — every line in this fixture is invalid", entries)
	}

	wantSubstrings := []string{
		"invalid name",                    // bad name
		"belongs in packages.txt",         // T0
		`must not be "latest"`,            // ref latest
		"must pin ref by image digest",    // TC ref not a digest
		"requires a 64-character",         // T4 missing checksum
		`sha256 must be "-"`,              // T2 carrying a checksum
		"expected 6 '|'-separated fields", // too few fields
		"reason must not be empty",        // empty reason
	}
	if len(problems) != len(wantSubstrings) {
		t.Fatalf("got %d problems, want %d: %+v", len(problems), len(wantSubstrings), problems)
	}
	for i, want := range wantSubstrings {
		if !strings.Contains(problems[i].Detail, want) {
			t.Errorf("problem %d (line %d) = %q, want it to contain %q", i, problems[i].Line, problems[i].Detail, want)
		}
	}
}

func TestParseEntryCommentAndWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "external.txt")
	content := "  wivrn   |  T2  |  flathub:io.github.wivrn.wivrn  |  0.22  |  -  |  VR streaming # trailing comment\n" +
		"# a full-line comment\n" +
		"\n" +
		"   \n" +
		"helper | T3 | https://example.org/helper.tar.gz | v1.4.0 | - | ok # another comment\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, problems, err := parseExternalFile(path, "p")
	if err != nil {
		t.Fatalf("parseExternalFile: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (blank and comment-only lines dropped): %+v", len(entries), entries)
	}
	if entries[0].Name != "wivrn" || entries[0].Reason != "VR streaming" {
		t.Errorf("entry 0 = %+v, want name=wivrn reason=%q (leading/trailing whitespace and trailing comment stripped)", entries[0], "VR streaming")
	}
	if entries[1].Name != "helper" || entries[1].Reason != "ok" {
		t.Errorf("entry 1 = %+v, want name=helper reason=%q", entries[1], "ok")
	}
}

func TestParseEntryHashInsideFieldIsAlwaysAComment(t *testing.T) {
	// The frozen format has no escaping: '#' truncates the line no matter
	// where it appears, so a value that itself needs '#' simply cannot be
	// expressed — this pins that behavior rather than leaving it implicit.
	dir := t.TempDir()
	path := filepath.Join(dir, "external.txt")
	content := "helper | T3 | https://example.org/helper.tar.gz | v1.4.0 | - | reason with # a hash\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, problems, err := parseExternalFile(path, "p")
	if err != nil {
		t.Fatalf("parseExternalFile: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if len(entries) != 1 || entries[0].Reason != "reason with" {
		t.Fatalf("entries = %+v, want one entry with reason %q (truncated at the '#')", entries, "reason with")
	}
}

func TestParseExternalFileMissingIsEmpty(t *testing.T) {
	entries, problems, err := parseExternalFile(filepath.Join(t.TempDir(), "does-not-exist.txt"), "p")
	if err != nil {
		t.Fatalf("parseExternalFile: %v", err)
	}
	if entries != nil || problems != nil {
		t.Fatalf("entries=%v problems=%v, want both nil for a missing file", entries, problems)
	}
}

func writeExternalTxt(t *testing.T, root, profile, content string) {
	t.Helper()
	dir := filepath.Join(root, "profiles", profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "external.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadUnionsProfilesInOrder(t *testing.T) {
	root := t.TempDir()
	writeExternalTxt(t, root, "base", "x | T2 | flathub:org.example.X | 1.0 | - | x reason\n")
	writeExternalTxt(t, root, "desktop", "y | T3 | https://example.org/y.tar.gz | 1.0 | - | y reason\n")

	entries, problems, err := Load(root, []string{"base", "desktop"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if len(entries) != 2 || entries[0].Name != "x" || entries[1].Name != "y" {
		t.Fatalf("entries = %+v, want [x, y] in profile order", entries)
	}
}

func TestLoadLaterProfileOverridesEarlier(t *testing.T) {
	root := t.TempDir()
	writeExternalTxt(t, root, "base",
		"x | T2 | flathub:org.example.X | 1.0 | - | from base\n"+
			"y | T3 | https://example.org/y.tar.gz | 1.0 | - | from base\n")
	writeExternalTxt(t, root, "desktop",
		"y | T3 | https://example.org/y.tar.gz | 2.0 | - | from desktop\n"+
			"z | T4 | https://example.org/z.AppImage | 1.0 | "+
			"a4f9c2d1e0b8a7968574635241302f1e0d9c8b7a695847362514039281706f5e | from desktop\n")

	entries, problems, err := Load(root, []string{"base", "desktop"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}

	// First-seen position (x, y, z) is preserved, but y's value is the
	// desktop profile's override, not base's.
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	names := []string{entries[0].Name, entries[1].Name, entries[2].Name}
	if names[0] != "x" || names[1] != "y" || names[2] != "z" {
		t.Fatalf("order = %v, want [x y z] (first-seen position preserved)", names)
	}
	if entries[1].Ref != "2.0" || entries[1].Reason != "from desktop" || entries[1].Profile != "desktop" {
		t.Errorf("y = %+v, want the desktop profile's override (ref=2.0, from desktop)", entries[1])
	}
}

func TestResolveProfiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "hosts"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# razer\nbase\ndesktop # inline comment\n\nlaptop\n"
	if err := os.WriteFile(filepath.Join(root, "hosts", "razer.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveProfiles(root, "razer")
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	want := []string{"base", "desktop", "laptop"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveProfilesMissingHost(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveProfiles(root, "no-such-host")
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil for an undeclared host", got)
	}
}
