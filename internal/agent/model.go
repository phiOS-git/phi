package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The agent data model: memory (system/profile/project, see memory.go) and
// projects (instructions, attachments, sessions, proposals, output; see
// project.go, chat.go). Root layout is binding-contract §2:
//
//	<root>/memoria.md, proposte/, sessions/
//	<root>/profiles/<profile>/memoria.md, proposte/
//	<root>/projects/<name>/{project.json, instructions.md, memoria.md,
//	                        proposte/, allegati/, sessions/, output/}

// migratedMarker sits at the root once the one-time legacy copy has run.
const migratedMarker = ".migrated-from-a1"

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// validName validates project or folder names (path-safe, 1-64 chars).
func validName(kind, name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid %s name %q: use lowercase letters, digits, '.', '_', '-' (1-64 chars)", kind, name)
	}
	return nil
}

// Model is the agent data model, rooted at DataRoot().
type Model struct {
	root string
}

// OpenModel returns the data model, creating the root if needed.
func OpenModel() (*Model, error) {
	root, err := DataRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Model{root: root}, nil
}

func (m *Model) Root() string                { return m.root }
func (m *Model) proposteDirSystem() string   { return filepath.Join(m.root, "proposte") }
func (m *Model) sessionsDirSystem() string   { return filepath.Join(m.root, "sessions") }
func (m *Model) profilesDir() string         { return filepath.Join(m.root, "profiles") }
func (m *Model) profileDir(p Profile) string { return filepath.Join(m.profilesDir(), string(p)) }
func (m *Model) projectsDir() string         { return filepath.Join(m.root, "projects") }
func (m *Model) projectDir(name string) string {
	return filepath.Join(m.projectsDir(), name)
}

// ProjectDir is the absolute directory of a project.
func (m *Model) ProjectDir(name string) string { return m.projectDir(name) }

// projectSubdirs: created for every new project.
var projectSubdirs = []string{"allegati", "sessions", "proposte", "output"}

// Ensure creates the §2 skeleton (idempotent) and, the first time it finds
// a legacy a1 data root, copies what it recognises into the new layout.
// Returns human-readable lines describing what was created or migrated;
// empty when everything was already in place.
func (m *Model) Ensure() (report []string, err error) {
	mkdir := func(p string) error {
		if _, statErr := os.Stat(p); statErr == nil {
			return nil
		}
		if mkErr := os.MkdirAll(p, 0o755); mkErr != nil {
			return mkErr
		}
		report = append(report, "created "+p)
		return nil
	}
	for _, p := range []string{m.root, m.proposteDirSystem(), m.sessionsDirSystem(), m.projectsDir()} {
		if err := mkdir(p); err != nil {
			return report, err
		}
	}
	for _, pr := range MemoryProfiles() {
		if err := mkdir(filepath.Join(m.profileDir(pr), "proposte")); err != nil {
			return report, err
		}
	}

	migrated, err := m.migrateLegacy()
	if err != nil {
		return report, err
	}
	report = append(report, migrated...)
	return report, nil
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

// HasProject reports if a project directory exists.
func (m *Model) HasProject(name string) bool {
	fi, err := os.Stat(m.projectDir(name))
	return err == nil && fi.IsDir()
}

// NewProject creates a project directory with the §2 skeleton.
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
	if err := m.SaveProjectMeta(name, meta); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "memoria.md"),
		[]byte("# Memory — project "+name+"\n\nDurable facts for this project. Written only by `phi agent memory accept --level project --project "+name+"`.\n"),
		0o644)
}

// DeleteProject removes a project and everything under it.
func (m *Model) DeleteProject(name string) error {
	if err := validName("project", name); err != nil {
		return err
	}
	if !m.HasProject(name) {
		return fmt.Errorf("no such project: %q", name)
	}
	return os.RemoveAll(m.projectDir(name))
}

// checkSegment rejects a name that is not a single path segment. The name
// arrives straight from a CLI argument or the shell panel; filepath.Join
// would otherwise resolve "../../etc/passwd" out of the target directory.
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

func fileExists(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }
func dirExists(p string) bool  { fi, err := os.Stat(p); return err == nil && fi.IsDir() }

// --- legacy migration (§2 "was ~/.local/share/phi-agent/a1/ — now legacy") ---

