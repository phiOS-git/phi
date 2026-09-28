package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Three-level memory (§2): system, profile, project. Each level has a
// read-only memoria.md and a writable proposte/ the agent may drop files
// into but never promote itself. Promotion (AcceptProposal) is the only
// path by which memoria.md is ever written, and it always runs outside the
// containment.

type MemKind string

const (
	MemSystem  MemKind = "system"
	MemProfile MemKind = "profile"
	MemProject MemKind = "project"
)

// MemLevel names one memory level. Name is the profile or project name;
// empty for system.
type MemLevel struct {
	Kind MemKind
	Name string
}

func SystemLevel() MemLevel             { return MemLevel{Kind: MemSystem} }
func ProfileLevel(p Profile) MemLevel   { return MemLevel{Kind: MemProfile, Name: string(p)} }
func ProjectLevel(name string) MemLevel { return MemLevel{Kind: MemProject, Name: name} }

func (l MemLevel) String() string {
	if l.Name == "" {
		return string(l.Kind)
	}
	return string(l.Kind) + ":" + l.Name
}

func isMemoryProfile(p Profile) bool {
	for _, mp := range MemoryProfiles() {
		if mp == p {
			return true
		}
	}
	return false
}

// ParseMemLevel builds a level from `--level` plus `--profile`/`--project`
// (§7's LEVEL FLAGS).
func ParseMemLevel(kind, profile, project string) (MemLevel, error) {
	switch MemKind(kind) {
	case MemSystem:
		return SystemLevel(), nil
	case MemProfile:
		if profile == "" {
			return MemLevel{}, errors.New("--level profile needs --profile NAME")
		}
		p, err := ParseProfile(profile)
		if err != nil {
			return MemLevel{}, err
		}
		if !isMemoryProfile(p) {
			return MemLevel{}, fmt.Errorf("profile %q has no memory level (want general, academic or coding)", p)
		}
		return ProfileLevel(p), nil
	case MemProject:
		if project == "" {
			return MemLevel{}, errors.New("--level project needs --project NAME")
		}
		if err := validName("project", project); err != nil {
			return MemLevel{}, err
		}
		return ProjectLevel(project), nil
	default:
		return MemLevel{}, fmt.Errorf("unknown memory level %q (want system, profile, project)", kind)
	}
}

func (m *Model) levelDir(l MemLevel) (string, error) {
	switch l.Kind {
	case MemSystem:
		return m.root, nil
	case MemProfile:
		p, err := ParseProfile(l.Name)
		if err != nil {
			return "", err
		}
		if !isMemoryProfile(p) {
			return "", fmt.Errorf("profile %q has no memory level", p)
		}
		return m.profileDir(p), nil
	case MemProject:
		if !m.HasProject(l.Name) {
			return "", fmt.Errorf("no such project: %q", l.Name)
		}
		return m.projectDir(l.Name), nil
	default:
		return "", fmt.Errorf("unknown memory level %q", l.Kind)
	}
}

func (m *Model) memoriaPath(l MemLevel) (string, error) {
	d, err := m.levelDir(l)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "memoria.md"), nil
}

func (m *Model) proposteDir(l MemLevel) (string, error) {
	d, err := m.levelDir(l)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "proposte"), nil
}

// MemoryText returns the current memoria.md for a level ("" if absent).
func (m *Model) MemoryText(l MemLevel) (string, error) {
	p, err := m.memoriaPath(l)
	if err != nil {
		return "", err
	}
	s, err := readFileString(p)
	if os.IsNotExist(err) {
		return "", nil
	}
	return s, err
}

// Proposals lists pending memory proposals for a level: files under its
// proposte/, which the agent may write but not promote. Never nil, so JSON
// output is "[]" rather than "null" when there are none.
func (m *Model) Proposals(l MemLevel) ([]string, error) {
	dir, err := m.proposteDir(l)
	if err != nil {
		return nil, err
	}
	out := []string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// AllProposals returns every pending proposal across system, every memory
// profile, and every project, keyed by level string (§7: every key present
// even when empty).
func (m *Model) AllProposals() (map[string][]string, error) {
	out := map[string][]string{}
	add := func(l MemLevel) error {
		props, err := m.Proposals(l)
		if err != nil {
			return err
		}
		out[l.String()] = props
		return nil
	}
	if err := add(SystemLevel()); err != nil {
		return nil, err
	}
	for _, p := range MemoryProfiles() {
		if err := add(ProfileLevel(p)); err != nil {
			return nil, err
		}
	}
	prj, _ := m.Projects()
	for _, p := range prj {
		if err := add(ProjectLevel(p)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ProposalText returns the literal text of one proposal at a level.
func (m *Model) ProposalText(l MemLevel, name string) (string, error) {
	if err := checkSegment(name); err != nil {
		return "", err
	}
	dir, err := m.proposteDir(l)
	if err != nil {
		return "", err
	}
	return readFileString(filepath.Join(dir, name))
}

// AcceptProposal appends a proposal's literal text to the level's
// memoria.md and removes it from proposte/.
func (m *Model) AcceptProposal(l MemLevel, name string) error {
	if err := checkSegment(name); err != nil {
		return err
	}
	dir, err := m.proposteDir(l)
	if err != nil {
		return err
	}
	pPath := filepath.Join(dir, name)
	text, err := readFileString(pPath)
	if err != nil {
		return err
	}
	memPath, err := m.memoriaPath(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(memPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(memPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if _, err := f.WriteString("\n" + text); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(pPath)
}

// RejectProposal removes a proposal without promoting it.
func (m *Model) RejectProposal(l MemLevel, name string) error {
	if err := checkSegment(name); err != nil {
		return err
	}
	dir, err := m.proposteDir(l)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, name))
}
