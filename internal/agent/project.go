package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Structured project metadata, phios-agente-delta.md D-04. project.json is the
// client's to own; progetto.md is derived from it so the engine keeps reading
// one plain file (§8.5). folders[] are real host directories mounted READ-ONLY
// into A1's per-project perimeter (§4.3.1, D-02) — never copied.

// ProjectMeta is the on-disk project.json.
type ProjectMeta struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Instructions       []string `json:"instructions"`
	DefaultPersonality string   `json:"default_personality"`
	Folders            []string `json:"folders"` // absolute host paths, read-only mounts
	Pins               []string `json:"pins"`    // pinned conversation ids
}

func (p *ProjectMeta) normalise(name string) {
	if p.Title == "" {
		p.Title = name
	}
	if p.DefaultPersonality == "" {
		p.DefaultPersonality = "general"
	}
	p.Folders = dedupeStrings(p.Folders)
	p.Pins = dedupeStrings(p.Pins)
	p.Instructions = trimEmpty(p.Instructions)
}

func (m *Model) projectMetaPath(name string) string {
	return filepath.Join(m.projectDir(name), "project.json")
}

// LoadProjectMeta reads project.json. A project created before D-04 has none;
// this returns a zero-value meta with the name as title in that case.
func (m *Model) LoadProjectMeta(name string) (ProjectMeta, error) {
	if !m.HasProject(name) {
		return ProjectMeta{}, fmt.Errorf("no such project: %q", name)
	}
	b, err := os.ReadFile(m.projectMetaPath(name))
	if errors.Is(err, os.ErrNotExist) {
		meta := ProjectMeta{}
		meta.normalise(name)
		return meta, nil
	}
	if err != nil {
		return ProjectMeta{}, err
	}
	var meta ProjectMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return ProjectMeta{}, fmt.Errorf("project.json for %q: %w", name, err)
	}
	meta.normalise(name)
	return meta, nil
}

// SaveProjectMeta writes project.json and regenerates progetto.md from it.
func (m *Model) SaveProjectMeta(name string, meta ProjectMeta) error {
	if err := validName("project", name); err != nil {
		return err
	}
	meta.normalise(name)
	for _, f := range meta.Folders {
		if err := ValidateFolderOfInterest(f); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(m.projectDir(name), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(m.projectMetaPath(name), b, 0o644); err != nil {
		return err
	}
	// folders.list — a plain one-path-per-line sidecar of meta.Folders, so
	// phi-agent-contain (bash + coreutils only, no jq) can read the
	// folders-of-interest without parsing JSON.
	if err := os.WriteFile(filepath.Join(m.projectDir(name), "folders.list"),
		[]byte(strings.Join(meta.Folders, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.projectDir(name), "progetto.md"),
		[]byte(renderProgettoMD(name, meta)), 0o644)
}

// renderProgettoMD builds the plain-text instructions file the engine reads,
// from the structured metadata (§8.5: the engine always reads one file).
func renderProgettoMD(name string, meta ProjectMeta) string {
	var b strings.Builder
	title := meta.Title
	if title == "" {
		title = name
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	b.WriteString("<!-- Generated from project.json by `phi agent project`. Do not edit by hand. -->\n\n")

	b.WriteString("## Default personality\n\n")
	fmt.Fprintf(&b, "%s\n\n", orString(meta.DefaultPersonality, "general"))

	b.WriteString("## About this project\n\n")
	if strings.TrimSpace(meta.Description) != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(meta.Description))
	} else {
		b.WriteString("(No description set.)\n\n")
	}

	if len(meta.Instructions) > 0 {
		b.WriteString("## Instructions\n\n")
		for _, ins := range meta.Instructions {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(ins))
		}
		b.WriteString("\n")
	}

	if len(meta.Folders) > 0 {
		b.WriteString("## Folders of interest (read-only)\n\n")
		b.WriteString("Real directories mounted read-only for this project. You may read them; ")
		b.WriteString("you cannot modify them — write results into `output/`.\n\n")
		for i, f := range meta.Folders {
			fmt.Fprintf(&b, "- `%s` → `/home/agent/folders/%d`\n", f, i)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ProjectFolders returns a project's folders-of-interest.
func (m *Model) ProjectFolders(name string) ([]string, error) {
	meta, err := m.LoadProjectMeta(name)
	if err != nil {
		return nil, err
	}
	return meta.Folders, nil
}

// AddProjectFolder validates and adds a folder-of-interest.
func (m *Model) AddProjectFolder(name, path string) error {
	abs, err := resolveDir(path)
	if err != nil {
		return err
	}
	if err := ValidateFolderOfInterest(abs); err != nil {
		return err
	}
	meta, err := m.LoadProjectMeta(name)
	if err != nil {
		return err
	}
	meta.Folders = dedupeStrings(append(meta.Folders, abs))
	return m.SaveProjectMeta(name, meta)
}

// RemoveProjectFolder drops a folder-of-interest by its path.
func (m *Model) RemoveProjectFolder(name, path string) error {
	meta, err := m.LoadProjectMeta(name)
	if err != nil {
		return err
	}
	abs := path
	if r, e := resolveDir(path); e == nil {
		abs = r
	}
	var kept []string
	for _, f := range meta.Folders {
		if f != path && f != abs {
			kept = append(kept, f)
		}
	}
	meta.Folders = kept
	return m.SaveProjectMeta(name, meta)
}

// SetProjectPin adds or removes a conversation id from a project's pins.
func (m *Model) SetProjectPin(name, convID string, pinned bool) error {
	if err := checkSegment(convID); err != nil {
		return err
	}
	meta, err := m.LoadProjectMeta(name)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	for _, p := range meta.Pins {
		set[p] = true
	}
	if pinned {
		set[convID] = true
	} else {
		delete(set, convID)
	}
	pins := make([]string, 0, len(set))
	for p := range set {
		pins = append(pins, p)
	}
	sort.Strings(pins)
	meta.Pins = pins
	return m.SaveProjectMeta(name, meta)
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func trimEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func orString(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// resolveDir expands ~, makes the path absolute, resolves symlinks, and
// checks it is a directory.
func resolveDir(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty path")
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = home + path[1:]
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}
