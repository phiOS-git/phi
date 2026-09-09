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

// The data model of phios-agente.md §8.2, revised by phios-agente-delta.md
// §3.5, on disk under A1's XDG data directory. Two orthogonal axes:
// personalities (a system prompt the user owns) and projects (a directory of
// instructions, materials, memory, archive, transcripts, proposals, output).
// A1 only — A2 has no memory and no project notion in this sense (§8.6,
// ADR 096).
//
//	<A1 data dir>/
//	  memoria.md                    system memory        [agent: read-only]
//	  proposte/                     system proposals     [agent: write]
//	  personalita/<name>/
//	    prompt.md                   system prompt — user writes, agent reads
//	    memoria.md                  personality memory   [agent: read-only]
//	    proposte/                   personality proposals[agent: write]
//	  projects/<name>/
//	    project.json                structured metadata — the client owns it
//	    progetto.md                 instructions — regenerated from project.json
//	    materiali/                  [agent: read-only]
//	    archivio/                   [agent: read-only]
//	    conversazioni/              transcript mirror    [agent: read-only]
//	    memoria.md                  project memory       [agent: read-only]
//	    proposte/                   project proposals    [agent: write]
//	    output/                     [agent: write]
//
// The italian inner names (progetto.md, materiali/, archivio/, proposte/,
// memoria.md) and the english outer `projects/` are kept as the code already
// had them: renaming re-invalidates V-05/V-06 for no functional gain.

//go:embed seeds/general.md seeds/technical.md
var seeds embed.FS

// projectSubdirs are created for every project. proposte/ and output/ are the
// only two the containment mounts writable inside the project tree (§4.3).
var projectSubdirs = []string{"materiali", "archivio", "conversazioni", "proposte", "output"}

// personalitySubdirs are created for every personality directory.
var personalitySubdirs = []string{"proposte"}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// validName guards every user-supplied personality or project name: it becomes
// a single path segment inside the data dir and, for a project, a mount
// destination.
func validName(kind, name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid %s name %q: use lowercase letters, digits, '.', '_', '-' (1-64 chars)", kind, name)
	}
	return nil
}

// Model is A1's data model rooted at its XDG data dir.
type Model struct {
	root string // <A1 data dir>
}

// OpenModel returns A1's data model, creating the root if needed but not the
// seed content (call Ensure for that).
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

// activeMarkerPath is the file phi-agent-contain reads (as a fallback to
// $PHI_AGENT_PROJECT) to know which project to mount. It lives in STATE, not
// DATA: it is runtime selection, not model content.
func activeMarkerPath() (string, error) {
	sd, err := A1.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sd, "active-project"), nil
}

// Ensure creates the §8.2 skeleton and the two seed personalities if they are
// absent, and migrates the pre-delta flat `personalita/<name>.md` layout to
// `personalita/<name>/prompt.md`. Idempotent: existing files are never
// overwritten (the user owns the prompts). This is §8.3's "minimal initial
// state for a clean install", not a reset.
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

// Personalities lists the personality names. A name counts if it is a
// directory holding prompt.md, or (transition) a bare `<name>.md`.
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

// PersonalityPromptPath returns the prompt file for a personality, preferring
// the directory layout and falling back to the flat file.
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

// PersonalityPrompt returns the system-prompt text of a personality.
func (m *Model) PersonalityPrompt(name string) (string, error) {
	if err := validName("personality", name); err != nil {
		return "", err
	}
	return readFileString(m.PersonalityPromptPath(name))
}

// HasPersonality reports whether a personality exists.
func (m *Model) HasPersonality(name string) bool {
	if !nameRE.MatchString(name) {
		return false
	}
	return fileExists(m.PersonalityPromptPath(name))
}

// WritePersonality creates or replaces a personality's system prompt. The
// panel calls this (it runs outside the containment and is the user, §8.2 /
// D-08); `personalita/` stays read-only inside the mount.
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

// RenamePersonality moves a personality directory (prompt + its memory and
// proposals) to a new name.
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
	// Normalise the old one to the directory layout first.
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

// DeletePersonality removes a personality and everything under it. It refuses
// to delete a personality that is the default of an existing project.
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

// Projects lists the project names.
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

// NewProject creates a project directory with a fresh project.json and a
// rendered progetto.md. It does not switch to it.
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
	// memoria.md starts empty. It is always in context (§8.5) and the agent
	// can never write it (§8.4) — only `phi agent memory accept` appends here.
	if err := os.WriteFile(filepath.Join(dir, "memoria.md"),
		[]byte("# Memory — project "+name+"\n\nDurable facts for this project. Written only by `phi agent memory accept --level project`.\n"),
		0o644); err != nil {
		return err
	}
	return nil
}

// HasProject reports whether a project directory exists.
func (m *Model) HasProject(name string) bool {
	fi, err := os.Stat(m.projectDir(name))
	return err == nil && fi.IsDir()
}

// ActiveProject reads the active-project marker. "" means none selected.
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

// SetActiveProject writes the marker after checking the project exists.
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

// ProjectInstructions returns progetto.md for a project ("" if absent).
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
