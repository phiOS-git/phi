package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Three-level memory, phios-agente-delta.md D-01. Each level has one
// `memoria.md` (always in context, §8.5) and one writable `proposte/`
// directory. The agent can never write any `memoria.md` — the read-only
// mount enforces it at every level (ADR 094, principle unchanged). The
// client, outside the containment, is the only writer, via AcceptProposal.
//
//	level        memoria.md                              proposte/
//	system       <root>/memoria.md                       <root>/proposte/
//	personality  <root>/personalita/<name>/memoria.md    <root>/personalita/<name>/proposte/
//	project      <root>/projects/<name>/memoria.md       <root>/projects/<name>/proposte/

type MemKind string

const (
	MemSystem      MemKind = "system"
	MemPersonality MemKind = "personality"
	MemProject     MemKind = "project"
)

// MemLevel names one memory level. Name is the personality or project name;
// empty for system.
type MemLevel struct {
	Kind MemKind
	Name string
}

func SystemLevel() MemLevel              { return MemLevel{Kind: MemSystem} }
func PersonalityLevel(n string) MemLevel { return MemLevel{Kind: MemPersonality, Name: n} }
func ProjectLevel(n string) MemLevel     { return MemLevel{Kind: MemProject, Name: n} }

func (l MemLevel) String() string {
	if l.Name == "" {
		return string(l.Kind)
	}
	return string(l.Kind) + ":" + l.Name
}

// ParseMemLevel builds a level from a "--level" flag plus an optional name.
func ParseMemLevel(kind, name string) (MemLevel, error) {
	switch MemKind(kind) {
	case MemSystem:
		return SystemLevel(), nil
	case MemPersonality:
		if name == "" {
			return MemLevel{}, errors.New("--level personality needs --personality NAME")
		}
		if !nameRE.MatchString(name) {
			return MemLevel{}, fmt.Errorf("invalid personality name %q", name)
		}
		return PersonalityLevel(name), nil
	case MemProject:
		if name == "" {
			return MemLevel{}, errors.New("--level project needs a project (active or --project NAME)")
		}
		if !nameRE.MatchString(name) {
			return MemLevel{}, fmt.Errorf("invalid project name %q", name)
		}
		return ProjectLevel(name), nil
	default:
		return MemLevel{}, fmt.Errorf("unknown memory level %q (want system, personality, project)", kind)
	}
}

func (m *Model) systemProposteDir() string { return filepath.Join(m.root, "proposte") }

func (m *Model) levelDir(l MemLevel) (string, error) {
	switch l.Kind {
	case MemSystem:
		return m.root, nil
	case MemPersonality:
		if !m.HasPersonality(l.Name) {
			return "", fmt.Errorf("no such personality: %q", l.Name)
		}
		if err := m.ensurePersonalityDir(l.Name); err != nil {
			return "", err
		}
		return m.personalityDir(l.Name), nil
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
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return s, err
}

// Proposals lists pending memory proposals for a level: files under its
// proposte/, which the agent may write but not promote (§8.4).
func (m *Model) Proposals(l MemLevel) ([]string, error) {
	dir, err := m.proposteDir(l)
	if err != nil {
		return nil, err
	}
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

// AllProposals returns every pending proposal across system, every
// personality, and every project, keyed by level.
func (m *Model) AllProposals() (map[string][]string, error) {
	out := map[string][]string{}
	add := func(l MemLevel) error {
		props, err := m.Proposals(l)
		if err != nil {
			return err
		}
		if len(props) > 0 {
			out[l.String()] = props
		}
		return nil
	}
	if err := add(SystemLevel()); err != nil {
		return nil, err
	}
	ps, _ := m.Personalities()
	for _, p := range ps {
		if err := add(PersonalityLevel(p)); err != nil {
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

// AcceptProposal appends a proposal's literal text to the level's memoria.md
// and removes it from proposte/. This is the client promoting an approved
// proposal (§8.4) — the only path by which memory is ever written, at any
// level, and it runs outside the containment.
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
	if err := os.Remove(pPath); err != nil {
		return err
	}
	if l.Kind == MemPersonality {
		return m.syncPersonalityAgent(l.Name)
	}
	return nil
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
