package external

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnumerator lets a test control exactly what Audit believes the
// machine's actual state is, without touching pacman, flatpak, podman or a
// real ~/Applications — none of which exist on the darwin build host.
type fakeEnumerator struct {
	flatpak    []string
	appImages  []string
	opt        []string
	containers []string
	dirs       map[string][]string
	manifest   []string
}

func (f *fakeEnumerator) Flatpak(context.Context) ([]string, error)    { return f.flatpak, nil }
func (f *fakeEnumerator) AppImages() ([]string, error)                 { return f.appImages, nil }
func (f *fakeEnumerator) Opt() ([]string, error)                       { return f.opt, nil }
func (f *fakeEnumerator) Containers(context.Context) ([]string, error) { return f.containers, nil }
func (f *fakeEnumerator) Manifest() ([]string, error)                  { return f.manifest, nil }
func (f *fakeEnumerator) ListDir(path string) ([]string, error) {
	if f.dirs == nil {
		return nil, nil
	}
	return f.dirs[path], nil
}

func findFinding(findings []Finding, check, kind, name string) *Finding {
	for i := range findings {
		if findings[i].Check == check && findings[i].Kind == kind && findings[i].Name == name {
			return &findings[i]
		}
	}
	return nil
}

func entryStatus(entries []Entry, name string) string {
	for _, e := range entries {
		if e.Name == name {
			return e.Status
		}
	}
	return "<not found>"
}

func TestAuditDriftBothDirections(t *testing.T) {
	root := t.TempDir()
	writeExternalTxt(t, root, "test",
		"wivrn | T2 | flathub:io.github.wivrn.wivrn | 0.22 | - | vr\n"+
			"schoolapp | T4 | https://example.org/schoolapp.AppImage | 1.0 | "+
			"a4f9c2d1e0b8a7968574635241302f1e0d9c8b7a695847362514039281706f5e | course\n"+
			"helper | T3 | https://example.org/helper.tar.gz | 1.0 | - | contained\n"+
			"prowlarr | TC | docker.io/linuxserver/prowlarr | sha256:9c1f0a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7 | - | indexer\n")

	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // isolate the fingerprint store

	enum := &fakeEnumerator{
		flatpak:    []string{"io.github.wivrn.wivrn", "org.mozilla.firefox"}, // firefox undeclared
		appImages:  nil,                                                      // schoolapp declared but absent
		opt:        []string{"helper"},                                       // matches
		containers: nil,                                                      // prowlarr declared but absent
	}

	report, err := Audit(context.Background(), Config{Root: root, Profiles: []string{"test"}, Home: home, Enum: enum})
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}

	if f := findFinding(report.Findings, "drift", "undeclared", "org.mozilla.firefox"); f == nil {
		t.Errorf("expected an undeclared-Flatpak finding for org.mozilla.firefox, got %+v", report.Findings)
	}
	if f := findFinding(report.Findings, "drift", "missing", "schoolapp"); f == nil {
		t.Errorf("expected a missing finding for schoolapp, got %+v", report.Findings)
	}
	if f := findFinding(report.Findings, "drift", "missing", "prowlarr"); f == nil {
		t.Errorf("expected a missing finding for prowlarr, got %+v", report.Findings)
	}
	if f := findFinding(report.Findings, "drift", "undeclared", "helper"); f != nil {
		t.Errorf("helper is declared and present, must not be reported undeclared: %+v", f)
	}

	if got := entryStatus(report.Entries, "wivrn"); got != "ok" {
		t.Errorf("wivrn status = %q, want ok", got)
	}
	if got := entryStatus(report.Entries, "schoolapp"); got != "missing" {
		t.Errorf("schoolapp status = %q, want missing", got)
	}
	if got := entryStatus(report.Entries, "prowlarr"); got != "missing" {
		t.Errorf("prowlarr status = %q, want missing", got)
	}
	if got := entryStatus(report.Entries, "helper"); got != "unverified" {
		t.Errorf("helper (T3, present) status = %q, want unverified — no artifact survives extraction to re-check", got)
	}
}

func TestAuditLeakPaths(t *testing.T) {
	root := t.TempDir()
	writeExternalTxt(t, root, "test", "wivrn | T2 | flathub:io.github.wivrn.wivrn | 0.22 | - | vr\n")
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	npmGlobal := filepath.Join(home, ".npm-global")
	localBin := filepath.Join(home, ".local", "bin")
	libDir := filepath.Join(home, ".local", "lib")
	sitePkgs := filepath.Join(libDir, "python3.11", "site-packages")

	enum := &fakeEnumerator{
		flatpak: []string{"io.github.wivrn.wivrn"},
		dirs: map[string][]string{
			npmGlobal: {"some-global-package"},
			localBin:  {"wivrn-launcher", "stray-tool"},
			libDir:    {"python3.11"},
			sitePkgs:  {"leaked-package"},
		},
		manifest: []string{".local/bin/wivrn-launcher"},
	}

	report, err := Audit(context.Background(), Config{Root: root, Profiles: []string{"test"}, Home: home, Enum: enum})
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}

	if f := findFinding(report.Findings, "leak", "leak", "~/.npm-global"); f == nil {
		t.Errorf("expected a leak finding for ~/.npm-global, got %+v", report.Findings)
	}
	if f := findFinding(report.Findings, "leak", "leak", "~/.local/lib/python3.11/site-packages"); f == nil {
		t.Errorf("expected a leak finding for the python site-packages glob match, got %+v", report.Findings)
	}
	binFinding := findFinding(report.Findings, "leak", "leak", "~/.local/bin")
	if binFinding == nil {
		t.Fatalf("expected a leak finding for ~/.local/bin (stray-tool is unrecorded), got %+v", report.Findings)
	}
	if !strings.Contains(binFinding.Detail, "stray-tool") {
		t.Errorf("~/.local/bin finding = %q, want it to name stray-tool", binFinding.Detail)
	}
	if strings.Contains(binFinding.Detail, "wivrn-launcher") {
		t.Errorf("~/.local/bin finding = %q, must not flag wivrn-launcher — it's in the manifest", binFinding.Detail)
	}
}

