// Package firewall manages phiOS's inbound nftables firewall via
// /etc/nftables.conf and ~/.config/phi/firewall.json.
package firewall

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const cmdTimeout = 15 * time.Second

// nftConf: nftables config file that phi manages.
const nftConf = "/etc/nftables.conf"

// preset defines firewall behavior for a profile.
type preset struct {
	applyRules bool
	icmpEcho   bool
	fullICMP   bool // false → drop every ICMP type not required for the stack to work
	logLimit   string
}

var presets = map[string]preset{
	"home":     {applyRules: true, icmpEcho: true, fullICMP: true, logLimit: "5/second burst 10 packets"},
	"public":   {applyRules: false, icmpEcho: false, fullICMP: true, logLimit: "5/second burst 10 packets"},
	"paranoid": {applyRules: false, icmpEcho: false, fullICMP: false, logLimit: "1/second burst 5 packets"},
}

// PresetNames lists accepted profiles (loosest to strictest).
var PresetNames = []string{"home", "public", "paranoid"}

func validPreset(p string) bool {
	_, ok := presets[p]
	return ok
}

// Rule is one inbound allow rule.
type Rule struct {
	Port  string `json:"port"`
	Proto string `json:"proto"`
	From  string `json:"from,omitempty"`
}

// ID returns a stable handle for the rule.
func (r Rule) ID() string {
	h := fnv.New32a()
	fmt.Fprintf(h, "%s|%s|%s", r.Port, r.Proto, r.From)
	return fmt.Sprintf("%06x", h.Sum32()&0xffffff)
}

func (r Rule) canonical() Rule {
	r.Proto = strings.ToLower(strings.TrimSpace(r.Proto))
	r.From = strings.TrimSpace(r.From)
	r.Port = strings.TrimSpace(r.Port)
	return r
}

var portRe = regexp.MustCompile(`^(\d{1,5})(?:-(\d{1,5}))?$`)

func (r Rule) validate() error {
	if r.Proto != "tcp" && r.Proto != "udp" {
		return fmt.Errorf("proto must be tcp or udp, got %q", r.Proto)
	}
	m := portRe.FindStringSubmatch(r.Port)
	if m == nil {
		return fmt.Errorf("port must be N or N-M, got %q", r.Port)
	}
	lo, _ := strconv.Atoi(m[1])
	if lo < 1 || lo > 65535 {
		return fmt.Errorf("port %d out of range", lo)
	}
	if m[2] != "" {
		hi, _ := strconv.Atoi(m[2])
		if hi < 1 || hi > 65535 || hi < lo {
			return fmt.Errorf("port range %s is not low-high within 1-65535", r.Port)
		}
	}
	if r.From != "" {
		if _, _, err := net.ParseCIDR(r.From); err != nil {
			if net.ParseIP(r.From) == nil {
				return fmt.Errorf("from must be an IP or CIDR, got %q", r.From)
			}
		}
	}
	return nil
}

// nftFrom returns the "ip saddr X" / "ip6 saddr X" clause for a rule's From,
// or "" for any source.
func (r Rule) nftSaddr() string {
	if r.From == "" {
		return ""
	}
	ip := r.From
	if h, _, err := net.ParseCIDR(r.From); err == nil {
		ip = h.String()
	}
	if strings.Contains(ip, ":") {
		return "ip6 saddr " + r.From + " "
	}
	return "ip saddr " + r.From + " "
}

// nftDport returns the dport match clause.
func (r Rule) nftDport() string {
	return r.Port
}

// Config is the on-disk desired state.
type Config struct {
	Enabled bool   `json:"enabled"`
	Preset  string `json:"preset"`
	Logging bool   `json:"logging"`
	Rules   []Rule `json:"rules"`
}

func defaultConfig() Config {
	return Config{Enabled: false, Preset: "home", Logging: false, Rules: []Rule{}}
}

// ConfigPath is ~/.config/phi/firewall.json (honouring XDG_CONFIG_HOME).
func ConfigPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "phi", "firewall.json"), nil
}

// Load reads firewall.json, returning defaults when it does not exist yet.
func Load() (Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return Config{}, err
	}
	c := defaultConfig()
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("firewall.json: %w", err)
	}
	if !validPreset(c.Preset) {
		c.Preset = "home"
	}
	if c.Rules == nil {
		c.Rules = []Rule{}
	}
	return c, nil
}

