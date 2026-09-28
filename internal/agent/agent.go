// Package agent: broker, data model, and CLI-facing verbs for the phiOS AI
// agent. The engine is pi (github.com/earendil-works/pi-coding-agent), run
// under phi-agent-contain (phios-dotfiles); this package never reads pi's
// on-disk state except the documented session JSONL it parses in
// transcript.go.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Instance is one of the two broker instances (phi-agent-broker@a1|a2).
// They differ by configuration, not capability: separate XDG trees,
// separate credentials, separate containment perimeter. a1 serves the
// general, academic and inline profiles (host network); a2 serves the
// coding profile (network-isolated behind an egress proxy). Broker code
// itself does not change with the pi migration.
type Instance string

const (
	A1 Instance = "a1"
	A2 Instance = "a2"
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

// Profile selects a pi agent-dir configuration and containment shape
// (binding contract §1).
type Profile string

const (
	General  Profile = "general"
	Academic Profile = "academic"
	Coding   Profile = "coding"
	Inline   Profile = "inline"
)

// ParseProfile validates a user-supplied profile name.
func ParseProfile(s string) (Profile, error) {
	switch Profile(s) {
	case General, Academic, Coding, Inline:
		return Profile(s), nil
	default:
		return "", fmt.Errorf("unknown profile %q (want general, academic, coding or inline)", s)
	}
}

// AllProfiles lists every profile.
func AllProfiles() []Profile { return []Profile{General, Academic, Coding, Inline} }

// ChatProfiles lists the profiles with a panel chat (§1: general, academic).
func ChatProfiles() []Profile { return []Profile{General, Academic} }

// MemoryProfiles lists the profiles with a profile-level memory directory
// (§2: profiles/<profile>/memoria.md). inline has none.
func MemoryProfiles() []Profile { return []Profile{General, Academic, Coding} }

// BrokerInstance is which broker instance a profile's pi process talks to
// (§1): coding is network-isolated behind a2; every other profile uses a1.
func (p Profile) BrokerInstance() Instance {
	if p == Coding {
		return A2
	}
	return A1
}

// HostShortName is os.Hostname() up to the first '.' — the key used in
// project.json's folders[].paths (§4). Returns "" if the hostname cannot be
// read.
func HostShortName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(h, '.'); i >= 0 {
		return h[:i]
	}
	return h
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

// DataRoot is the agent data root, $XDG_DATA_HOME/phi-agent (§2). Holds
// memory, projects and unfiled session transcripts; shared by every
// profile (unlike ConfigDir/StateDir, which stay per broker instance).
func DataRoot() (string, error) {
	d, err := dataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "phi-agent"), nil
}

// LegacyDataRoot is the pre-pi data root, DataRoot()/a1 — read-only, for
// the one-time non-destructive migration in model.go.
func LegacyDataRoot() (string, error) {
	root, err := DataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "a1"), nil
}

// ConfigDir is ~/.config/phi-agent/<instance> — the per-instance broker
// configuration root (broker.json, provider-key). This is the HOST-side
// path; inside the containment it is remapped by phi-agent-contain.
func (i Instance) ConfigDir() (string, error) {
	c, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "phi-agent", string(i)), nil
}

// StateDir is ~/.local/state/phi-agent/<instance> — broker state/meters.
func (i Instance) StateDir() (string, error) {
	s, err := stateHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(s, "phi-agent", string(i)), nil
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
