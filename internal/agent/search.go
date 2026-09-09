package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// `phi agent search`, phios-agente-delta.md D-06 / ADR 099. Searches ONLY the
// markdown files phi owns — transcript mirrors, archivio/, every level's
// memoria.md, and progetto.md — never opencode's private database. Results
// are grouped project → conversation ("smart hierarchy") and each hit says
// whether the query matched a title or the body.

type SearchHit struct {
	Project string // project name, or "_unfiled", or "" for memory/instructions
	Kind    string // "conversation" | "archive" | "memory" | "instructions"
	ID      string // conversation id / file stem
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

// Search runs the query. project == "" searches everything; otherwise it is
// scoped to that project (plus system/personality memory, which is always in
// scope because it is always in context).
func (m *Model) Search(query, project string) (SearchResults, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return SearchResults{}, nil
	}

	var hits []SearchHit

	// Conversation mirrors + archive.
	convProjects := m.searchProjects(project)
	for _, p := range convProjects {
		hits = append(hits, m.searchConversations(p, q)...)
		hits = append(hits, m.searchArchive(p, q)...)
	}

	// Memory: project level (scoped or all), plus system and every personality
	// (always in context, so always searchable).
	memProjects := convProjects
	for _, p := range memProjects {
		if p == unfiledProject {
			continue
		}
		hits = append(hits, m.searchMemoryFile(ProjectLevel(p), "memory", p, q)...)
		hits = append(hits, m.searchInstructions(p, q)...)
	}
	hits = append(hits, m.searchMemoryFile(SystemLevel(), "memory", "", q)...)
	if ps, _ := m.Personalities(); ps != nil {
		for _, pn := range ps {
			hits = append(hits, m.searchMemoryFile(PersonalityLevel(pn), "memory", "", q)...)
		}
	}

	return group(hits), nil
}

func (m *Model) searchProjects(project string) []string {
	if project != "" {
		return []string{project}
	}
	out := []string{unfiledProject}
	prj, _ := m.Projects()
	return append(out, prj...)
}

func (m *Model) searchConversations(project, q string) []SearchHit {
	metas, _ := m.ListTranscripts(project)
	var hits []SearchHit
	for _, meta := range metas {
		raw, err := m.ReadTranscript(project, meta.ID)
		if err != nil {
			continue
		}
		body := stripFrontMatter(raw)
		matchBody := body
		if h := strings.TrimPrefix(matchBody, "# "); h != matchBody {
			if nl := strings.IndexByte(h, '\n'); nl >= 0 {
				matchBody = h[nl+1:] // drop the echoed title heading
			}
		}
		inTitle := strings.Contains(strings.ToLower(meta.Title), q)
		inBody := strings.Contains(strings.ToLower(matchBody), q)
		if !inTitle && !inBody {
			continue
		}
		hits = append(hits, SearchHit{
			Project: project, Kind: "conversation", ID: meta.ID, Title: meta.Title,
			InTitle: inTitle, InBody: inBody,
			Snippet: snippet(body, q), Score: score(inTitle, inBody, meta.Title, q),
		})
	}
	return hits
}

func (m *Model) searchArchive(project, q string) []SearchHit {
	if project == unfiledProject {
		return nil
	}
	dir := filepath.Join(m.projectDir(project), "archivio")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var hits []SearchHit
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		text := string(b)
		lc := strings.ToLower(text)
		inTitle := strings.Contains(strings.ToLower(e.Name()), q)
		inBody := strings.Contains(lc, q)
		if !inTitle && !inBody {
			continue
		}
		hits = append(hits, SearchHit{
			Project: project, Kind: "archive", ID: e.Name(), Title: e.Name(),
			InTitle: inTitle, InBody: inBody,
			Snippet: snippet(text, q), Score: score(inTitle, inBody, e.Name(), q) - 5,
		})
	}
	return hits
}

func (m *Model) searchMemoryFile(l MemLevel, kind, project, q string) []SearchHit {
	text, err := m.MemoryText(l)
	if err != nil || strings.TrimSpace(text) == "" {
		return nil
	}
	if !strings.Contains(strings.ToLower(text), q) {
		return nil
	}
	return []SearchHit{{
		Project: project, Kind: kind, ID: l.String(), Title: "memoria.md (" + l.String() + ")",
		InBody: true, Snippet: snippet(text, q), Score: score(false, true, "", q) + 10,
	}}
}

func (m *Model) searchInstructions(project, q string) []SearchHit {
	text, err := m.ProjectInstructions(project)
	if err != nil || !strings.Contains(strings.ToLower(text), q) {
		return nil
	}
	return []SearchHit{{
		Project: project, Kind: "instructions", ID: "progetto.md", Title: "progetto.md",
		InBody: true, Snippet: snippet(text, q), Score: score(false, true, "", q),
	}}
}

// stripFrontMatter drops a leading "---\n…\n---\n" YAML block so a body match
// means a match in the actual conversation, not in the mirror's metadata.
func stripFrontMatter(text string) string {
	if !strings.HasPrefix(text, "---\n") {
		return text
	}
	if i := strings.Index(text[4:], "\n---"); i >= 0 {
		rest := text[4+i+4:]
		return strings.TrimLeft(rest, "-\n")
	}
	return text
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
		key := h.Project
		if key == "" {
			key = "_memory"
		}
		if _, seen := byProject[key]; !seen {
			order = append(order, key)
		}
		byProject[key] = append(byProject[key], h)
	}
	// Order projects by their best hit's score.
	sort.Slice(order, func(i, j int) bool {
		return bestScore(byProject[order[i]]) > bestScore(byProject[order[j]])
	})
	var res SearchResults
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
