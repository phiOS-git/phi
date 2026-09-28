package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Structured project metadata (binding contract §4). project.json is the
// client's to own; instructions.md is generated from it so pi always reads
// one plain file via --append-system-prompt. folders[] are real host
// directories, resolved per host by paths[HostShortName()] — never copied.

// Folder is one project folder-of-interest.
type Folder struct {
	Name  string            `json:"name"`
	Mode  string            `json:"mode"` // "ro" | "rw"
	Paths map[string]string `json:"paths"`
}

// ProjectMeta is the on-disk project.json (§4).
type ProjectMeta struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Instructions   []string `json:"instructions"`
	DefaultProfile Profile  `json:"default_profile"`
	Folders        []Folder `json:"folders"`
	// Pins is legacy: pin state lives in chat sidecars now (chat.go). Read
	// for backward compatibility, never written to by this package.
	Pins []string `json:"pins"`
}

// legacyProjectMeta reads both the current shape and the pre-pi one:
// default_personality instead of default_profile, and folders as a plain
// array of host path strings instead of Folder objects.
type legacyProjectMeta struct {
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	Instructions       []string        `json:"instructions"`
	DefaultPersonality string          `json:"default_personality"`
	DefaultProfile     Profile         `json:"default_profile"`
	Folders            json.RawMessage `json:"folders"`
	Pins               []string        `json:"pins"`
}

// UnmarshalJSON accepts the current project.json shape and the legacy one
// (§4): default_personality -> default_profile "general"; a plain string
// array of folders -> one {name: sanitised basename, mode: "ro",
// paths: {<this host>: path}} per entry.
func (p *ProjectMeta) UnmarshalJSON(data []byte) error {
	var raw legacyProjectMeta
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Title = raw.Title
	p.Description = raw.Description
	p.Instructions = raw.Instructions
	p.Pins = raw.Pins
	p.DefaultProfile = raw.DefaultProfile
	if p.DefaultProfile == "" && raw.DefaultPersonality != "" {
		p.DefaultProfile = General
	}

	if len(raw.Folders) == 0 || string(raw.Folders) == "null" {
		return nil
	}
	var folders []Folder
	if err := json.Unmarshal(raw.Folders, &folders); err == nil {
		p.Folders = folders
		return nil
	}
	var paths []string
	if err := json.Unmarshal(raw.Folders, &paths); err != nil {
		return fmt.Errorf("folders: %w", err)
	}
	host := HostShortName()
	seen := map[string]bool{}
	for _, path := range paths {
		name := SanitiseFolderName(filepath.Base(strings.TrimRight(path, "/")))
		orig := name
		for i := 2; seen[name]; i++ {
			name = fmt.Sprintf("%s-%d", orig, i)
		}
		seen[name] = true
		p.Folders = append(p.Folders, Folder{Name: name, Mode: "ro", Paths: map[string]string{host: path}})
	}
	return nil
}

// SanitiseFolderName lower-cases s and replaces every character outside
// [a-z0-9._-] with '-', strips a non-alphanumeric prefix, and truncates to
// 64 chars — the shape validName("folder", …) requires.
func SanitiseFolderName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s = b.String()
	for len(s) > 0 && !((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= '0' && s[0] <= '9')) {
		s = s[1:]
	}
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		s = "folder"
	}
	return s
}

func (p *ProjectMeta) normalise(name string) {
	if p.Title == "" {
		p.Title = name
	}
	if p.DefaultProfile == "" {
		p.DefaultProfile = General
	}
	p.Instructions = trimEmpty(p.Instructions)
	p.Pins = dedupeStrings(p.Pins)
	folders := make([]Folder, 0, len(p.Folders))
	seen := map[string]bool{}
	for _, f := range p.Folders {
		if f.Name == "" || seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		if f.Mode != "rw" {
			f.Mode = "ro"
		}
		if f.Paths == nil {
			f.Paths = map[string]string{}
		}
		folders = append(folders, f)
	}
	p.Folders = folders
}

func (m *Model) projectMetaPath(name string) string {
	return filepath.Join(m.projectDir(name), "project.json")
}

func (m *Model) projectInstructionsPath(name string) string {
	return filepath.Join(m.projectDir(name), "instructions.md")
}

