package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// `phi agent search`. Searches only phi-owned text: session transcripts
// (message text), every level's memoria.md, a project's generated
// instructions.md, and .md/.txt files under a project's allegati/. Never
// reads pi's own state beyond the .jsonl files chat.go already parses.

type SearchHit struct {
	Project string // "" for system-wide / unfiled
	Kind    string // "chat" | "memory" | "instructions" | "attachment"
	ID      string
	Title   string
	InTitle bool
	InBody  bool
	Snippet string
	Score   int
}

// SearchResults is hits grouped by project, each group ordered by score.
type SearchResults struct {
	Groups []SearchGroup
}

type SearchGroup struct {
	Project string
	Hits    []SearchHit
}

// Search runs the query. project == "" searches every project (plus system
// and profile memory, always in scope); otherwise it is scoped to that
// project (system and profile memory are still included).
func (m *Model) Search(query, project string) (SearchResults, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return SearchResults{Groups: []SearchGroup{}}, nil
	}

	var hits []SearchHit
	hits = append(hits, m.searchTranscripts(project, q)...)
	hits = append(hits, m.searchMemoryFile(SystemLevel(), "", q)...)
	for _, p := range MemoryProfiles() {
		hits = append(hits, m.searchMemoryFile(ProfileLevel(p), "", q)...)
	}

	projects := []string{project}
	if project == "" {
		projects, _ = m.Projects()
	}
	for _, p := range projects {
		if p == "" {
			continue
		}
		hits = append(hits, m.searchMemoryFile(ProjectLevel(p), p, q)...)
		hits = append(hits, m.searchInstructions(p, q)...)
		hits = append(hits, m.searchAttachments(p, q)...)
	}

	return group(hits), nil
}

func (m *Model) searchTranscripts(project, q string) []SearchHit {
	metas, err := m.ListTranscripts(project, false)
	if err != nil {
		return nil
	}
	var hits []SearchHit
	for _, meta := range metas {
		if meta.Path == "" {
			continue
		}
		messages, err := TranscriptMessages(meta.Path)
		if err != nil {
			continue
		}
		var body strings.Builder
		for _, msg := range messages {
			body.WriteString(msg.Text)
			body.WriteString("\n")
		}
		text := body.String()
		inTitle := strings.Contains(strings.ToLower(meta.Title), q)
		inBody := strings.Contains(strings.ToLower(text), q)
		if !inTitle && !inBody {
			continue
		}
		hits = append(hits, SearchHit{
			Project: meta.Project, Kind: "chat", ID: meta.ID, Title: meta.Title,
			InTitle: inTitle, InBody: inBody,
			Snippet: snippet(text, q), Score: score(inTitle, inBody, meta.Title, q),
		})
	}
	return hits
}

func (m *Model) searchMemoryFile(l MemLevel, project, q string) []SearchHit {
	text, err := m.MemoryText(l)
	if err != nil || strings.TrimSpace(text) == "" {
		return nil
	}
	if !strings.Contains(strings.ToLower(text), q) {
		return nil
	}
	return []SearchHit{{
		Project: project, Kind: "memory", ID: l.String(), Title: "memoria.md (" + l.String() + ")",
		InBody: true, Snippet: snippet(text, q), Score: score(false, true, "", q) + 10,
	}}
}

func (m *Model) searchInstructions(project, q string) []SearchHit {
	text, err := m.ProjectInstructionsText(project)
	if err != nil || !strings.Contains(strings.ToLower(text), q) {
		return nil
	}
	return []SearchHit{{
		Project: project, Kind: "instructions", ID: "instructions.md", Title: "instructions.md",
		InBody: true, Snippet: snippet(text, q), Score: score(false, true, "", q),
	}}
}

func (m *Model) searchAttachments(project, q string) []SearchHit {
	root := filepath.Join(m.projectDir(project), "allegati")
	var hits []SearchHit
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".txt" {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		text := string(b)
		rel, _ := filepath.Rel(root, path)
		lc := strings.ToLower(text)
		inTitle := strings.Contains(strings.ToLower(rel), q)
		inBody := strings.Contains(lc, q)
		if !inTitle && !inBody {
			return nil
		}
		hits = append(hits, SearchHit{
			Project: project, Kind: "attachment", ID: rel, Title: rel,
			InTitle: inTitle, InBody: inBody,
			Snippet: snippet(text, q), Score: score(inTitle, inBody, rel, q) - 5,
		})
		return nil
	})
	return hits
}

func score(inTitle, inBody bool, title, q string) int {
	s := 0
	if inTitle {
		s += 60
		if strings.EqualFold(strings.TrimSpace(title), q) {
			s += 40
		} else if strings.HasPrefix(strings.ToLower(title), q) {
			s += 20
		}
	}
	if inBody {
		s += 30
	}
	return s
}

func snippet(text, q string) string {
	lc := strings.ToLower(text)
	i := strings.Index(lc, q)
	if i < 0 {
		return ""
	}
	start := i - 60
	if start < 0 {
		start = 0
	}
	end := i + len(q) + 60
	if end > len(text) {
		end = len(text)
	}
	s := strings.ReplaceAll(text[start:end], "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if start > 0 {
		s = "…" + s
	}
	if end < len(text) {
		s = s + "…"
	}
	return s
}

func group(hits []SearchHit) SearchResults {
	byProject := map[string][]SearchHit{}
	var order []string
	for _, h := range hits {
		if _, seen := byProject[h.Project]; !seen {
			order = append(order, h.Project)
		}
		byProject[h.Project] = append(byProject[h.Project], h)
	}
	sort.Slice(order, func(i, j int) bool {
		return bestScore(byProject[order[i]]) > bestScore(byProject[order[j]])
	})
	res := SearchResults{Groups: []SearchGroup{}}
	for _, key := range order {
		g := byProject[key]
		sort.Slice(g, func(i, j int) bool { return g[i].Score > g[j].Score })
		res.Groups = append(res.Groups, SearchGroup{Project: key, Hits: g})
	}
	return res
}

func bestScore(hits []SearchHit) int {
	best := 0
	for _, h := range hits {
		if h.Score > best {
			best = h.Score
		}
	}
	return best
}
