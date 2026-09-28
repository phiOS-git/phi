package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every `--json` list output must marshal an empty result as `[]`, never
// `null` (the shell panel and any other JSON consumer should never have to
// special-case the empty case). These hit the agent-package functions
// directly, the same ones internal/cli/agent.go marshals unchanged.

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEmptyChatListIsJSONArray(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	metas, err := m.ListTranscripts("", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshal(t, metas); got != "[]" {
		t.Errorf("ListTranscripts JSON = %q, want []", got)
	}
}

func TestEmptySessionListIsJSONArray(t *testing.T) {
	_ = testModel(t) // sets XDG_DATA_HOME/XDG_STATE_HOME/XDG_CONFIG_HOME to temp dirs
	recs, err := ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshal(t, recs); got != "[]" {
		t.Errorf("ListSessions JSON = %q, want []", got)
	}
}

func TestEmptyMemoryListIsJSONArray(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	props, err := m.Proposals(SystemLevel())
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshal(t, props); got != "[]" {
		t.Errorf("Proposals JSON = %q, want []", got)
	}
}

func TestEmptySearchGroupsIsJSONArray(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	res, err := m.Search("no-such-term-anywhere", "")
	if err != nil {
		t.Fatal(err)
	}
	got := mustMarshal(t, res)
	if !strings.Contains(got, `"Groups":[]`) {
		t.Errorf("Search JSON = %q, want Groups: []", got)
	}
}
