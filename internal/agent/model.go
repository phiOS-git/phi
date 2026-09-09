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

// The data model of phios-agente.md §8.2, on disk under A1's XDG data
// directory. Two orthogonal axes: personalities (a system prompt the user
// owns) and projects (a directory of instructions, materials, memory,
// archive, proposals, output). A1 only — A2 has no memory and no project
// notion in this sense (§8.6, ADR 096).
//
//	<A1 data dir>/
//	  personalita/<name>.md        prompt — user writes, agent reads
//	  projects/<name>/
//	    progetto.md                instructions + default personality
//	    materiali/                 [agent: read-only]
//	    archivio/                  [agent: read-only]
//	    memoria.md                 [agent: read-only — enforced by the mount]
//	    proposte/                  [agent: write]
//	    output/                    [agent: write]

//go:embed seeds/general.md seeds/technical.md seeds/project.md
var seeds embed.FS

// projectSubdirs are created for every project. proposte/ and output/ are
// the only two the containment mounts writable (§4.3).
var projectSubdirs = []string{"materiali", "archivio", "proposte", "output"}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// validName guards every user-supplied personality or project name: it
// becomes a single path segment inside the data dir and, for a project, a
// mount destination.
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

// OpenModel returns A1's data model, creating the root if needed but not
// the seed content (call Ensure for that).
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

func (m *Model) personalitaDir() string { return filepath.Join(m.root, "personalita") }
func (m *Model) projectsDir() string    { return filepath.Join(m.root, "projects") }
func (m *Model) projectDir(name string) string {
	return filepath.Join(m.projectsDir(), name)
}

// activeMarker is the file phi-agent-contain reads (as a fallback to
// $PHI_AGENT_PROJECT) to know which project to mount. It lives in STATE,
// not DATA: it is runtime selection, not model content.
func activeMarkerPath() (string, error) {
	sd, err := A1.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sd, "active-project"), nil
}

// Ensure creates the §8.2 skeleton and the two seed personalities if they
// are absent. Idempotent: existing files are never overwritten (the user
// owns personalita/). This is §8.3's "minimal initial state for a clean
// install", not a reset.
func (m *Model) Ensure() (created []string, err error) {
	if err := os.MkdirAll(m.personalitaDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.projectsDir(), 0o755); err != nil {
		return nil, err
	}
	for _, p := range []string{"general", "technical"} {
		dst := filepath.Join(m.personalitaDir(), p+".md")
		if _, statErr := os.Stat(dst); statErr == nil {
			continue
		}
		data, readErr := seeds.ReadFile("seeds/" + p + ".md")
		if readErr != nil {
			return created, readErr
		}
		if writeErr := os.WriteFile(dst, data, 0o644); writeErr != nil {
			return created, writeErr
		}
		created = append(created, dst)
	}
	return created, nil
}

// Personalities lists the personality names (file stem of personalita/*.md).
func (m *Model) Personalities() ([]string, error) {
	entries, err := os.ReadDir(m.personalitaDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(out)
	return out, nil
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

// NewProject creates a project directory from the template. It does not
// switch to it.
func (m *Model) NewProject(name string) error {
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
	tmpl, err := seeds.ReadFile("seeds/project.md")
	if err != nil {
		return err
	}
	body := strings.ReplaceAll(string(tmpl), "{{NAME}}", name)
	if err := os.WriteFile(filepath.Join(dir, "progetto.md"), []byte(body), 0o644); err != nil {
		return err
	}
	// memoria.md starts empty. It is always in context (§8.5) and the agent
	// can never write it (§8.4) — only `phi agent memory accept` appends here.
	if err := os.WriteFile(filepath.Join(dir, "memoria.md"), []byte("# Memory\n\nDurable facts for this project. Written only by `phi agent memory accept`.\n"), 0o644); err != nil {
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

// Proposals lists pending memory proposals for a project: files under
// proposte/, which the agent may write but not promote (§8.4).
func (m *Model) Proposals(project string) ([]string, error) {
	dir := filepath.Join(m.projectDir(project), "proposte")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// checkSegment rejects a proposal file name that is not a single path
// segment. The name arrives straight from a CLI argument or the shell panel;
// filepath.Join would otherwise resolve "../../etc/passwd" out of proposte/.
func checkSegment(name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return fmt.Errorf("invalid proposal name %q: must be a single file name", name)
	}
	return nil
}

// ProposalText returns the literal text of one proposal.
func (m *Model) ProposalText(project, name string) (string, error) {
	if err := checkSegment(name); err != nil {
		return "", err
	}
	return readFileString(filepath.Join(m.projectDir(project), "proposte", name))
}

// MemoryText returns the current memoria.md.
func (m *Model) MemoryText(project string) (string, error) {
	return readFileString(filepath.Join(m.projectDir(project), "memoria.md"))
}

// AcceptProposal appends a proposal's literal text to memoria.md and
// removes it from proposte/. This is the client promoting an approved
// proposal (§8.4) — the only path by which memory is ever written, and it
// runs outside the containment.
func (m *Model) AcceptProposal(project, name string) error {
	if err := checkSegment(name); err != nil {
		return err
	}
	pPath := filepath.Join(m.projectDir(project), "proposte", name)
	text, err := readFileString(pPath)
	if err != nil {
		return err
	}
	memPath := filepath.Join(m.projectDir(project), "memoria.md")
	f, err := os.OpenFile(memPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	block := "\n"
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if _, err := f.WriteString(block + text); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(pPath)
}

// RejectProposal removes a proposal without promoting it.
func (m *Model) RejectProposal(project, name string) error {
	if err := checkSegment(name); err != nil {
		return err
	}
	return os.Remove(filepath.Join(m.projectDir(project), "proposte", name))
}

// ProjectInstructions returns progetto.md for a project ("" if absent).
func (m *Model) ProjectInstructions(project string) (string, error) {
	s, err := readFileString(filepath.Join(m.projectDir(project), "progetto.md"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return s, err
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
