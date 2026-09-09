package view

import (
	"fmt"
	"strings"
)

// AgentProjectList renders `phi agent project list`.
func AgentProjectList(projects []string, active string, personalities []string) string {
	var b strings.Builder
	b.WriteString("personalities: ")
	if len(personalities) == 0 {
		b.WriteString("(none)\n")
	} else {
		b.WriteString(strings.Join(personalities, ", ") + "\n")
	}
	b.WriteString("projects:\n")
	if len(projects) == 0 {
		b.WriteString("  (none — create one with `phi agent project new NAME`)\n")
		return b.String()
	}
	for _, p := range projects {
		mark := "  "
		if p == active {
			mark = "* "
		}
		fmt.Fprintf(&b, "%s%s\n", mark, p)
	}
	return b.String()
}

// AgentMemoryList renders `phi agent memory list`. When not styled (output
// redirected — this is how the shell panel reads it) it emits exactly one
// bare proposal name per line and nothing else, so a name containing a space
// or a colon still reaches the reader intact. A proposal that exists on disk
// but never surfaces would be the one silent failure §8.6 cannot tolerate.
func AgentMemoryList(project string, proposals []string, styled bool) string {
	if !styled {
		var b strings.Builder
		for _, p := range proposals {
			b.WriteString(p)
			b.WriteByte('\n')
		}
		return b.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "project: %s\npending memory proposals:\n", project)
	if len(proposals) == 0 {
		b.WriteString("  (none)\n")
		return b.String()
	}
	for _, p := range proposals {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	b.WriteString("\nreview with `phi agent memory show FILE`, then accept or reject.\n")
	return b.String()
}

// AgentMemoryDiff shows the LITERAL text a proposal would add to memoria.md,
// as an append (phios-agente.md §8.6: never a summary — a summary would be
// produced by the same model that may have been manipulated).
func AgentMemoryDiff(name, currentMemory, proposalText string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "proposal: %s\n\n", name)
	b.WriteString("current memoria.md:\n")
	for _, line := range strings.Split(currentMemory, "\n") {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	b.WriteString("\nwould append (literal):\n")
	for _, line := range strings.Split(proposalText, "\n") {
		fmt.Fprintf(&b, "+ %s\n", line)
	}
	b.WriteString("\n`phi agent memory accept " + name + "` to write it, `reject` to discard.\n")
	return b.String()
}