// Save writes firewall.json at mode 0600.
func Save(c Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// --- rendering -------------------------------------------------------------

const managedHeader = "#!/usr/bin/nft -f\n" +
	"# phiOS — managed by `phi firewall`. Manual edits are lost on the next\n" +
	"# `phi firewall` command. This file scopes every change to `table inet\n" +
	"# phi`, so a container/VM runtime's own nftables tables are left alone.\n" +
	"# See `phi firewall status`.\n\n"

// Render generates an nft script scoped to `table inet phi`, idempotent,
// leaving other firewall tables untouched.
func Render(c Config) string {
	var b strings.Builder
	b.WriteString(managedHeader)

	if !c.Enabled {
		b.WriteString("# firewall disabled — the phi table is removed, every packet is accepted.\n")
		b.WriteString("add table inet phi\n")
		b.WriteString("delete table inet phi\n")
		return b.String()
	}

	p := presets[c.Preset]
	// Delete-then-recreate ensures a clean slate.
	b.WriteString("add table inet phi\n")
	b.WriteString("delete table inet phi\n")
	b.WriteString("add table inet phi\n")
	b.WriteString("add chain inet phi input { type filter hook input priority 0; policy drop; }\n\n")

	add := func(rule string) { fmt.Fprintf(&b, "add rule inet phi input %s\n", rule) }

	add("ct state established,related accept")
	add("ct state invalid drop")
	add("iif \"lo\" accept")
	b.WriteString("\n")

	// IPv6 neighbour discovery / PMTU — the stack does not work without these.
	add("icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-advert, nd-router-solicit, packet-too-big, parameter-problem, time-exceeded, destination-unreachable } accept")
	// IPv4 control messages ordinary traffic depends on.
	add("icmp type { destination-unreachable, time-exceeded, parameter-problem } accept")

	if p.fullICMP {
		add("meta l4proto { icmp, ipv6-icmp } accept")
	}
	if p.icmpEcho {
		add("icmp type echo-request accept")
		add("icmpv6 type echo-request accept")
	}
	b.WriteString("\n")

	if p.applyRules && len(c.Rules) > 0 {
		b.WriteString("# allow rules (`phi firewall allow`)\n")
		for _, r := range sortedRules(c.Rules) {
			add(fmt.Sprintf("%s%s dport %s accept", r.nftSaddr(), r.Proto, r.nftDport()))
		}
		b.WriteString("\n")
	}

	if c.Logging {
		add(fmt.Sprintf("limit rate %s log prefix \"phi-fw: \" level info", p.logLimit))
	}
	return b.String()
}

func sortedRules(rules []Rule) []Rule {
	out := append([]Rule(nil), rules...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Proto != out[j].Proto {
			return out[i].Proto < out[j].Proto
		}
		return out[i].Port < out[j].Port
	})
	return out
}

// --- apply ----------------------------------------------------------------

// Apply loads the rendered ruleset live, then persists it only on success.
func Apply(ctx context.Context, c Config) error {
	if _, err := exec.LookPath("nft"); err != nil {
		return fmt.Errorf("nft not installed (package: nftables)")
	}
	script := Render(c)

	if err := sudoStdin(ctx, script, "nft", "-f", "-"); err != nil {
		return fmt.Errorf("%s (is profiles/desktop/system/etc/sudoers.d/49-phi-firewall installed?)", err)
	}
	if err := sudoStdin(ctx, script, "tee", nftConf); err != nil {
		return fmt.Errorf("ruleset is live but was not persisted to %s: %w", nftConf, err)
	}
	return nil
}

