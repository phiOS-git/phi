package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A health summary of the agent system for Settings › AI Agent (plan §8),
// so the shell never reads these files itself: systemd unit state, which profiles have a
// models.json and what it declares (never the key), the broker configs
// (never the key), the tinyproxy whitelist size, and the two XDG roots.
// Best-effort throughout — a check that cannot run reports "unknown" rather
// than failing the whole call, the same posture as `phi doctor`.

// agentUnits are the systemd --user units the panel's status page reports,
// in display order.
var agentUnits = []string{
	"phi-agent.service",
	"phi-agent-broker@a1.service",
	"phi-agent-broker@a2.service",
	"phi-agent-proxy.service",
	"phi-agent-net-bridge.service",
}

// statusModelProfiles: which profiles CollectStatus looks for a models.json
// under (inline has none of its own — it reuses whichever chat profile
// invoked it).
var statusModelProfiles = []Profile{General, Academic, Coding}

// UnitStatus is one systemd --user unit's state.
type UnitStatus struct {
	Name    string `json:"name"`
	Active  string `json:"active"`
	Enabled string `json:"enabled"`
}

// ProviderInfo is one entry of a profile's models.json providers map —
// never the API key.
type ProviderInfo struct {
	Name    string   `json:"name"`
	BaseURL string   `json:"baseUrl"`
	Models  []string `json:"models"`
}

// ProfileModels is whether a profile has a models.json and, if so, what it
// declares.
type ProfileModels struct {
	Present   bool           `json:"present"`
	Providers []ProviderInfo `json:"providers"`
}

// BrokerSummary is one broker instance's configuration, with the secret
// (the provider key) reduced to a presence flag.
type BrokerSummary struct {
	Configured bool   `json:"configured"`
	Upstream   string `json:"upstream"`
	Listen     string `json:"listen"`
	AuthHeader string `json:"authHeader"`
	RateLimit  string `json:"rateLimit"`
	KeyPresent bool   `json:"keyPresent"`
	Requests   int    `json:"requests"`
}

// Status is the answer to `phi agent status` and the panel's status page.
type Status struct {
	Units            []UnitStatus             `json:"units"`
	Profiles         map[string]ProfileModels `json:"profiles"`
	Brokers          map[string]BrokerSummary `json:"brokers"`
	WhitelistEntries int                      `json:"whitelistEntries"`
	ConfigRoot       string                   `json:"configRoot"`
	StateRoot        string                   `json:"stateRoot"`
}

// systemctlOutput runs `systemctl --user <args...>` with a 3 s timeout and
// returns its trimmed stdout, "" on any failure (a stopped, masked or
// not-found unit all exit non-zero here — unitStatus turns that into
// "unknown" rather than treating it as an error). A package var so tests
// can stub it without a real systemd user session.
var systemctlOutput = func(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).Output()
	return strings.TrimSpace(string(out))
}

func unitStatus(name string) UnitStatus {
	active := systemctlOutput("is-active", name)
	if active == "" {
		active = "unknown"
	}
	enabled := systemctlOutput("is-enabled", name)
	if enabled == "" {
		enabled = "unknown"
	}
	return UnitStatus{Name: name, Active: active, Enabled: enabled}
}

// modelsJSONProvider is the subset of one profile's models.json provider
// entry CollectStatus reports; apiKey is deliberately absent from this
// struct so it is never read, let alone returned.
type modelsJSONProvider struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	Models  []struct {
		ID string `json:"id"`
	} `json:"models"`
}

type modelsJSONFile struct {
	Providers map[string]modelsJSONProvider `json:"providers"`
}

// loadProfileModels reads <configHome>/phi-agent/pi/profiles/<profile>/models.json.
// A missing or unparseable file just means Present: false.
func loadProfileModels(configHome string, profile Profile) ProfileModels {
	path := filepath.Join(configHome, "phi-agent", "pi", "profiles", string(profile), "models.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return ProfileModels{}
	}
	var mf modelsJSONFile
	if err := json.Unmarshal(b, &mf); err != nil {
		return ProfileModels{}
	}
	names := make([]string, 0, len(mf.Providers))
	for name := range mf.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	providers := make([]ProviderInfo, 0, len(names))
	for _, name := range names {
		pr := mf.Providers[name]
		displayName := pr.Name
		if displayName == "" {
			displayName = name
		}
		ids := make([]string, 0, len(pr.Models))
		for _, mdl := range pr.Models {
			ids = append(ids, mdl.ID)
		}
		providers = append(providers, ProviderInfo{Name: displayName, BaseURL: pr.BaseURL, Models: ids})
	}
	return ProfileModels{Present: true, Providers: providers}
}

