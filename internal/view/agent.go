package view

import (
	"fmt"
	"sort"
	"strings"

	"phi/internal/agent"
)

// AgentProjectList renders `phi agent project list`.
func AgentProjectList(projects []agent.ProjectSummary) string {
	var b strings.Builder
	if len(projects) == 0 {
		b.WriteString("no projects — create one with `phi agent project new NAME`\n")
		return b.String()
	}
	for _, p := range projects {
		fmt.Fprintf(&b, "%-20s %s\n", p.Name, p.Title)
		if p.Description != "" {
			fmt.Fprintf(&b, "%-20s %s\n", "", p.Description)
		}
		fmt.Fprintf(&b, "%-20s default profile: %s\n", "", p.DefaultProfile)
	}
	return b.String()
}

// AgentProjectShow renders `phi agent project show NAME`.
func AgentProjectShow(name string, meta agent.ProjectMeta, dir, host string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n", name, meta.Title)
	fmt.Fprintf(&b, "dir: %s\n", dir)
	if meta.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", meta.Description)
	}
	fmt.Fprintf(&b, "default profile: %s\n", meta.DefaultProfile)
	if len(meta.Instructions) == 0 {
		b.WriteString("instructions: (none)\n")
	} else {
		b.WriteString("instructions:\n")
		for _, ins := range meta.Instructions {
			fmt.Fprintf(&b, "  - %s\n", ins)
		}
	}
	if len(meta.Folders) == 0 {
		b.WriteString("folders: (none)\n")
	} else {
		b.WriteString("folders:\n")
		for _, f := range meta.Folders {
			here := f.Paths[host]
			if here == "" {
				here = "(not mounted on this host)"
			}
			fmt.Fprintf(&b, "  %s (%s): %s\n", f.Name, f.Mode, here)
		}
	}
	return b.String()
}

// AgentMemoryList renders `phi agent memory list`. When not styled (output
// redirected — this is how the shell panel reads it) it emits exactly one
// bare proposal name per line and nothing else, so a name containing a space
// or a colon still reaches the reader intact. A proposal that exists on disk
// but never surfaces would be a silent failure.
func AgentMemoryList(level string, proposals []string, styled bool) string {
	if !styled {
		var b strings.Builder
		for _, p := range proposals {
			b.WriteString(p)
			b.WriteByte('\n')
		}
		return b.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "level: %s\npending memory proposals:\n", level)
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

// AgentMemoryListAll renders `phi agent memory list-all`.
func AgentMemoryListAll(all map[string][]string) string {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%-20s %d pending\n", k, len(all[k]))
	}
	return b.String()
}

// AgentChatList renders `phi agent chat list`.
func AgentChatList(metas []agent.TranscriptMeta) string {
	var b strings.Builder
	if len(metas) == 0 {
		b.WriteString("no chats\n")
		return b.String()
	}
	for _, m := range metas {
		mark := " "
		if m.Pinned {
			mark = "*"
		}
		proj := m.Project
		if proj == "" {
			proj = "(unfiled)"
		}
		fmt.Fprintf(&b, "%s %s  [%s/%s]  %s\n", mark, m.ID, proj, m.Profile, m.Title)
	}
	return b.String()
}

// AgentSearch renders `phi agent search`, grouped project -> hit. Each hit
// says whether the query matched a title or the body.
func AgentSearch(query string, res agent.SearchResults) string {
	var b strings.Builder
	fmt.Fprintf(&b, "search: %q\n", query)
	if len(res.Groups) == 0 {
		b.WriteString("  (no matches)\n")
		return b.String()
	}
	for _, g := range res.Groups {
		label := g.Project
		if label == "" {
			label = "(system / unfiled)"
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

// AgentSessionList renders `phi agent session list`.
func AgentSessionList(recs []agent.SessionRecord) string {
	var b strings.Builder
	if len(recs) == 0 {
		b.WriteString("no terminal sessions recorded\n")
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
// as an append (never a summary — a summary would be
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
