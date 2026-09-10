// Package firewall manages phiOS's inbound firewall through nftables
// (Out-of-plan: firewall). architettura §6.6 left the backend [TBD] between
// nftables, ufw and firewalld; the user chose nftables directly — it is in
// the base system (T0), needs no daemon (nftables.service is a one-shot that
// loads one file), and has no abstraction layer to fight when the settings
// panel wants fine control.
//
// Model: phi owns /etc/nftables.conf outright. When enabled the file is one
// `table inet phi` with a single `input` base chain, policy drop. In
// nftables an `accept` from any base chain is not final — evaluation
// continues to the next chain — but a `drop` is, and a chain's policy drop
// takes effect for every packet no rule in that chain accepted. So phi's
// default-drop applies to EVERY inbound packet on this hook, including
// traffic a container/VM runtime's own nftables table would have accepted:
// a published container port needs a matching `phi firewall allow`. The
// scoped `add`/`delete table inet phi` (never `flush ruleset`) keeps phi
// from deleting those other tables' rules — it does not let their accepts
// punch through phi's chain. `disable` removes `table inet phi` entirely
// (accept all). nftables.service loads the file on boot, so persistence
// needs no extra unit.
//
// Desired state lives at ~/.config/phi/firewall.json — nested JSON, not the
// closed `phi state` key set (same call Services/Chroma makes for
// chroma.json). Every verb mutates that file, re-renders, and (while enabled)
// re-applies. The two privileged steps — `nft -f -` to load and `tee
// /etc/nftables.conf` to persist — go through `sudo -n`; the drop-in that
// allows them without a password is profiles/*/system/etc/sudoers.d/
// 49-phi-firewall, /etc material this repo ships and never applies, exactly
// like 49-phi-vpn.
//
// ADR 067 analog: nothing here emits one of the user's own addresses. The
// `blocked` view reads the kernel log for the "phi-fw:" prefix and reports
// the source and destination port of packets the firewall dropped — an
// unsolicited scanner's own fields, never any phiOS config or peer address.
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

// nftConf is the single file phi owns. nftables.service loads it on boot.
const nftConf = "/etc/nftables.conf"

// Presets. They differ only in whether the user's allow-rules are honoured,
// whether ICMP echo (ping) is answered, whether non-essential ICMP is kept
// at all, and how hard the log rule is rate-limited.
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

// PresetNames is the accepted set, ordered loosest → strictest.
var PresetNames = []string{"home", "public", "paranoid"}

func validPreset(p string) bool {
	_, ok := presets[p]
	return ok
}

// Rule is one inbound allow. Port is "22" or a "1714-1764" range; Proto is
// "tcp" or "udp"; From is a CIDR (or bare IP) that "" leaves as any source.
type Rule struct {
	Port  string `json:"port"`
	Proto string `json:"proto"`
	From  string `json:"from,omitempty"`
}

// ID is a short stable handle for `phi firewall remove` — derived from the
// rule itself, so the same rule always has the same id and a duplicate add
// is a no-op.
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

// nftDport is the dport match — a bare port or an "N-M" range, both of which
// nftables accepts verbatim.
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

// Save writes firewall.json (0600 — it is not secret, but neither is it
// anyone else's business), creating ~/.config/phi if needed.
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

// Render turns a Config into an nft script — the same text is loaded live
// (`nft -f -`) and written to /etc/nftables.conf for nftables.service to
// load at boot. Every statement is scoped to `table inet phi`: `add` is
// idempotent, `flush table` clears only our table, and the disabled form
// creates-then-deletes it. No `flush ruleset`, so docker/libvirt/podman
// tables are never touched.
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
	// Delete-then-recreate rather than flush: a clean slate with no chance of
	// a stale chain spec or leftover rule, and `delete` after `add` never
	// errors on a missing table.
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

// Apply loads the rendered ruleset live (`nft -f -`) and, only if that
// succeeds, persists it to /etc/nftables.conf. A render bug therefore never
// leaves a file that breaks the next boot. Both steps are `sudo -n`.
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

// with loads the config, applies fn, re-applies the firewall when it is
// enabled, and saves — in that order, so a failed apply leaves firewall.json
// untouched and `status` still reflects reality.
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

// Enable turns the firewall on. It applies first and only records
// enabled=true if that worked — a denied `sudo` leaves the config off.
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

// Disable turns it off. It always records the intent (so the state is not
// stuck) and applies the accept-all ruleset best-effort.
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

// Allow adds an inbound rule. A duplicate (same port/proto/source) is a
// silent no-op. It returns the rule that is now present.
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

// LockoutRisk returns a warning when this process is running over SSH and
// applying c would refuse a *fresh* inbound connection on the SSH server
// port. The live `nft -f -` reload keeps the current session up — it
// matches `ct state established,related accept` — but a reconnect, or a
// reboot (nftables.service reloads /etc/nftables.conf from a clean state),
// would be dropped. It returns "" when there is no SSH session or the port
// stays reachable. Callers print it; they never fail on it.
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

// Status reports config plus a live probe of whether `table inet phi` is
// actually loaded — so a drift between "enabled in the file" and "enforced
// in the kernel" (a denied sudo, a manual `nft flush`) is visible.
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

// probeEnforced runs `sudo -n nft list table inet phi`. Exit 0 → the table
// is loaded ("yes"); a clean "No such file or directory" → "no"; anything
// else (no nft, denied sudo) → "unknown". A denial is memoised for an hour
// so the shell's 15s status poll does not write a journal line every time.
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

// RecentBlocked reads the last of the "phi-fw:" kernel log lines through
// `sudo -n journalctl` (the kernel log is root-only on Arch — dmesg_restrict).
// Needs `c.Logging` to have been on for anything to be there.
func RecentBlocked(ctx context.Context, limit int) ([]Blocked, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	// Fixed argument vector so the sudoers drop-in can pin it exactly. The
	// grep pattern is "phi-fw" without the trailing colon on purpose: a
	// literal ':' in a sudoers command argument is a metacharacter that
	// must be backslash-escaped, and an unescaped one is a hard parse error
	// (`visudo -cf` rejects the whole file). "phi-fw" matches a superset;
	// the exact "phi-fw:" filter below narrows it back down in-process.
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
