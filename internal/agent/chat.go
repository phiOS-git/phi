package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// pi owns each session's transcript (a JSONL file it writes inside the
// containment); phi never writes one. phi keeps a small sidecar next to
// each, `<id>.phi.json` (§2), for the things pi does not track itself:
// title override, pin state, and which profile/project the session
// belongs to. A session with no project lives under the data root's
// sessions/; a session for project P lives under projects/P/sessions/.

// NewSessionID mints a session id valid for pi's --session-id:
// YYYYMMDD-HHMMSS-xxxxxx (6 lowercase hex digits), sortable and
// filesystem-safe. Shared by pi sessions and phi's own terminal session
// records (session.go), so `phi agent code` can use one id for both.
func NewSessionID() string {
	var buf [3]byte
	_, _ = rand.Read(buf[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buf[:])
}

// Sidecar is phi's per-session metadata, `<id>.phi.json` next to pi's own
// `<id>.jsonl` (§2). Title "" means: use pi's latest session_info name,
// else the first user message truncated to 60 chars, else the id.
type Sidecar struct {
	ID      string    `json:"id"`
	Profile string    `json:"profile"`
	Project string    `json:"project"`
	Title   string    `json:"title"`
	Pinned  bool      `json:"pinned"`
	Created time.Time `json:"created"`
}

// TranscriptMeta is one session's list-view metadata (§7: chat list/show).
type TranscriptMeta struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Profile string    `json:"profile"`
	Project string    `json:"project"`
	Pinned  bool      `json:"pinned"`
	Updated time.Time `json:"updated"`
	Path    string    `json:"path"` // absolute .jsonl path; "" if pi has not written it yet
}

// SessionsDir is the sessions directory for project ("" = the data root's
// own, for unfiled sessions).
func (m *Model) SessionsDir(project string) (string, error) {
	if project == "" {
		return m.sessionsDirSystem(), nil
	}
	if !m.HasProject(project) {
		return "", fmt.Errorf("no such project: %q", project)
	}
	return filepath.Join(m.projectDir(project), "sessions"), nil
}

func sidecarPath(dir, id string) string { return filepath.Join(dir, id+".phi.json") }

// WriteSidecar creates or replaces a session's sidecar file,
// <dir>/<id>.phi.json.
func WriteSidecar(dir string, meta Sidecar) error {
	if err := checkSegment(meta.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sidecarPath(dir, meta.ID), append(b, '\n'), 0o644)
}

func readSidecar(path string) (Sidecar, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Sidecar{}, err
	}
	var sc Sidecar
	if err := json.Unmarshal(b, &sc); err != nil {
		return Sidecar{}, fmt.Errorf("%s: %w", path, err)
	}
	return sc, nil
}

// readOrCreateSidecar loads <dir>/<id>.phi.json, lazily creating it with
// profile "general" if missing (§2).
func readOrCreateSidecar(dir, id string) (Sidecar, error) {
	p := sidecarPath(dir, id)
	if sc, err := readSidecar(p); err == nil {
		if sc.ID == "" {
			sc.ID = id
		}
		return sc, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Sidecar{}, err
	}
	sc := Sidecar{ID: id, Profile: string(General), Created: time.Now().UTC()}
	if err := WriteSidecar(dir, sc); err != nil {
		return Sidecar{}, err
	}
	return sc, nil
}

