package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A project's allegati/ (§2), the material the agent may read but that phi
// alone manages the lifecycle of: added and removed from outside the
// containment, never by the agent itself.

// Attachment is one file or directory under a project's allegati/.
type Attachment struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	IsDir    bool      `json:"isDir"`
	Modified time.Time `json:"modified"`
}

func (m *Model) allegatiDir(project string) (string, error) {
	if !m.HasProject(project) {
		return "", fmt.Errorf("no such project: %q", project)
	}
	return filepath.Join(m.projectDir(project), "allegati"), nil
}

// Attachments lists project's allegati/, sorted by name. A project with no
// attachments yet returns an empty slice, not nil.
func (m *Model) Attachments(project string) ([]Attachment, error) {
	dir, err := m.allegatiDir(project)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Attachment{}, nil
		}
		return nil, err
	}
	out := make([]Attachment, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue // a file removed between ReadDir and Stat: skip it
		}
		var size int64
		if !info.IsDir() {
			size = info.Size()
		}
		out = append(out, Attachment{Name: e.Name(), Size: size, IsDir: info.IsDir(), Modified: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AddAttachment copies src (a file or a directory tree) into project's
// allegati/ under its own base name, refusing to overwrite an attachment
// that already exists. Returns the name it was stored under.
func (m *Model) AddAttachment(project, src string) (string, error) {
	dir, err := m.allegatiDir(project)
	if err != nil {
		return "", err
	}
	name := filepath.Base(src)
	if err := checkSegment(name); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("attachment %q already exists", name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	fi, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if fi.IsDir() {
		if err := copyTree(src, dst); err != nil {
			return "", err
		}
	} else if err := copyAttachmentFile(src, dst); err != nil {
		return "", err
	}
	return name, nil
}

// copyAttachmentFile copies one regular file, unlike copyFileIfAbsent (which
// silently no-ops on a missing source) or copyTree (which only walks
// directories): AddAttachment already checked the destination is free, so a
// read failure here should surface as a real error.
func copyAttachmentFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// RemoveAttachment deletes one named attachment (file or directory) from
// project's allegati/. checkSegment keeps it from ever resolving outside
// that directory.
func (m *Model) RemoveAttachment(project, name string) error {
	dir, err := m.allegatiDir(project)
	if err != nil {
		return err
	}
	if err := checkSegment(name); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(dir, name))
}
