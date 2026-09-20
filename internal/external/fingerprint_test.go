package external

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFingerprintMissingDirIsEmptyNotError(t *testing.T) {
	digest, n, err := Fingerprint(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if digest != "" || n != 0 {
		t.Fatalf("Fingerprint(missing) = (%q, %d), want (\"\", 0)", digest, n)
	}
}

func TestFingerprintStableAcrossRepeatedCalls(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}

	d1, n1, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	d2, n2, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if d1 != d2 || n1 != n2 {
		t.Fatalf("Fingerprint of an untouched tree changed: (%q,%d) != (%q,%d)", d1, n1, d2, n2)
	}
	if d1 == "" || n1 != 3 { // a.txt, sub/, sub/b.txt
		t.Fatalf("Fingerprint = (%q, %d), want a non-empty digest over 3 entries", d1, n1)
	}
}

func TestFingerprintChangesWhenPathSetChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, nBefore, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	// Add a file rather than rewriting one in place: this changes the path
	// set unconditionally, unlike a content rewrite, which can collide on
	// both size and the filesystem's mtime granularity and falsely look
	// unchanged.
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, nAfter, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	if before == after {
		t.Fatalf("Fingerprint did not change after adding a file: both %q", before)
	}
	if nAfter != nBefore+1 {
		t.Fatalf("entries = %d, want %d after adding one file", nAfter, nBefore+1)
	}
}

func TestFingerprintChangesWhenMtimeChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	// Same path, same size, only mtime moves — set it explicitly, far
	// enough away that no filesystem's mtime granularity can collapse the
	// two values back together.
	future := time.Now().Add(48 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	after, _, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if before == after {
		t.Fatalf("Fingerprint did not change after Chtimes: both %q", before)
	}
}

func TestFingerprintRejectsUnknownRoot(t *testing.T) {
	if _, err := fingerprintRootDir("/home/x", "nonsense"); err == nil {
		t.Fatal("fingerprintRootDir(\"nonsense\") = nil error, want a rejection")
	}
}

func TestAcceptRejectsUnknownRoot(t *testing.T) {
	if err := Accept("nonsense"); err == nil {
		t.Fatal("Accept(\"nonsense\") = nil error, want a rejection")
	}
}

func TestAcceptWritesAndAuditReadsTheSameStore(t *testing.T) {
	// Accept always resolves the real os.UserHomeDir() for the root it
	// fingerprints, but the store path itself honors XDG_STATE_HOME, so
	// this test can observe Accept's effect without touching the real
	// home directory's fingerprint store.
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	if err := Accept("local-bin"); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	store, err := loadFingerprintStore()
	if err != nil {
		t.Fatalf("loadFingerprintStore: %v", err)
	}
	rs, ok := store["local-bin"]
	if !ok {
		t.Fatal("loadFingerprintStore() has no entry for local-bin after Accept")
	}
	_ = rs

	path, err := fingerprintStorePath()
	if err != nil {
		t.Fatalf("fingerprintStorePath: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fingerprint store was not written to %s: %v", path, err)
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Fatalf("temporary %s.new was left behind after a successful write", path)
	}
}
