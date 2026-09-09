package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Client-side transcript mirror, phios-agente-delta.md D-05. The shell client
// (Services/Agent.qml) writes each conversation's full transcript as markdown
// into `projects/<name>/conversazioni/<id>.md`, from the opencode HTTP
// responses it already receives. This is engine-independent (ADR 098): if
// opencode is replaced, the mirror files stay. It is the dashboard's chat
// source and the corpus for `phi agent search` — no query ever touches
// opencode's private database (ADR 099 §10.1).
//
// Conversations not attached to a project are mirrored under a reserved
// pseudo-project name so the dashboard can still list and search them.

const unfiledProject = "_unfiled"

// TranscriptMeta is the front-matter of a mirrored conversation.
type TranscriptMeta struct {
	ID      string
	Title   string
	Project string
	Pinned  bool
	Updated time.Time
}

func (m *Model) conversazioniDir(project string) string {
	if project == "" || project == unfiledProject {
		return filepath.Join(m.root, "conversazioni")
	}
	return filepath.Join(m.projectDir(project), "conversazioni")
}

func (m *Model) transcriptPath(project, id string) (string, error) {
	if err := checkSegment(id); err != nil {
		return "", err
	}
	return filepath.Join(m.conversazioniDir(project), id+".md"), nil
}

// WriteTranscript stores or replaces a conversation's mirror. body is the
// already-rendered markdown transcript (the client builds it from
// GET /session/:id/message). Pin state is preserved across rewrites.
func (m *Model) WriteTranscript(project, id, title, body string) error {
	if project != "" && project != unfiledProject && !m.HasProject(project) {
		return fmt.Errorf("no such project: %q", project)
	}
	path, err := m.transcriptPath(project, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	pinned := false
	if prev, e := m.readTranscriptMeta(path); e == nil {
		pinned = prev.Pinned
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", id)
	fmt.Fprintf(&b, "title: %s\n", strings.ReplaceAll(title, "\n", " "))
	fmt.Fprintf(&b, "project: %s\n", orString(project, unfiledProject))
	fmt.Fprintf(&b, "pinned: %t\n", pinned)
	fmt.Fprintf(&b, "updated: %s\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("---\n\n")
	if title != "" {
		fmt.Fprintf(&b, "# %s\n\n", title)
	}
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// ReadTranscript returns the raw markdown of one mirrored conversation.
func (m *Model) ReadTranscript(project, id string) (string, error) {
	path, err := m.transcriptPath(project, id)
	if err != nil {
		return "", err
	}
	return readFileString(path)
}

// TranscriptByID finds a mirrored conversation across all projects and the
// unfiled bucket, returning its owning project and raw markdown.
func (m *Model) TranscriptByID(id string) (project, markdown string, err error) {
	project, path, err := m.findTranscript(id)
	if err != nil {
		return "", "", err
	}
	markdown, err = readFileString(path)
	return project, markdown, err
}

func (m *Model) readTranscriptMeta(path string) (TranscriptMeta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return TranscriptMeta{}, err
	}
	return parseTranscriptMeta(string(b)), nil
}

func parseTranscriptMeta(text string) TranscriptMeta {
	meta := TranscriptMeta{}
	if !strings.HasPrefix(text, "---\n") {
		return meta
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return meta
	}
	for _, line := range strings.Split(text[4:4+end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "id":
			meta.ID = v
		case "title":
			meta.Title = v
		case "project":
			meta.Project = v
		case "pinned":
			meta.Pinned = v == "true"
		case "updated":
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				meta.Updated = t
			}
		}
	}
	return meta
}

// ListTranscripts returns the mirrored conversations for one project, or for
// every project plus the unfiled bucket when project == "".
func (m *Model) ListTranscripts(project string) ([]TranscriptMeta, error) {
	var dirs []string
	if project != "" {
		dirs = []string{project}
	} else {
		dirs = append(dirs, unfiledProject)
		prj, _ := m.Projects()
		dirs = append(dirs, prj...)
	}
	var out []TranscriptMeta
	for _, p := range dirs {
		d := m.conversazioniDir(p)
		entries, err := os.ReadDir(d)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			meta, err := m.readTranscriptMeta(filepath.Join(d, e.Name()))
			if err != nil {
				continue
			}
			if meta.ID == "" {
				meta.ID = strings.TrimSuffix(e.Name(), ".md")
			}
			if meta.Project == "" {
				meta.Project = p
			}
			out = append(out, meta)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out, nil
}

// findTranscript locates a conversation across projects by id.
func (m *Model) findTranscript(id string) (project, path string, err error) {
	if err := checkSegment(id); err != nil {
		return "", "", err
	}
	candidates := []string{unfiledProject}
	prj, _ := m.Projects()
	candidates = append(candidates, prj...)
	for _, p := range candidates {
		cand := filepath.Join(m.conversazioniDir(p), id+".md")
		if fileExists(cand) {
			return p, cand, nil
		}
	}
	return "", "", fmt.Errorf("no mirrored conversation %q", id)
}

// SetTranscriptPin toggles the pinned flag in a conversation's front-matter,
// and mirrors the flag into the owning project's project.json pins.
func (m *Model) SetTranscriptPin(id string, pinned bool) error {
	project, path, err := m.findTranscript(id)
	if err != nil {
		return err
	}
	text, err := readFileString(path)
	if err != nil {
		return err
	}
	text = replaceFrontMatterField(text, "pinned", fmt.Sprintf("%t", pinned))
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return err
	}
	if project != "" && project != unfiledProject && m.HasProject(project) {
		_ = m.SetProjectPin(project, id, pinned)
	}
	return nil
}

// SetTranscriptTitle rewrites the title in the front-matter and the first
// heading.
func (m *Model) SetTranscriptTitle(id, title string) error {
	_, path, err := m.findTranscript(id)
	if err != nil {
		return err
	}
	text, err := readFileString(path)
	if err != nil {
		return err
	}
	title = strings.ReplaceAll(strings.TrimSpace(title), "\n", " ")
	text = replaceFrontMatterField(text, "title", title)
	if i := strings.Index(text, "\n# "); i >= 0 {
		nl := strings.IndexByte(text[i+1:], '\n')
		if nl >= 0 {
			text = text[:i+1] + "# " + title + text[i+1+nl:]
		}
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func replaceFrontMatterField(text, key, value string) string {
	if !strings.HasPrefix(text, "---\n") {
		return text
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return text
	}
	head := text[4 : 4+end]
	rest := text[4+end:]
	lines := strings.Split(head, "\n")
	found := false
	for i, line := range lines {
		if k, _, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == key {
			lines[i] = key + ": " + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+": "+value)
	}
	return "---\n" + strings.Join(lines, "\n") + rest
}
