// Package agent: broker, MCP server, project/ask subcommands. Domain logic
// with thin view layer. Engine (opencode) and containment elsewhere.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Instance is one of the two configured opencode instances. They differ by
// configuration, not capability: separate XDG trees, separate
// credentials, separate containment perimeter.
type Instance string

const (
	A1 Instance = "a1" // assistant: no shell, writes to output/ and proposte/
	A2 Instance = "a2" // worker: has a shell, writes to the code projects root
)

// ParseInstance validates a user-supplied instance name.
func ParseInstance(s string) (Instance, error) {
	switch Instance(s) {
	case A1:
		return A1, nil
	case A2:
		return A2, nil
	default:
		return "", fmt.Errorf("unknown instance %q (want a1 or a2)", s)
	}
}

// configHome is $XDG_CONFIG_HOME or ~/.config — the same rule the rest of
// phiOS uses (bin/lib/common.sh, internal/state).
func configHome() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

// stateHome is $XDG_STATE_HOME or ~/.local/state.
func stateHome() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}

// dataHome is $XDG_DATA_HOME or ~/.local/share.
func dataHome() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// ConfigDir is ~/.config/phi-agent/<instance> — the per-instance
// configuration root (broker.json, opencode/, provider-key). This is the
// HOST-side path; inside the containment it is remapped by phi-agent-contain.
func (i Instance) ConfigDir() (string, error) {
	c, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "phi-agent", string(i)), nil
}

// StateDir is ~/.local/state/phi-agent/<instance>.
func (i Instance) StateDir() (string, error) {
	s, err := stateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(s, "phi-agent", string(i)), nil
}

// DataDir is ~/.local/share/phi-agent/<instance> — for A1 this is also the
// root of the data model (personalita/, projects/).
func (i Instance) DataDir() (string, error) {
	d, err := dataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "phi-agent", string(i)), nil
}

// AgentConfigHome is ~/.config/phi-agent (shared parent of both instances).
func AgentConfigHome() (string, error) {
	c, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "phi-agent"), nil
}

// readTrimmedFile reads a file and trims surrounding whitespace, the shape
// every credential and marker file in this package uses.
func readTrimmedFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