// sudoStdin runs `sudo -n <args...>` feeding stdin, discarding stdout,
// capturing stderr for the error message.
func sudoStdin(ctx context.Context, stdin string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	full := append([]string{"-n"}, args...)
	cmd := exec.CommandContext(cctx, "sudo", full...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return fmt.Errorf("%s", firstLine(s))
		}
		return err
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// --- mutation verbs -----------------------------------------------------

// with loads config, applies fn, re-applies if enabled, saves.
func with(ctx context.Context, fn func(*Config) error) error {
	c, err := Load()
	if err != nil {
		return err
	}
	if err := fn(&c); err != nil {
		return err
	}
	if c.Enabled {
		if err := Apply(ctx, c); err != nil {
			return err
		}
	}
	return Save(c)
}

// Enable turns the firewall on, applying first.
func Enable(ctx context.Context) error {
	c, err := Load()
	if err != nil {
		return err
	}
	if c.Enabled {
		return nil
	}
	c.Enabled = true
	if err := Apply(ctx, c); err != nil {
		return err
	}
	return Save(c)
}

// Disable turns the firewall off, recording intent before applying.
func Disable(ctx context.Context) error {
	c, err := Load()
	if err != nil {
		return err
	}
	c.Enabled = false
	applyErr := Apply(ctx, c)
	if err := Save(c); err != nil {
		return err
	}
	return applyErr
}

func SetPreset(ctx context.Context, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !validPreset(name) {
		return fmt.Errorf("preset must be one of %s", strings.Join(PresetNames, ", "))
	}
	return with(ctx, func(c *Config) error { c.Preset = name; return nil })
}

func SetLogging(ctx context.Context, on bool) error {
	return with(ctx, func(c *Config) error { c.Logging = on; return nil })
}

// Allow adds an inbound rule, silently returning if it already exists.
func Allow(ctx context.Context, r Rule) (Rule, error) {
	r = r.canonical()
	if r.Proto == "" {
		r.Proto = "tcp"
	}
	if err := r.validate(); err != nil {
		return Rule{}, err
	}
	err := with(ctx, func(c *Config) error {
		for _, ex := range c.Rules {
			if ex.ID() == r.ID() {
				return nil
			}
		}
		c.Rules = append(c.Rules, r)
		return nil
	})
	return r, err
}

// Remove deletes the rule with the given id. An unknown id is an error so a
// typo is not silently ignored.
func Remove(ctx context.Context, id string) error {
	return with(ctx, func(c *Config) error {
		next := c.Rules[:0]
		found := false
		for _, r := range c.Rules {
			if r.ID() == id {
				found = true
				continue
			}
			next = append(next, r)
		}
		if !found {
			return fmt.Errorf("no rule with id %q (see `phi firewall status`)", id)
		}
		c.Rules = append([]Rule(nil), next...)
		return nil
	})
}

// --- lockout guard -----------------------------------------------------

// LockoutRisk warns if applying c would block SSH reconnection. The live
// session stays up via established-connection matching, but reboot would fail.
func LockoutRisk(c Config) string {
	if !c.Enabled {
		return ""
	}
	f := strings.Fields(os.Getenv("SSH_CONNECTION")) // client-ip client-port server-ip server-port
	if len(f) != 4 {
		return ""
	}
	clientIP, port := f[0], f[3]
	if c.permitsInboundTCP(port, clientIP) {
		return ""
	}
	from := ""
	if clientIP != "" {
		from = " --from " + clientIP
	}
	return fmt.Sprintf("you are connected over SSH on port %s and no allow-rule covers it — "+
		"this session stays up, but a reconnect or a reboot will lock you out; "+
		"run `phi firewall allow %s/tcp%s` first", port, port, from)
}

// permitsInboundTCP reports whether c's effective ruleset would accept a
// new inbound TCP connection to dport from srcIP ("" = source unknown).
func (c Config) permitsInboundTCP(dport, srcIP string) bool {
	if !presets[c.Preset].applyRules {
		return false
	}
	src := net.ParseIP(srcIP)
	for _, raw := range c.Rules {
		r := raw.canonical()
		if r.Proto != "tcp" || !portInSpec(dport, r.Port) {
			continue
		}
		if r.From == "" {
			return true
		}
		if _, cidr, err := net.ParseCIDR(r.From); err == nil {
			if src != nil && cidr.Contains(src) {
				return true
			}
			continue
		}
		if ip := net.ParseIP(r.From); ip != nil && src != nil && ip.Equal(src) {
			return true
		}
	}
	return false
}

// portInSpec reports whether the single port want falls inside spec, which
// is "N" or "N-M".
func portInSpec(want, spec string) bool {
	w, err := strconv.Atoi(strings.TrimSpace(want))
	if err != nil {
		return false
	}
	m := portRe.FindStringSubmatch(spec)
	if m == nil {
		return false
	}
	lo, _ := strconv.Atoi(m[1])
	hi := lo
	if m[2] != "" {
		hi, _ = strconv.Atoi(m[2])
	}
	return w >= lo && w <= hi
}

// --- status -------------------------------------------------------------

// RuleStatus is a Rule plus its id, for display.
type RuleStatus struct {
	ID    string `json:"id"`
	Port  string `json:"port"`
	Proto string `json:"proto"`
	From  string `json:"from,omitempty"`
}

// State is the reported firewall state (config plus a live enforcement probe).
type State struct {
	Enabled      bool         `json:"enabled"`
	Preset       string       `json:"preset"`
	Logging      bool         `json:"logging"`
	Rules        []RuleStatus `json:"rules"`
	Enforced     string       `json:"enforced"` // "yes" | "no" | "unknown"
	NftAvailable bool         `json:"nftAvailable"`
}

// Status reports config plus whether `table inet phi` is actually loaded.
func Status(ctx context.Context) (State, error) {
	c, err := Load()
	if err != nil {
		return State{}, err
	}
	st := State{
		Enabled:      c.Enabled,
		Preset:       c.Preset,
		Logging:      c.Logging,
		NftAvailable: lookPathOK("nft"),
		Enforced:     probeEnforced(ctx),
	}
	for _, r := range sortedRules(c.Rules) {
		st.Rules = append(st.Rules, RuleStatus{ID: r.ID(), Port: r.Port, Proto: r.Proto, From: r.From})
	}
	return st, nil
}

func lookPathOK(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// probeEnforced checks if `table inet phi` is loaded. Denials are memoised
// for an hour to avoid journal spam from status polls.
func probeEnforced(ctx context.Context) string {
	if probeBackedOff() {
		return "unknown"
	}
	if !lookPathOK("nft") {
		return "unknown"
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sudo", "-n", "nft", "list", "table", "inet", "phi")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		s := stderr.String()
		if strings.Contains(s, "No such file") || strings.Contains(s, "does not exist") {
			clearProbeBackoff()
			return "no"
		}
		if strings.Contains(s, "password is required") || strings.Contains(s, "not allowed") ||
			strings.Contains(s, "a terminal is required") {
			markProbeBackoff()
		}
		return "unknown"
	}
	clearProbeBackoff()
	return "yes"
}

func probeMarker() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "phi", "firewall-noprobe")
}

