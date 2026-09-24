package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"phi/internal/query"
)

// withTempFrecency points XDG_STATE_HOME at a fresh temp dir so
// runQueryRecord's own frecency file never touches the real machine's.
func withTempFrecency(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

func TestRunQueryRecordPlainForm(t *testing.T) {
	withTempFrecency(t)
	var stdout, stderr bytes.Buffer
	if code := runQueryRecord([]string{"app:firefox.desktop"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runQueryRecord(plain) = %d, stderr: %s", code, stderr.String())
	}

	path, err := query.FrecencyPath()
	if err != nil {
		t.Fatalf("FrecencyPath: %v", err)
	}
	f, err := query.LoadFrecency(path)
	if err != nil {
		t.Fatalf("LoadFrecency: %v", err)
	}
	if got := f.Score("app:firefox.desktop"); got <= 0 {
		t.Errorf("Score after runQueryRecord = %v, want > 0", got)
	}
}

func TestRunQueryRecordWithSnapshotJSON(t *testing.T) {
	withTempFrecency(t)
	var stdout, stderr bytes.Buffer
	resultJSON := `{"id":"app:firefox.desktop","provider":"application","title":"Firefox","subtitle":"firefox","score":9080,"action":{"kind":"exec","data":{"command":"firefox"}}}`
	if code := runQueryRecord([]string{"app:firefox.desktop", resultJSON}, &stdout, &stderr); code != 0 {
		t.Fatalf("runQueryRecord(with JSON) = %d, stderr: %s", code, stderr.String())
	}

	path, err := query.FrecencyPath()
	if err != nil {
		t.Fatalf("FrecencyPath: %v", err)
	}
	f, err := query.LoadFrecency(path)
	if err != nil {
		t.Fatalf("LoadFrecency: %v", err)
	}
	got := f.Snapshots(map[string]bool{"application": true})
	if len(got) != 1 || got[0].Title != "Firefox" {
		t.Fatalf("Snapshots after runQueryRecord(with JSON) = %v, want the stored Firefox snapshot", got)
	}
}

func TestRunQueryRecordMalformedJSONStillRecords(t *testing.T) {
	withTempFrecency(t)
	var stdout, stderr bytes.Buffer
	// A malformed second argument must not fail the call — the plain
	// selection (count/lastUsed) still has to be recorded.
	if code := runQueryRecord([]string{"app:firefox.desktop", "{not valid json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runQueryRecord(malformed JSON) = %d, stderr: %s", code, stderr.String())
	}

	path, err := query.FrecencyPath()
	if err != nil {
		t.Fatalf("FrecencyPath: %v", err)
	}
	f, err := query.LoadFrecency(path)
	if err != nil {
		t.Fatalf("LoadFrecency: %v", err)
	}
	if got := f.Score("app:firefox.desktop"); got <= 0 {
		t.Errorf("Score after malformed-JSON record = %v, want > 0 (the plain bump must still happen)", got)
	}
	if got := f.Snapshots(map[string]bool{"application": true}); got != nil {
		t.Errorf("Snapshots = %v, want none stored from malformed JSON", got)
	}
}

func TestRunQueryRecordWrongArgCount(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runQueryRecord(nil, &stdout, &stderr); code == 0 {
		t.Error("runQueryRecord(no args) = 0, want a non-zero exit and usage on stderr")
	}
	stderr.Reset()
	if code := runQueryRecord([]string{"a", "b", "c"}, &stdout, &stderr); code == 0 {
		t.Error("runQueryRecord(3 args) = 0, want a non-zero exit and usage on stderr")
	}
}

// TestWithTempFrecencyRedirectsPath confirms the helper above actually
// redirects FrecencyPath under XDG_STATE_HOME, so the tests above cannot
// silently pass by touching the real machine's own frecency file.
func TestWithTempFrecencyRedirectsPath(t *testing.T) {
	withTempFrecency(t)
	path, err := query.FrecencyPath()
	if err != nil {
		t.Fatalf("FrecencyPath: %v", err)
	}
	wantDir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "phi")
	if filepath.Dir(path) != wantDir {
		t.Errorf("FrecencyPath = %q, want it under %q", path, wantDir)
	}
}
