package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phi/internal/agent"
)

// withTempAgentHome points every XDG root at fresh temp dirs, the same
// isolation testModel gives internal/agent's own tests, so these CLI tests
// never touch a real phi-agent installation.
func withTempAgentHome(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestRunAgentUsageJSON(t *testing.T) {
	withTempAgentHome(t)
	var stdout, stderr bytes.Buffer
	if code := runAgentUsage([]string{"--days", "3", "--json"}, &stdout, &stderr, false); code != 0 {
		t.Fatalf("code = %d, stderr: %s", code, stderr.String())
	}
	var report agent.UsageReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, stdout.String())
	}
	if len(report.Days) != 3 {
		t.Errorf("len(Days) = %d, want 3", len(report.Days))
	}
}

func TestRunAgentUsageClampsDays(t *testing.T) {
	withTempAgentHome(t)
	var stdout, stderr bytes.Buffer
	if code := runAgentUsage([]string{"--days", "9999", "--json"}, &stdout, &stderr, false); code != 0 {
		t.Fatalf("code = %d, stderr: %s", code, stderr.String())
	}
	var report agent.UsageReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Days) != 365 {
		t.Errorf("len(Days) = %d, want clamped to 365", len(report.Days))
	}
}

func TestRunAgentPrefsSetAndGet(t *testing.T) {
	withTempAgentHome(t)
	var out, stderr bytes.Buffer
	if code := runAgentPrefs([]string{"set", "idleMinutes", "45", "--json"}, &out, &stderr, false); code != 0 {
		t.Fatalf("set code = %d, stderr: %s", code, stderr.String())
	}
	var p agent.Prefs
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.IdleMinutes != 45 {
		t.Errorf("IdleMinutes = %d, want 45", p.IdleMinutes)
	}

	out.Reset()
	if code := runAgentPrefs([]string{"get", "idleMinutes"}, &out, &stderr, false); code != 0 {
		t.Fatalf("get code = %d, stderr: %s", code, stderr.String())
	}
	if strings.TrimSpace(out.String()) != "45" {
		t.Errorf("get idleMinutes = %q, want 45", out.String())
	}

	out.Reset()
	if code := runAgentPrefs([]string{"set", "idleMinutes", "not-a-number"}, &out, &stderr, false); code == 0 {
		t.Error("set with a bad value should fail")
	}
}

func TestRunAgentStatusJSON(t *testing.T) {
	withTempAgentHome(t)
	// No real systemd/broker/models.json here: CollectStatus degrades every
	// field to its zero value rather than failing, so this only checks the
	// shape (fixed-length Units) comes back at all.
	var out, stderr bytes.Buffer
	if code := runAgentStatus([]string{"--json"}, &out, &stderr, false); code != 0 {
		t.Fatalf("code = %d, stderr: %s", code, stderr.String())
	}
	var st agent.Status
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out.String())
	}
	if len(st.Units) == 0 {
		t.Error("Units should be non-empty")
	}
}

func TestRunAgentBrokerRequestsMissing(t *testing.T) {
	withTempAgentHome(t)
	var out, stderr bytes.Buffer
	if code := runAgentBrokerRequests([]string{"--instance", "a1", "--json"}, &out, &stderr, false); code != 0 {
		t.Fatalf("code = %d, stderr: %s", code, stderr.String())
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("output = %q, want an empty JSON array", out.String())
	}
}

func TestRunAgentBrokerRequestsBadInstance(t *testing.T) {
	withTempAgentHome(t)
	var out, stderr bytes.Buffer
	if code := runAgentBrokerRequests([]string{"--instance", "bogus"}, &out, &stderr, false); code == 0 {
		t.Error("an unknown instance should fail")
	}
}

func TestRunAgentAttachmentAddListRemove(t *testing.T) {
	withTempAgentHome(t)
	m, err := agent.OpenModel()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("study", agent.ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(src, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, stderr bytes.Buffer
	if code := runAgentAttachment([]string{"add", "study", src, "--json"}, &out, &stderr, false); code != 0 {
		t.Fatalf("add code = %d, stderr: %s", code, stderr.String())
	}
	var added map[string]string
	if err := json.Unmarshal(out.Bytes(), &added); err != nil || added["name"] != "notes.txt" {
		t.Fatalf("add output = %q, err %v", out.String(), err)
	}

	out.Reset()
	if code := runAgentAttachment([]string{"list", "study", "--json"}, &out, &stderr, false); code != 0 {
		t.Fatalf("list code = %d, stderr: %s", code, stderr.String())
	}
	var atts []agent.Attachment
	if err := json.Unmarshal(out.Bytes(), &atts); err != nil || len(atts) != 1 {
		t.Fatalf("list output = %q, err %v", out.String(), err)
	}

	out.Reset()
	if code := runAgentAttachment([]string{"remove", "study", "notes.txt"}, &out, &stderr, false); code != 0 {
		t.Fatalf("remove code = %d, stderr: %s", code, stderr.String())
	}
}

func TestRunAgentMemoryReadSystem(t *testing.T) {
	withTempAgentHome(t)
	m, err := agent.OpenModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}

	var out, stderr bytes.Buffer
	if code := runAgentMemoryRead([]string{"--level", "system", "--json"}, &out, &stderr, false); code != 0 {
		t.Fatalf("code = %d, stderr: %s", code, stderr.String())
	}
	var body map[string]string
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out.String())
	}
	wantPath := filepath.Join(m.Root(), "memoria.md")
	if body["path"] != wantPath {
		t.Errorf("path = %q, want %q", body["path"], wantPath)
	}
}

func TestRunAgentMemoryReadNeedsLevel(t *testing.T) {
	withTempAgentHome(t)
	var out, stderr bytes.Buffer
	if code := runAgentMemoryRead(nil, &out, &stderr, false); code == 0 {
		t.Error("memory-read with no --level should fail")
	}
}

func TestRunAgentSessionPruneJSON(t *testing.T) {
	withTempAgentHome(t)
	var out, stderr bytes.Buffer
	if code := runAgentSessionPrune([]string{"--older-than", "30", "--json"}, &out, &stderr, false); code != 0 {
		t.Fatalf("code = %d, stderr: %s", code, stderr.String())
	}
	var body map[string]int
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out.String())
	}
	if body["removed"] != 0 {
		t.Errorf("removed = %d, want 0 on an empty state dir", body["removed"])
	}
}
