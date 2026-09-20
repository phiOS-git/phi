package agent

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The agent data model (A1 only) with personalities (system prompts) and
// projects (instructions, materials, memory, archive, transcripts, proposals).
// Personalities: <A1 data dir>/personalita/<name>/{prompt.md, memoria.md}.
// Projects: <A1 data dir>/projects/<name>/{project.json, progetto.md,
// materiali/, archivio/, conversazioni/, memoria.md, proposte/, output/}.
// Italian inner names kept for compatibility.

//go:embed seeds/general.md seeds/technical.md
var seeds embed.FS

// projectSubdirs: created for every project.
var projectSubdirs = []string{"materiali", "archivio", "conversazioni", "proposte", "output"}

// personalitySubdirs: created for every personality.
var personalitySubdirs = []string{"proposte"}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// validName validates personality or project names (path-safe, 1-64 chars).
func validName(kind, name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid %s name %q: use lowercase letters, digits, '.', '_', '-' (1-64 chars)", kind, name)
	}
	return nil
}

// Model is A1's data model.
type Model struct {
	root string // <A1 data dir>
}

// OpenModel returns A1's data model, creating the root if needed.
func OpenModel() (*Model, error) {
	root, err := A1.DataDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Model{root: root}, nil
}

func (m *Model) Root() string           { return m.root }
func (m *Model) personalitaDir() string { return filepath.Join(m.root, "personalita") }
func (m *Model) projectsDir() string    { return filepath.Join(m.root, "projects") }
func (m *Model) personalityDir(n string) string {
	return filepath.Join(m.personalitaDir(), n)
}
func (m *Model) projectDir(name string) string {
	return filepath.Join(m.projectsDir(), name)
}

// activeMarkerPath: runtime project selection (in STATE, not DATA).
func activeMarkerPath() (string, error) {
	sd, err := A1.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sd, "active-project"), nil
}

// Ensure creates skeleton and seed personalities, migrates old layout
// (idempotent, preserves user prompts).
func (m *Model) Ensure() (created []string, err error) {
	if err := os.MkdirAll(m.personalitaDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.projectsDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.systemProposteDir(), 0o755); err != nil {
		return nil, err
	}

	migrated, err := m.migratePersonalities()
	if err != nil {
		return nil, err
	}
	created = append(created, migrated...)

	for _, p := range []string{"general", "technical"} {
		dir := m.personalityDir(p)
		prompt := filepath.Join(dir, "prompt.md")
		if _, statErr := os.Stat(prompt); statErr == nil {
			continue
		}
		data, readErr := seeds.ReadFile("seeds/" + p + ".md")
		if readErr != nil {
			return created, readErr
		}
		if mkErr := m.ensurePersonalityDir(p); mkErr != nil {
			return created, mkErr
		}
		if writeErr := os.WriteFile(prompt, data, 0o644); writeErr != nil {
			return created, writeErr
		}
		created = append(created, prompt)
	}
	if names, _ := m.Personalities(); names != nil {
		for _, n := range names {
			_ = m.syncPersonalityAgent(n)
		}
	}
	return created, nil
}

// migratePersonalities moves any `personalita/<name>.md` to
// `personalita/<name>/prompt.md` once. A name that already has a directory is
// left alone.
func (m *Model) migratePersonalities() (moved []string, err error) {
	entries, err := os.ReadDir(m.personalitaDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if !nameRE.MatchString(name) {
			continue
		}
		dir := m.personalityDir(name)
		if _, statErr := os.Stat(dir); statErr == nil {
			continue // already a directory; leave the stray file
		}
		if mkErr := m.ensurePersonalityDir(name); mkErr != nil {
			return moved, mkErr
		}
		src := filepath.Join(m.personalitaDir(), e.Name())
		dst := filepath.Join(dir, "prompt.md")
		if renErr := os.Rename(src, dst); renErr != nil {
			return moved, renErr
		}
		_ = m.syncPersonalityAgent(name)
		moved = append(moved, dst)
	}
	return moved, nil
}