func probeBackedOff() bool {
	m := probeMarker()
	if m == "" {
		return false
	}
	fi, err := os.Stat(m)
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) < time.Hour
}

func markProbeBackoff() {
	m := probeMarker()
	if m == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m), 0o755)
	now := time.Now()
	if f, err := os.OpenFile(m, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_ = f.Close()
	}
	_ = os.Chtimes(m, now, now)
}

func clearProbeBackoff() {
	if m := probeMarker(); m != "" {
		_ = os.Remove(m)
	}
}

// --- blocked view -----------------------------------------------------

// Blocked is one dropped inbound packet from the kernel log.
type Blocked struct {
	Time  time.Time `json:"time"`
	Src   string    `json:"src"`
	Proto string    `json:"proto"`
	Dport string    `json:"dport"`
}

var (
	kvSrc   = regexp.MustCompile(`\bSRC=(\S+)`)
	kvProto = regexp.MustCompile(`\bPROTO=(\S+)`)
	kvDpt   = regexp.MustCompile(`\bDPT=(\S+)`)
)

// RecentBlocked reads recent "phi-fw:" kernel log lines via journalctl.
func RecentBlocked(ctx context.Context, limit int) ([]Blocked, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	// "phi-fw" pattern avoids sudoers colon-escaping issues.
	cmd := exec.CommandContext(cctx, "sudo", "-n", "journalctl",
		"-k", "--no-pager", "-o", "json", "-g", "phi-fw", "-n", "200")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cannot read the kernel log (sudoers drop-in installed? logging on?): %w", err)
	}
	var res []Blocked
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var rec struct {
			Msg  string `json:"MESSAGE"`
			TsUS string `json:"__REALTIME_TIMESTAMP"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || !strings.Contains(rec.Msg, "phi-fw:") {
			continue
		}
		b := Blocked{Proto: firstGroup(kvProto, rec.Msg), Src: firstGroup(kvSrc, rec.Msg), Dport: firstGroup(kvDpt, rec.Msg)}
		if us, e := strconv.ParseInt(rec.TsUS, 10, 64); e == nil {
			b.Time = time.UnixMicro(us)
		}
		res = append(res, b)
	}
	// newest first, capped
	for i, j := 0, len(res)-1; i < j; i, j = i+1, j-1 {
		res[i], res[j] = res[j], res[i]
	}
	if len(res) > limit {
		res = res[:limit]
	}
	return res, nil
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}
