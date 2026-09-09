package view

import (
	"fmt"
	"strings"

	"phi/internal/agent"
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

// AgentSearch renders `phi agent search`, grouped project -> conversation
// (phios-agente-delta.md D-06). Each hit says whether the query matched a
// title or the body.
func AgentSearch(query string, res agent.SearchResults) string {
	var b strings.Builder
	fmt.Fprintf(&b, "search: %q\n", query)
	if len(res.Groups) == 0 {
		b.WriteString("  (no matches)\n")
		return b.String()
	}
	for _, g := range res.Groups {
		label := g.Project
		if label == "_unfiled" {
			label = "(unfiled)"
		} else if label == "_memory" {
			label = "(memory & instructions)"
		}
		fmt.Fprintf(&b, "\n%s\n", label)
		for _, h := range g.Hits {
			where := "body"
			if h.InTitle && h.InBody {
				where = "title+body"
			} else if h.InTitle {
				where = "title"
			}
			fmt.Fprintf(&b, "  [%s] %s  (%s)\n", h.Kind, h.Title, where)
			if h.Snippet != "" {
				fmt.Fprintf(&b, "      %s\n", h.Snippet)
			}
		}
	}
	return b.String()
}

// AgentSessionList renders `phi agent session list` (delta D-07).
func AgentSessionList(recs []agent.SessionRecord) string {
	var b strings.Builder
	if len(recs) == 0 {
		b.WriteString("no coding sessions recorded\n")
		return b.String()
	}
	for _, r := range recs {
		fmt.Fprintf(&b, "%s  %-7s  %s\n", r.ID, r.Status, r.Dir)
		if !r.Started.IsZero() {
			fmt.Fprintf(&b, "        started %s", r.Started.Local().Format("2006-01-02 15:04"))
			if r.Status == "ended" && !r.Ended.IsZero() {
				fmt.Fprintf(&b, ", ended %s", r.Ended.Local().Format("2006-01-02 15:04"))
			}
			b.WriteByte('\n')
		}
	}
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
