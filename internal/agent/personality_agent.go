package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Personality memory injection, phios-agente-delta.md D-01 / §3.6.
//
// opencode's agent config takes a single `prompt`. To get a personality's
// memoria.md into context alongside its prompt WITHOUT letting the agent write
// memoria.md (ADR 094 — memoria.md stays a read-only mount), phi renders a
// combined `agent.md` = the user's prompt.md + a managed memory section drawn
// from memoria.md. The opencode `agent.<name>.prompt` points at agent.md, not
// prompt.md. phi regenerates agent.md whenever the prompt or the personality
// memory changes; the agent never touches it.
//
// This mirrors how progetto.md is regenerated from project.json — one plain
// file for the engine to read, kept in sync by the client.

const personalityMemoryMarker = "<!-- phi:personality-memory (generated — edit prompt.md and use `phi agent memory`) -->"

func (m *Model) personalityAgentPath(name string) string {
	return filepath.Join(m.personalityDir(name), "agent.md")
}

// syncPersonalityAgent (re)writes personalita/<name>/agent.md from the current
// prompt.md and memoria.md. A no-op if the personality has no directory yet.
func (m *Model) syncPersonalityAgent(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid personality name %q", name)
	}
	dir := m.personalityDir(name)
	if !dirExists(dir) {
		return nil
	}
	prompt, _ := readFileString(filepath.Join(dir, "prompt.md"))
	if prompt == "" {
		// Migration transition: prompt might still be the flat file.
		prompt, _ = readFileString(filepath.Join(m.personalitaDir(), name+".md"))
	}
	mem, _ := m.MemoryText(PersonalityLevel(name))

	var b strings.Builder
	b.WriteString(strings.TrimRight(prompt, "\n"))
	b.WriteString("\n\n")
	b.WriteString(personalityMemoryMarker)
	b.WriteString("\n## Your memory\n\n")
	if strings.TrimSpace(mem) == "" {
		b.WriteString("(No durable facts recorded yet.)\n")
	} else {
		b.WriteString(strings.TrimRight(stripHeading(mem), "\n"))
		b.WriteString("\n")
	}
	return os.WriteFile(m.personalityAgentPath(name), []byte(b.String()), 0o644)
}

// stripHeading drops a leading "# ..." line so the memory file's own title
// does not become a second top-level heading inside agent.md.
func stripHeading(text string) string {
	text = strings.TrimLeft(text, "\n")
	if strings.HasPrefix(text, "# ") {
		if nl := strings.IndexByte(text, '\n'); nl >= 0 {
			return strings.TrimLeft(text[nl+1:], "\n")
		}
		return ""
	}
	return text
}