// LoadProjectMeta reads project.json (accepting the legacy shape). A
// project with no project.json yet returns a zero-value meta named after
// the project.
func (m *Model) LoadProjectMeta(name string) (ProjectMeta, error) {
	if !m.HasProject(name) {
		return ProjectMeta{}, fmt.Errorf("no such project: %q", name)
	}
	b, err := os.ReadFile(m.projectMetaPath(name))
	if os.IsNotExist(err) {
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

// SaveProjectMeta writes project.json and regenerates instructions.md from
// it (never hand-edited; §6 reads it via --append-system-prompt).
func (m *Model) SaveProjectMeta(name string, meta ProjectMeta) error {
	if err := validName("project", name); err != nil {
		return err
	}
	meta.normalise(name)
	for _, f := range meta.Folders {
		if err := validName("folder", f.Name); err != nil {
			return err
		}
		if f.Mode != "ro" && f.Mode != "rw" {
			return fmt.Errorf("folder %q: mode %q (want ro or rw)", f.Name, f.Mode)
		}
		for _, path := range f.Paths {
			if err := ValidateFolderOfInterest(path); err != nil {
				return err
			}
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
	return os.WriteFile(m.projectInstructionsPath(name), []byte(renderInstructionsMD(name, meta)), 0o644)
}

// renderInstructionsMD builds the short markdown brief pi reads (§6): title,
// description, instructions, and the mounted folders with their mode.
func renderInstructionsMD(name string, meta ProjectMeta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", orString(meta.Title, name))
	b.WriteString("<!-- Generated from project.json by `phi agent project`. Do not edit by hand. -->\n\n")
	if strings.TrimSpace(meta.Description) != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(meta.Description))
	}
	if len(meta.Instructions) > 0 {
		b.WriteString("## Instructions\n\n")
		for _, ins := range meta.Instructions {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(ins))
		}
		b.WriteString("\n")
	}
	if len(meta.Folders) > 0 {
		b.WriteString("## Folders\n\n")
		for _, f := range meta.Folders {
			fmt.Fprintf(&b, "- `%s` (%s)\n", f.Name, f.Mode)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ResolvedFolder is one project folder resolved for a specific host.
type ResolvedFolder struct {
	Name string
	Mode string
	Path string
}

// ResolveFolders returns the folders that have a path for host, in
// declaration order (§4).
func (p ProjectMeta) ResolveFolders(host string) []ResolvedFolder {
	var out []ResolvedFolder
	for _, f := range p.Folders {
		if path, ok := f.Paths[host]; ok && path != "" {
			out = append(out, ResolvedFolder{Name: f.Name, Mode: f.Mode, Path: path})
		}
	}
	return out
}

// AddProjectFolder sets this host's path for a project folder, creating the
// folder entry if folderName is new (or empty — defaulted to a sanitised
// basename of path). mode is used only when creating the entry, or to
// change an existing one's mode; "" keeps the current/default mode.
func (m *Model) AddProjectFolder(project, path, folderName, mode string) error {
	abs, err := resolveDir(path)
	if err != nil {
		return err
	}
	if err := ValidateFolderOfInterest(abs); err != nil {
		return err
	}
	if folderName == "" {
		folderName = SanitiseFolderName(filepath.Base(abs))
	}
	if err := validName("folder", folderName); err != nil {
		return err
	}
	if mode != "" && mode != "ro" && mode != "rw" {
		return fmt.Errorf("folder mode %q (want ro or rw)", mode)
	}
	meta, err := m.LoadProjectMeta(project)
	if err != nil {
		return err
	}
	host := HostShortName()
	for i := range meta.Folders {
		if meta.Folders[i].Name == folderName {
			if meta.Folders[i].Paths == nil {
				meta.Folders[i].Paths = map[string]string{}
			}
			meta.Folders[i].Paths[host] = abs
			if mode != "" {
				meta.Folders[i].Mode = mode
			}
			return m.SaveProjectMeta(project, meta)
		}
	}
	if mode == "" {
		mode = "ro"
	}
	meta.Folders = append(meta.Folders, Folder{Name: folderName, Mode: mode, Paths: map[string]string{host: abs}})
	return m.SaveProjectMeta(project, meta)
}

// RemoveProjectFolder drops a folder entry entirely (every host's path).
func (m *Model) RemoveProjectFolder(project, folderName string) error {
	meta, err := m.LoadProjectMeta(project)
	if err != nil {
		return err
	}
	var kept []Folder
	found := false
	for _, f := range meta.Folders {
		if f.Name == folderName {
			found = true
			continue
		}
		kept = append(kept, f)
	}
	if !found {
		return fmt.Errorf("no such folder: %q", folderName)
	}
	meta.Folders = kept
	return m.SaveProjectMeta(project, meta)
}

// SetProjectFolderMode changes a folder's mode.
func (m *Model) SetProjectFolderMode(project, folderName, mode string) error {
	if mode != "ro" && mode != "rw" {
		return fmt.Errorf("folder mode %q (want ro or rw)", mode)
	}
	meta, err := m.LoadProjectMeta(project)
	if err != nil {
		return err
	}
	for i := range meta.Folders {
		if meta.Folders[i].Name == folderName {
			meta.Folders[i].Mode = mode
			return m.SaveProjectMeta(project, meta)
		}
	}
	return fmt.Errorf("no such folder: %q", folderName)
}

// ProjectSummary is one row of `phi agent project list` (§7).
type ProjectSummary struct {
	Name           string `json:"name"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	DefaultProfile string `json:"default_profile"`
}

// ListProjectSummaries returns every project's list-view summary.
func (m *Model) ListProjectSummaries() ([]ProjectSummary, error) {
	names, err := m.Projects()
	if err != nil {
		return nil, err
	}
	out := make([]ProjectSummary, 0, len(names))
	for _, n := range names {
		meta, err := m.LoadProjectMeta(n)
		if err != nil {
			continue
		}
		out = append(out, ProjectSummary{
			Name: n, Title: meta.Title, Description: meta.Description,
			DefaultProfile: string(meta.DefaultProfile),
		})
	}
	return out, nil
}

// ProjectInstructionsText returns a project's generated instructions.md
// ("" if absent).
func (m *Model) ProjectInstructionsText(name string) (string, error) {
	s, err := readFileString(m.projectInstructionsPath(name))
	if os.IsNotExist(err) {
		return "", nil
	}
	return s, err
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
	sort.Strings(out)
	return out
}

func trimEmpty(in []string) []string {
	out := make([]string, 0, len(in))
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
		return "", fmt.Errorf("empty path")
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
