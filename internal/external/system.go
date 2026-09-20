package external

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const cmdTimeout = 10 * time.Second

// run executes name with args and returns its combined, trimmed output.
// available is false when name is not on PATH — the caller's signal to
// report an empty population rather than an error, since a missing manager
// (no flatpak, no podman) is an environment fact, not machine drift.
func run(ctx context.Context, name string, args ...string) (output string, available bool, err error) {
	if _, lookErr := exec.LookPath(name); lookErr != nil {
		return "", false, lookErr
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	return strings.TrimSpace(buf.String()), true, err
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// systemEnumerator is the real-system Enumerator: pacman, flatpak, podman
// and the filesystem, none of which exist on the darwin machine this
// package is built on — every test supplies its own fake instead.
type systemEnumerator struct{}

func (systemEnumerator) Flatpak(ctx context.Context) ([]string, error) {
	out, avail, err := run(ctx, "flatpak", "list", "--app", "--columns=application")
	if !avail {
		return nil, nil
	}
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

func (systemEnumerator) AppImages() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(filepath.Join(home, "Applications"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".appimage") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (systemEnumerator) Opt() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(filepath.Join(home, ".local", "opt"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (systemEnumerator) Containers(ctx context.Context) ([]string, error) {
	out, avail, err := run(ctx, "podman", "ps", "-a", "--format", "{{.Names}}")
	if !avail {
		return nil, nil
	}
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

func (systemEnumerator) ListDir(path string) ([]string, error) {
	ents, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out, nil
}

func (systemEnumerator) Manifest() ([]string, error) {
	path := manifestPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}
		out = append(out, fields[1]) // target, relative to $HOME
	}
	return out, nil
}

// manifestPath is $XDG_STATE_HOME/phios/manifest, the installer's own state
// file (bin/lib/manifest.sh) — read-only here, never written: it belongs to
// bin/phios-install, and this package only ever consults it.
func manifestPath() string {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "phios", "manifest")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "phios", "manifest")
}

// npmPrefixer is an optional capability of an Enumerator, asserted for
// rather than added to the frozen Enumerator interface. It exists because
// the npm leak check ("~/.npm-global, and the npm prefix if npm is on
// PATH") needs a shell-out that only makes sense for the real system: a
// test fake simply doesn't implement it, and Audit treats that exactly
// like "no npm prefix to check" rather than an error.
type npmPrefixer interface {
	NpmPrefix() (string, bool)
}

func (systemEnumerator) NpmPrefix() (string, bool) {
	if _, err := exec.LookPath("npm"); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", "config", "get", "prefix")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", false
	}
	prefix := strings.TrimSpace(buf.String())
	if prefix == "" {
		return "", false
	}
	return prefix, true
}
