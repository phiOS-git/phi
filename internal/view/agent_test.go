package view

import "testing"

// The panel reads `phi agent memory list` redirected (not a TTY). Every
// proposal must come back as one intact line, whatever characters the agent
// put in the file name — a proposal that silently disappears is the failure
// §8.6 forbids.
func TestAgentMemoryListStructured(t *testing.T) {
	props := []string{"2026-09-09 durable fact.md", "adr:094 note.md", "plain.md"}
	got := AgentMemoryList("study", props, false)
	want := "2026-09-09 durable fact.md\nadr:094 note.md\nplain.md\n"
	if got != want {
		t.Errorf("structured output\n got %q\nwant %q", got, want)
	}
	if AgentMemoryList("study", nil, false) != "" {
		t.Errorf("no proposals should produce empty structured output")
	}
}

func TestAgentMemoryListStyled(t *testing.T) {
	got := AgentMemoryList("study", []string{"a.md"}, true)
	if got == "a.md\n" || len(got) == 0 {
		t.Errorf("styled output should be decorated, got %q", got)
	}
}