// sessionIDFromFilename extracts <session-id> from pi's own
// <timestamp>_<session-id>.jsonl naming (§2).
func sessionIDFromFilename(name string) string {
	name = strings.TrimSuffix(name, ".jsonl")
	if i := strings.IndexByte(name, '_'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func findSessionJSONL(dir, id string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*_"+id+".jsonl"))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// sessionIDsInDir is the union of ids named by *.jsonl and *.phi.json in dir.
func sessionIDsInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".phi.json"):
			add(strings.TrimSuffix(name, ".phi.json"))
		case strings.HasSuffix(name, ".jsonl"):
			add(sessionIDFromFilename(name))
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// truncate60 collapses whitespace and truncates to 60 runes (§2's title
// fallback rule).
func truncate60(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

// resolveTitle applies §2's title rule: sidecar title, else pi's latest
// session_info name, else the first user message (truncated), else the id.
func resolveTitle(sc Sidecar, jsonlPath string) string {
	if sc.Title != "" {
		return sc.Title
	}
	if jsonlPath != "" {
		if f, err := os.Open(jsonlPath); err == nil {
			msgs, sessionName, perr := parseTranscript(f)
			f.Close()
			if perr == nil {
				if sessionName != "" {
					return sessionName
				}
				for _, msg := range msgs {
					if msg.Role == "user" && strings.TrimSpace(msg.Text) != "" {
						return truncate60(msg.Text)
					}
				}
			}
		}
	}
	return sc.ID
}

func transcriptUpdated(jsonlPath string, sc Sidecar, sidecarFile string) time.Time {
	if jsonlPath != "" {
		if fi, err := os.Stat(jsonlPath); err == nil {
			return fi.ModTime()
		}
	}
	if fi, err := os.Stat(sidecarFile); err == nil {
		return fi.ModTime()
	}
	return sc.Created
}

// FindTranscript locates a session by id across the data root's sessions/
// and every project's sessions/. The sidecar is authoritative for which
// directory a session lives in even before pi has written the .jsonl.
func (m *Model) FindTranscript(id string) (dir, jsonlPath string, meta Sidecar, err error) {
	if err := checkSegment(id); err != nil {
		return "", "", Sidecar{}, err
	}
	dirs := []string{m.sessionsDirSystem()}
	prj, _ := m.Projects()
	for _, p := range prj {
		dirs = append(dirs, filepath.Join(m.projectDir(p), "sessions"))
	}
	for _, d := range dirs {
		jp := findSessionJSONL(d, id)
		if jp == "" && !fileExists(sidecarPath(d, id)) {
			continue
		}
		sc, serr := readOrCreateSidecar(d, id)
		if serr != nil {
			return "", "", Sidecar{}, serr
		}
		return d, jp, sc, nil
	}
	return "", "", Sidecar{}, fmt.Errorf("no such session: %q", id)
}

func (m *Model) projectForSessionsDir(dir string) string {
	if dir == m.sessionsDirSystem() {
		return ""
	}
	return filepath.Base(filepath.Dir(dir))
}

// ListTranscripts lists sessions: for project == "" and !unfiledOnly, every
// session everywhere; for unfiledOnly, only the data root's own; otherwise
// only project's. Sorted pinned first, then newest.
func (m *Model) ListTranscripts(project string, unfiledOnly bool) ([]TranscriptMeta, error) {
	type target struct{ dir, project string }
	var targets []target
	switch {
	case unfiledOnly:
		targets = append(targets, target{m.sessionsDirSystem(), ""})
	case project != "":
		d, err := m.SessionsDir(project)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target{d, project})
	default:
		targets = append(targets, target{m.sessionsDirSystem(), ""})
		prj, _ := m.Projects()
		for _, p := range prj {
			targets = append(targets, target{filepath.Join(m.projectDir(p), "sessions"), p})
		}
	}

	out := []TranscriptMeta{}
	for _, tg := range targets {
		ids, err := sessionIDsInDir(tg.dir)
		if err != nil {
			continue
		}
		for _, id := range ids {
			jsonlPath := findSessionJSONL(tg.dir, id)
			sc, err := readOrCreateSidecar(tg.dir, id)
			if err != nil {
				continue
			}
			out = append(out, TranscriptMeta{
				ID: id, Title: resolveTitle(sc, jsonlPath), Profile: sc.Profile, Project: tg.project,
				Pinned: sc.Pinned, Updated: transcriptUpdated(jsonlPath, sc, sidecarPath(tg.dir, id)),
				Path: jsonlPath,
			})
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

// TranscriptMessages parses one pi session JSONL file into phi's simplified
// message shape (§7).
func TranscriptMessages(jsonlPath string) ([]Message, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	messages, _, err := parseTranscript(f)
	return messages, err
}

// LoadTranscript resolves everything `phi agent chat show` needs: the
// session's metadata (title resolved per §2) and its messages.
func (m *Model) LoadTranscript(id string) (TranscriptMeta, []Message, error) {
	dir, jsonlPath, sc, err := m.FindTranscript(id)
	if err != nil {
		return TranscriptMeta{}, nil, err
	}
	messages := []Message{}
	if jsonlPath != "" {
		messages, err = TranscriptMessages(jsonlPath)
		if err != nil {
			return TranscriptMeta{}, nil, err
		}
		if messages == nil {
			messages = []Message{}
		}
	}
	meta := TranscriptMeta{
		ID: id, Title: resolveTitle(sc, jsonlPath), Profile: sc.Profile,
		Project: m.projectForSessionsDir(dir), Pinned: sc.Pinned,
		Updated: transcriptUpdated(jsonlPath, sc, sidecarPath(dir, id)), Path: jsonlPath,
	}
	return meta, messages, nil
}

// TranscriptMarkdown renders a session as plain markdown for `chat show`
// without --json.
func TranscriptMarkdown(meta TranscriptMeta, messages []Message) string {
	var b strings.Builder
	title := meta.Title
	if title == "" {
		title = meta.ID
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	for _, msg := range messages {
		heading := "you"
		if msg.Role == "assistant" {
			heading = "agent"
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", heading, strings.TrimRight(msg.Text, "\n"))
		if msg.Error != "" {
			fmt.Fprintf(&b, "*(error: %s)*\n\n", msg.Error)
		}
	}
	return b.String()
}

// SetChatPinned sets a session's pinned flag.
func (m *Model) SetChatPinned(id string, pinned bool) error {
	dir, _, sc, err := m.FindTranscript(id)
	if err != nil {
		return err
	}
	sc.Pinned = pinned
	return WriteSidecar(dir, sc)
}

// SetChatTitle sets a session's title override.
func (m *Model) SetChatTitle(id, title string) error {
	dir, _, sc, err := m.FindTranscript(id)
	if err != nil {
		return err
	}
	sc.Title = strings.TrimSpace(title)
	return WriteSidecar(dir, sc)
}

// DeleteChat removes a session's .jsonl (if pi has written one) and its
// sidecar.
func (m *Model) DeleteChat(id string) error {
	dir, jsonlPath, _, err := m.FindTranscript(id)
	if err != nil {
		return err
	}
	if jsonlPath != "" {
		if err := os.Remove(jsonlPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(sidecarPath(dir, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