// migrateLegacy runs once: when LegacyDataRoot() exists and migratedMarker
// is absent, it COPIES (never moves or deletes) what it recognises from the
// pre-pi a1 layout into the new one. Destinations that already exist are
// left alone — this never overwrites anything the new layout already has.
func (m *Model) migrateLegacy() (lines []string, err error) {
	legacyRoot, err := LegacyDataRoot()
	if err != nil {
		return nil, err
	}
	if !dirExists(legacyRoot) {
		return nil, nil
	}
	markerPath := filepath.Join(m.root, migratedMarker)
	if fileExists(markerPath) {
		return nil, nil
	}

	if copied, cerr := copyFileIfAbsent(filepath.Join(legacyRoot, "memoria.md"), filepath.Join(m.root, "memoria.md")); cerr != nil {
		return lines, cerr
	} else if copied {
		lines = append(lines, "migrated system memoria.md")
	}
	if n, cerr := copyDirShallow(filepath.Join(legacyRoot, "proposte"), m.proposteDirSystem()); cerr != nil {
		return lines, cerr
	} else if n > 0 {
		lines = append(lines, fmt.Sprintf("migrated %d system proposal(s)", n))
	}

	// personalita/<name>/memoria.md -> profiles/<profile>/memoria.md.
	// general -> general; technical -> coding (§4's default_personality ->
	// default_profile mapping mirrors this). Anything else has no
	// equivalent profile and is reported, not migrated.
	toProfile := map[string]Profile{"general": General, "technical": Coding}
	legacyPersDir := filepath.Join(legacyRoot, "personalita")
	if entries, rerr := os.ReadDir(legacyPersDir); rerr == nil {
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			name := ent.Name()
			prof, ok := toProfile[name]
			if !ok {
				lines = append(lines, fmt.Sprintf("personality %q not migrated (no equivalent profile)", name))
				continue
			}
			src := filepath.Join(legacyPersDir, name, "memoria.md")
			dst := filepath.Join(m.profileDir(prof), "memoria.md")
			if copied, cerr := copyFileIfAbsent(src, dst); cerr != nil {
				return lines, cerr
			} else if copied {
				lines = append(lines, fmt.Sprintf("migrated profile %s memoria.md (from personality %q)", prof, name))
			}
		}
	}

	legacyProjDir := filepath.Join(legacyRoot, "projects")
	if entries, rerr := os.ReadDir(legacyProjDir); rerr == nil {
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			name := ent.Name()
			if m.HasProject(name) {
				lines = append(lines, fmt.Sprintf("project %q not migrated (already exists)", name))
				continue
			}
			if !nameRE.MatchString(name) {
				lines = append(lines, fmt.Sprintf("project %q not migrated (invalid name)", name))
				continue
			}
			if merr := m.migrateLegacyProject(legacyProjDir, name); merr != nil {
				return lines, merr
			}
			lines = append(lines, fmt.Sprintf("migrated project %q", name))
		}
	}

	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return lines, err
	}
	if err := os.WriteFile(markerPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return lines, err
	}
	return lines, nil
}

// migrateLegacyProject copies one legacy project into the new layout.
// materiali/ -> allegati/, conversazioni/ -> allegati/legacy-conversazioni/,
// archivio/ -> allegati/legacy-archivio/ (§4/§2 of the binding contract).
func (m *Model) migrateLegacyProject(legacyProjDir, name string) error {
	srcDir := filepath.Join(legacyProjDir, name)
	dstDir := m.projectDir(name)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}

	if b, err := os.ReadFile(filepath.Join(srcDir, "project.json")); err == nil {
		var meta ProjectMeta
		if err := json.Unmarshal(b, &meta); err != nil {
			return fmt.Errorf("legacy project.json for %q: %w", name, err)
		}
		if err := m.SaveProjectMeta(name, meta); err != nil {
			return err
		}
	}

	if _, err := copyFileIfAbsent(filepath.Join(srcDir, "memoria.md"), filepath.Join(dstDir, "memoria.md")); err != nil {
		return err
	}
	if _, err := copyDirShallow(filepath.Join(srcDir, "proposte"), filepath.Join(dstDir, "proposte")); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(srcDir, "output"), filepath.Join(dstDir, "output")); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(srcDir, "materiali"), filepath.Join(dstDir, "allegati")); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(srcDir, "conversazioni"), filepath.Join(dstDir, "allegati", "legacy-conversazioni")); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(srcDir, "archivio"), filepath.Join(dstDir, "allegati", "legacy-archivio")); err != nil {
		return err
	}
	return nil
}

// copyFileIfAbsent copies src to dst only when dst does not exist yet and
// src does. Reports whether a copy happened.
func copyFileIfAbsent(src, dst string) (bool, error) {
	if !fileExists(src) || fileExists(dst) {
		return false, nil
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// copyDirShallow copies the regular files directly inside srcDir into
// dstDir (not recursive — proposte/ never nests), skipping any file whose
// destination already exists. Returns how many files were copied.
func copyDirShallow(srcDir, dstDir string) (int, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		copied, err := copyFileIfAbsent(filepath.Join(srcDir, e.Name()), filepath.Join(dstDir, e.Name()))
		if err != nil {
			return n, err
		}
		if copied {
			n++
		}
	}
	return n, nil
}

// copyTree recursively copies srcDir into dstDir, skipping any destination
// file that already exists. A missing srcDir is not an error (most legacy
// subtrees are optional).
func copyTree(srcDir, dstDir string) error {
	if !dirExists(srcDir) {
		return nil
	}
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		_, err = copyFileIfAbsent(path, dst)
		return err
	})
}
