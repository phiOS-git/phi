package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSystemctl replaces systemctlOutput for the duration of the test with a
// canned answer keyed by "<verb> <unit>", restoring the real one after.
func stubSystemctl(t *testing.T, answers map[string]string) {
	t.Helper()
	orig := systemctlOutput
	systemctlOutput = func(args ...string) string {
		return answers[strings.Join(args, " ")]
	}
	t.Cleanup(func() { systemctlOutput = orig })
}

func TestUnitStatusUnknownOnNoAnswer(t *testing.T) {
	stubSystemctl(t, map[string]string{
		"is-active phi-agent.service":  "active",
		"is-enabled phi-agent.service": "enabled",
	})
	got := unitStatus("phi-agent.service")
	if got.Active != "active" || got.Enabled != "enabled" {
		t.Errorf("unitStatus = %+v", got)
	}
	got2 := unitStatus("phi-agent-proxy.service") // no stubbed answer -> ""
	if got2.Active != "unknown" || got2.Enabled != "unknown" {
		t.Errorf("unitStatus with no answer = %+v, want unknown/unknown", got2)
	}
}

func TestCollectStatusUnits(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stubSystemctl(t, map[string]string{
		"is-active phi-agent.service": "active",
	})
	st := CollectStatus()
	if len(st.Units) != len(agentUnits) {
		t.Fatalf("len(Units) = %d, want %d", len(st.Units), len(agentUnits))
	}
	if st.Units[0].Name != "phi-agent.service" || st.Units[0].Active != "active" {
		t.Errorf("Units[0] = %+v", st.Units[0])
	}
	for _, p := range []string{"general", "academic", "coding"} {
		if _, ok := st.Profiles[p]; !ok {
			t.Errorf("Profiles missing %q", p)
		}
	}
	for _, i := range []string{"a1", "a2"} {
		if _, ok := st.Brokers[i]; !ok {
			t.Errorf("Brokers missing %q", i)
		}
	}
}

func TestLoadProfileModelsPresentNeverLeaksKey(t *testing.T) {
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "phi-agent", "pi", "profiles", "general")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `{"providers":{"anthropic":{"name":"Anthropic","baseUrl":"https://api.anthropic.com","apiKey":"secret-value","models":[{"id":"claude-sonnet"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	pm := loadProfileModels(cfg, General)
	if !pm.Present {
		t.Fatal("Present = false, want true")
	}
	if len(pm.Providers) != 1 || pm.Providers[0].Name != "Anthropic" || pm.Providers[0].BaseURL != "https://api.anthropic.com" {
		t.Fatalf("Providers = %+v", pm.Providers)
	}
	if len(pm.Providers[0].Models) != 1 || pm.Providers[0].Models[0] != "claude-sonnet" {
		t.Fatalf("Models = %+v", pm.Providers[0].Models)
	}

	b, err := json.Marshal(pm)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-value") {
		t.Error("loadProfileModels leaked the API key into its output")
	}
}

func TestLoadProfileModelsMissingFile(t *testing.T) {
	pm := loadProfileModels(t.TempDir(), Academic)
	if pm.Present {
		t.Error("Present = true for a missing models.json")
	}
}

func TestLoadBrokerSummary(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, err := A1.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"listen":"127.0.0.1:8789","upstream":"https://api.example.com","auth_header":"Authorization",` +
		`"auth_value":"Bearer {key}","rate_limit":{"requests":60,"window_seconds":60}}`
	if err := os.WriteFile(filepath.Join(dir, "broker.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// No key file yet: configured, but key not present.
	sum := loadBrokerSummary(A1)
	if !sum.Configured {
		t.Fatal("Configured = false, want true")
	}
	if sum.KeyPresent {
		t.Error("KeyPresent = true before a key file exists")
	}
	if sum.RateLimit != "60 req / 60s" {
		t.Errorf("RateLimit = %q", sum.RateLimit)
	}

	if err := os.WriteFile(filepath.Join(dir, "provider-key"), []byte("sk-abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum2 := loadBrokerSummary(A1)
	if !sum2.KeyPresent {
		t.Error("KeyPresent = false after writing a key file")
	}

	// A2 has no broker.json at all: not configured.
	sum3 := loadBrokerSummary(A2)
	if sum3.Configured {
		t.Error("Configured = true for an unconfigured instance")
	}
}

func TestCountWhitelistEntries(t *testing.T) {
	cfg := t.TempDir()
	if got := countWhitelistEntries(cfg); got != -1 {
		t.Errorf("missing whitelist = %d, want -1", got)
	}
	dir := filepath.Join(cfg, "phi-agent", "tinyproxy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# comment\n\nexample.com\napi.example.com\n  \n"
	if err := os.WriteFile(filepath.Join(dir, "whitelist"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := countWhitelistEntries(cfg); got != 2 {
		t.Errorf("whitelist entries = %d, want 2", got)
	}
}

func TestRecentBrokerRequestsOrderAndLimit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	sd, err := A1.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, date := range []string{"2026-09-28", "2026-09-29", "2026-09-30"} {
		lines = append(lines, `{"time":"`+date+`T00:00:00Z","instance":"a1","method":"POST","path":"/v1/messages","status":200,"model":"m","dur_ms":10,"resp_bytes":5,"rate_limited":false}`)
	}
	if err := os.WriteFile(filepath.Join(sd, "broker-meter.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := RecentBrokerRequests(A1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Newest first: the last two lines written, most recent first.
	if !strings.HasPrefix(got[0].Time, "2026-09-30") || !strings.HasPrefix(got[1].Time, "2026-09-29") {
		t.Errorf("order = [%s %s], want [30 29]", got[0].Time, got[1].Time)
	}
}

func TestRecentBrokerRequestsMissingFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	got, err := RecentBrokerRequests(A2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