func TestAuditLeakLocalBinAllowsDeclaredNameOfAnyTier(t *testing.T) {
	root := t.TempDir()
	writeExternalTxt(t, root, "test", "helper | T3 | https://example.org/helper.tar.gz | 1.0 | - | contained\n")
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	localBin := filepath.Join(home, ".local", "bin")
	enum := &fakeEnumerator{
		opt:  []string{"helper"},
		dirs: map[string][]string{localBin: {"helper"}}, // named exactly like the declared entry, no manifest record
	}

	report, err := Audit(context.Background(), Config{Root: root, Profiles: []string{"test"}, Home: home, Enum: enum})
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if f := findFinding(report.Findings, "leak", "leak", "~/.local/bin"); f != nil {
		t.Errorf("a ~/.local/bin entry matching a declared external.txt name must not be flagged: %+v", f)
	}
}

func TestAuditIntegrityChecksumMatch(t *testing.T) {
	root := t.TempDir()
	content := []byte("fake appimage bytes")
	sum := sha256.Sum256(content)
	hexSum := hex.EncodeToString(sum[:])
	writeExternalTxt(t, root, "test",
		"schoolapp | T4 | https://example.org/schoolapp.AppImage | 1.0 | "+hexSum+" | course\n")

	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(home, "Applications"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "Applications", "schoolapp.AppImage"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	enum := &fakeEnumerator{appImages: []string{"schoolapp.AppImage"}}
	report, err := Audit(context.Background(), Config{Root: root, Profiles: []string{"test"}, Home: home, Enum: enum})
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if got := entryStatus(report.Entries, "schoolapp"); got != "ok" {
		t.Errorf("schoolapp status = %q, want ok (checksum matches)", got)
	}
	if f := findFinding(report.Findings, "integrity", "checksum", "schoolapp"); f != nil {
		t.Errorf("a matching checksum must not produce a finding: %+v", f)
	}
}

func TestAuditIntegrityChecksumMismatch(t *testing.T) {
	root := t.TempDir()
	writeExternalTxt(t, root, "test",
		"schoolapp | T4 | https://example.org/schoolapp.AppImage | 1.0 | "+
			"a4f9c2d1e0b8a7968574635241302f1e0d9c8b7a695847362514039281706f5e | course\n")

	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(home, "Applications"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Content deliberately does not hash to the recorded sha256.
	if err := os.WriteFile(filepath.Join(home, "Applications", "schoolapp.AppImage"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}

	enum := &fakeEnumerator{appImages: []string{"schoolapp.AppImage"}}
	report, err := Audit(context.Background(), Config{Root: root, Profiles: []string{"test"}, Home: home, Enum: enum})
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if got := entryStatus(report.Entries, "schoolapp"); got != "changed" {
		t.Errorf("schoolapp status = %q, want changed", got)
	}
	if f := findFinding(report.Findings, "integrity", "checksum", "schoolapp"); f == nil {
		t.Errorf("expected a checksum finding, got %+v", report.Findings)
	}
}

func TestAuditRootsAndFingerprintChangeDetection(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("HOME", home) // Accept resolves os.UserHomeDir() for the real system

	enum := &fakeEnumerator{}
	cfg := Config{Root: root, Profiles: nil, Home: home, Enum: enum}

	report, err := Audit(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Audit (first run): %v", err)
	}
	if len(report.Roots) != 4 {
		t.Fatalf("Roots = %+v, want all 4 closed roots reported", report.Roots)
	}
	for _, r := range report.Roots {
		if r.Changed {
			t.Errorf("root %s reported Changed on a never-baselined store", r.Root)
		}
	}

	// Baseline "applications" as it is now (empty), then change it, and
	// confirm Audit notices without ever writing the store itself.
	if err := Accept("applications"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "Applications"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "Applications", "new.AppImage"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	report2, err := Audit(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Audit (second run): %v", err)
	}
	var apps *RootState
	for i := range report2.Roots {
		if report2.Roots[i].Root == "applications" {
			apps = &report2.Roots[i]
		}
	}
	if apps == nil || !apps.Changed {
		t.Fatalf("applications root = %+v, want Changed=true after adding a file post-baseline", apps)
	}
	if f := findFinding(report2.Findings, "integrity", "changed", "applications"); f == nil {
		t.Errorf("expected a changed finding for applications, got %+v", report2.Findings)
	}
}