// loadBrokerSummary reads <ConfigDir>/broker.json directly (not via
// LoadBroker, which also demands a working provider key and would turn an
// incomplete config into an error rather than a partial status).
func loadBrokerSummary(inst Instance) BrokerSummary {
	dir, err := inst.ConfigDir()
	if err != nil {
		return BrokerSummary{}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "broker.json"))
	if err != nil {
		return BrokerSummary{}
	}
	var cfg BrokerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return BrokerSummary{}
	}

	rateLimit := "unlimited"
	if cfg.RateLimit.Requests > 0 {
		w := cfg.RateLimit.WindowSeconds
		if w <= 0 {
			w = 60
		}
		rateLimit = fmt.Sprintf("%d req / %ds", cfg.RateLimit.Requests, w)
	}

	_, _, keyErr := loadProviderKey(dir)

	requests := 0
	if sd, err := inst.StateDir(); err == nil {
		requests = countMeterLines(filepath.Join(sd, "broker-meter.jsonl"))
	}

	return BrokerSummary{
		Configured: true,
		Upstream:   cfg.Upstream,
		Listen:     cfg.Listen,
		AuthHeader: cfg.AuthHeader,
		RateLimit:  rateLimit,
		KeyPresent: keyErr == nil,
		Requests:   requests,
	}
}

// countMeterLines counts non-blank lines in a broker meter JSONL file; a
// missing file counts as 0 requests, not an error.
func countMeterLines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// countWhitelistEntries counts non-blank, non-comment lines in the tinyproxy
// whitelist. -1 means the file could not be read (not "zero entries").
func countWhitelistEntries(configHome string) int {
	b, err := os.ReadFile(filepath.Join(configHome, "phi-agent", "tinyproxy", "whitelist"))
	if err != nil {
		return -1
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
	}
	return n
}

// CollectStatus gathers everything `phi agent status` reports. It never
// fails outright: a piece it cannot read (no XDG home, no systemd session,
// no broker configured yet) just reports its own zero value.
func CollectStatus() Status {
	units := make([]UnitStatus, 0, len(agentUnits))
	for _, u := range agentUnits {
		units = append(units, unitStatus(u))
	}

	cfgHome, cfgErr := configHome()
	stHome, stErr := stateHome()

	profiles := make(map[string]ProfileModels, len(statusModelProfiles))
	for _, p := range statusModelProfiles {
		if cfgErr == nil {
			profiles[string(p)] = loadProfileModels(cfgHome, p)
		} else {
			profiles[string(p)] = ProfileModels{}
		}
	}

	brokers := map[string]BrokerSummary{
		string(A1): loadBrokerSummary(A1),
		string(A2): loadBrokerSummary(A2),
	}

	whitelist := -1
	if cfgErr == nil {
		whitelist = countWhitelistEntries(cfgHome)
	}

	var configRoot, stateRoot string
	if cfgErr == nil {
		configRoot = filepath.Join(cfgHome, "phi-agent")
	}
	if stErr == nil {
		stateRoot = filepath.Join(stHome, "phi-agent")
	}

	return Status{
		Units: units, Profiles: profiles, Brokers: brokers,
		WhitelistEntries: whitelist, ConfigRoot: configRoot, StateRoot: stateRoot,
	}
}

// BrokerRequest is one line of a broker's meter log (plan §5.8's
// GET /broker/requests), field-for-field what meter.go's meterRecord writes.
type BrokerRequest struct {
	Time        string `json:"time"`
	Instance    string `json:"instance"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	Model       string `json:"model"`
	DurMillis   int64  `json:"dur_ms"`
	RespBytes   int64  `json:"resp_bytes"`
	RateLimited bool   `json:"rate_limited"`
}

// RecentBrokerRequests returns the last limit records of
// StateDir/broker-meter.jsonl, newest first. A missing meter file (no
// request has been brokered yet) is not an error.
func RecentBrokerRequests(inst Instance, limit int) ([]BrokerRequest, error) {
	sd, err := inst.StateDir()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(sd, "broker-meter.jsonl"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []BrokerRequest{}, nil
		}
		return nil, err
	}

	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}

	out := make([]BrokerRequest, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- { // newest first
		var r BrokerRequest
		if err := json.Unmarshal([]byte(lines[i]), &r); err != nil {
			continue // tolerate a corrupt or partial trailing line
		}
		out = append(out, r)
	}
	return out, nil
}