func (m *Model) ensurePersonalityDir(name string) error {
	if err := os.MkdirAll(m.personalityDir(name), 0o755); err != nil {
		return err
	}
	for _, sub := range personalitySubdirs {
		if err := os.MkdirAll(filepath.Join(m.personalityDir(name), sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Personalities lists personality names (from directories or flat files).
func (m *Model) Personalities() ([]string, error) {
	entries, err := os.ReadDir(m.personalitaDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			if fileExists(filepath.Join(m.personalityDir(e.Name()), "prompt.md")) && !seen[e.Name()] {
				seen[e.Name()] = true
				out = append(out, e.Name())
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".md") {
			n := strings.TrimSuffix(e.Name(), ".md")
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// PersonalityPromptPath returns the prompt file for a personality.
func (m *Model) PersonalityPromptPath(name string) string {
	dirPrompt := filepath.Join(m.personalityDir(name), "prompt.md")
	if fileExists(dirPrompt) {
		return dirPrompt
	}
	flat := filepath.Join(m.personalitaDir(), name+".md")
	if fileExists(flat) {
		return flat
	}
	return dirPrompt
}

// PersonalityPrompt returns a personality's system prompt text.
func (m *Model) PersonalityPrompt(name string) (string, error) {
	if err := validName("personality", name); err != nil {
		return "", err
	}
	return readFileString(m.PersonalityPromptPath(name))
}

// HasPersonality reports if a personality exists.
func (m *Model) HasPersonality(name string) bool {
	if !nameRE.MatchString(name) {
		return false
	}
	return fileExists(m.PersonalityPromptPath(name))
}

// WritePersonality creates or replaces a personality's system prompt.
func (m *Model) WritePersonality(name, prompt string) error {
	if err := validName("personality", name); err != nil {
		return err
	}
	if err := m.ensurePersonalityDir(name); err != nil {
		return err
	}
	if !strings.HasSuffix(prompt, "\n") {
		prompt += "\n"
	}
	if err := os.WriteFile(filepath.Join(m.personalityDir(name), "prompt.md"), []byte(prompt), 0o644); err != nil {
		return err
	}
	return m.syncPersonalityAgent(name)
}

// RenamePersonality moves a personality to a new name.
func (m *Model) RenamePersonality(oldName, newName string) error {
	if err := validName("personality", oldName); err != nil {
		return err
	}
	if err := validName("personality", newName); err != nil {
		return err
	}
	if !m.HasPersonality(oldName) {
		return fmt.Errorf("no such personality: %q", oldName)
	}
	if m.HasPersonality(newName) {
		return fmt.Errorf("personality %q already exists", newName)
	}
	// Migrate to directory layout if needed.
	if !dirExists(m.personalityDir(oldName)) {
		if err := m.ensurePersonalityDir(oldName); err != nil {
			return err
		}
		if err := os.Rename(
			filepath.Join(m.personalitaDir(), oldName+".md"),
			filepath.Join(m.personalityDir(oldName), "prompt.md"),
		); err != nil {
			return err
		}
	}
	if err := os.Rename(m.personalityDir(oldName), m.personalityDir(newName)); err != nil {
		return err
	}
	return m.syncPersonalityAgent(newName)
}

// DeletePersonality removes a personality (refuses if it's a project default).
func (m *Model) DeletePersonality(name string) error {
	if err := validName("personality", name); err != nil {
		return err
	}
	if !m.HasPersonality(name) {
		return fmt.Errorf("no such personality: %q", name)
	}
	projects, _ := m.Projects()
	for _, p := range projects {
		if meta, err := m.LoadProjectMeta(p); err == nil && meta.DefaultPersonality == name {
			return fmt.Errorf("personality %q is the default of project %q — change it first", name, p)
		}
	}
	_ = os.Remove(filepath.Join(m.personalitaDir(), name+".md"))
	return os.RemoveAll(m.personalityDir(name))
}

// Projects lists all project names.
func (m *Model) Projects() ([]string, error) {
	entries, err := os.ReadDir(m.projectsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// NewProject creates a project directory (does not switch to it).
func (m *Model) NewProject(name string, meta ProjectMeta) error {
	if err := validName("project", name); err != nil {
		return err
	}
	dir := m.projectDir(name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("project %q already exists", name)
	}
	for _, sub := range projectSubdirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
	}
	meta.normalise(name)
	if err := m.SaveProjectMeta(name, meta); err != nil {
		return err
	}
	// memoria.md starts empty (agent read-only, written via `memory accept`).
	if err := os.WriteFile(filepath.Join(dir, "memoria.md"),
		[]byte("# Memory — project "+name+"\n\nDurable facts for this project. Written only by `phi agent memory accept --level project`.\n"),
		0o644); err != nil {
		return err
	}
	return nil
}

// HasProject reports if a project directory exists.
func (m *Model) HasProject(name string) bool {
	fi, err := os.Stat(m.projectDir(name))
	return err == nil && fi.IsDir()
}

// ActiveProject reads the active-project marker ("" if none).
func (m *Model) ActiveProject() (string, error) {
	p, err := activeMarkerPath()
	if err != nil {
		return "", err
	}
	v, err := readTrimmedFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return v, err
}

// SetActiveProject sets the active project marker.
func (m *Model) SetActiveProject(name string) error {
	if !m.HasProject(name) {
		return fmt.Errorf("no such project: %q (create it with `phi agent project new %s`)", name, name)
	}
	p, err := activeMarkerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(name+"\n"), 0o644)
}

// ClearActiveProject removes the active project marker.
func (m *Model) ClearActiveProject() error {
	p, err := activeMarkerPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ProjectInstructions returns a project's progetto.md (empty if absent).
func (m *Model) ProjectInstructions(project string) (string, error) {
	s, err := readFileString(filepath.Join(m.projectDir(project), "progetto.md"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return s, err
}

// checkSegment rejects a name that is not a single path segment. The name
// arrives straight from a CLI argument or the shell panel; filepath.Join would
// otherwise resolve "../../etc/passwd" out of the target directory.
func checkSegment(name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return fmt.Errorf("invalid name %q: must be a single file name", name)
	}
	return nil
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
