package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// A2 session metadata store, phios-agente-delta.md D-07. `phi agent code`
// records one JSON file per coding session OUTSIDE the containment; the shell
// panel reads these files. There is NO inbound network path into A2's
// namespace (ADR 084 — no shared path/tool/credential between A1 and A2).
//
//	~/.local/state/phi-agent/a2/sessions/<id>.json

// SessionRecord is one A2 coding session.
type SessionRecord struct {
	ID             string    `json:"id"`
	Dir            string    `json:"dir"`
	Status         string    `json:"status"` // "active" | "ended"
	Started        time.Time `json:"started"`
	Ended          time.Time `json:"ended,omitempty"`
	PID            int       `json:"pid,omitempty"`
	WindowAddr     string    `json:"window_addr,omitempty"` // Hyprland window address, if launched in a terminal
	TranscriptPath string    `json:"transcript_path,omitempty"`
	ExitNote       string    `json:"exit_note,omitempty"`
}

func sessionsDir() (string, error) {
	sd, err := A2.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sd, "sessions"), nil
}

func sessionPath(id string) (string, error) {
	if err := checkSegment(id); err != nil {
		return "", err
	}
	d, err := sessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, id+".json"), nil
}

// NewSessionID mints a short, sortable, filesystem-safe session id.
func NewSessionID() string {
	return time.Now().UTC().Format("20060102-150405")
}

// RecordSessionStart writes the initial record for a coding session.
func RecordSessionStart(rec SessionRecord) error {
	if rec.ID == "" {
		rec.ID = NewSessionID()
	}
	if err := checkSegment(rec.ID); err != nil {
		return err
	}
	if rec.Started.IsZero() {
		rec.Started = time.Now().UTC()
	}
	rec.Status = "active"
	return writeSession(rec)
}

// RecordSessionEnd marks a session ended, keeping the rest of the record.
func RecordSessionEnd(id, note string) error {
	rec, err := GetSession(id)
	if err != nil {
		return err
	}
	rec.Status = "ended"
	rec.Ended = time.Now().UTC()
	if note != "" {
		rec.ExitNote = note
	}
	return writeSession(rec)
}

// UpdateSession applies a partial update (non-zero fields overwrite).
func UpdateSession(id string, fn func(*SessionRecord)) error {
	rec, err := GetSession(id)
	if err != nil {
		return err
	}
	fn(&rec)
	return writeSession(rec)
}

func writeSession(rec SessionRecord) error {
	p, err := sessionPath(rec.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// GetSession reads one record.
func GetSession(id string) (SessionRecord, error) {
	p, err := sessionPath(id)
	if err != nil {
		return SessionRecord{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return SessionRecord{}, err
	}
	var rec SessionRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return SessionRecord{}, fmt.Errorf("session %q: %w", id, err)
	}
	return rec, nil
}

// ListSessions returns every recorded coding session, active first, then most
// recent. A record whose process is gone but still marked "active" is
// reconciled to "ended" on read (best-effort — the wrapper normally does this).
func ListSessions() ([]SessionRecord, error) {
	d, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []SessionRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec, err := GetSession(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		if rec.Status == "active" && rec.PID > 0 && !processAlive(rec.PID) {
			rec.Status = "ended"
			if rec.Ended.IsZero() {
				rec.Ended = time.Now().UTC()
			}
			_ = writeSession(rec)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i].Status == "active", out[j].Status == "active"
		if ai != aj {
			return ai
		}
		return out[i].Started.After(out[j].Started)
	})
	return out, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, signal 0 probes existence without delivering anything.
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
