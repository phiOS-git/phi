// Package vpn drives WireGuard tunnels. Configs at ~/.config/phi/wireguard/
// (outside repo, for private keys). No endpoint, IP or peer logged.
package vpn

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const cmdTimeout = 15 * time.Second

// etcDir is wg-quick's default location, read best-effort for .conf basenames.
const etcDir = "/etc/wireguard"

// ConfigDir is ~/.config/phi/wireguard.
func ConfigDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "phi", "wireguard"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "phi", "wireguard"), nil
}

// confNames returns *.conf basenames in dir, nil on missing/unreadable.
func confNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := strings.TrimSuffix(e.Name(), ".conf"); n != e.Name() {
			names = append(names, n)
		}
	}
	return names
}

// managedNames lists the tunnels in ~/.config/phi/wireguard.
func managedNames() []string {
	dir, err := ConfigDir()
	if err != nil {
		return nil
	}
	return confNames(dir)
}

// isManaged reports whether name has a config file in the managed directory.
func isManaged(name string) bool {
	for _, n := range managedNames() {
		if n == name {
			return true
		}
	}
	return false
}

// activeInterfaces lists WireGuard interfaces currently up via ip link (unprivileged).
func activeInterfaces(ctx context.Context) []string {
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ip", "-o", "link", "show", "type", "wireguard").Output()
	if err != nil {
		return nil
	}
	var names []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		// `ip -o link` line: "3: wg0: <POINTOPOINT,...> mtu 1420 ..." — the
		// interface name is the second colon-separated field, trimmed.
		fields := strings.SplitN(sc.Text(), ":", 3)
		if len(fields) < 2 {
			continue
		}
		if n := strings.TrimSpace(fields[1]); n != "" {
			// A "@" suffix (e.g. "wg0@if3") is only on stacked links; plain
			// wireguard interfaces have none, but strip it defensively.
			if i := strings.IndexByte(n, '@'); i >= 0 {
				n = n[:i]
			}
			names = append(names, n)
		}
	}
	return names
}

// List returns all tunnel names: managed configs, /etc/wireguard, and active interfaces.
func List() ([]string, error) {
	set := map[string]bool{}
	for _, n := range managedNames() {
		set[n] = true
	}
	for _, n := range confNames(etcDir) {
		set[n] = true
	}
	for _, n := range activeInterfaces(context.Background()) {
		set[n] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// validName rejects names that could escape directories or aren't plausible WireGuard names.
func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\ \t\n") {
		return fmt.Errorf("invalid tunnel name %q", name)
	}
	return nil
}

func configPath(name string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".conf"), nil
}

// TunnelStatus is the safe-to-show state of one tunnel. No address fields.
type TunnelStatus struct {
	Name         string
	Up           bool
	Managed      bool   // config lives in ~/.config/phi/wireguard — import/forget apply
	Origin       string // "managed" | "etc" | "external"
	HandshakeAge string // "" when down or never handshaked; else "12s", "3m", "1h"
	Rx           string // human bytes, "" when unknown
	Tx           string
}

// Status reports every tunnel (or just `name` when non-empty).
func Status(ctx context.Context, name string) ([]TunnelStatus, error) {
	managed := map[string]bool{}
	for _, n := range managedNames() {
		managed[n] = true
	}
	inEtc := map[string]bool{}
	for _, n := range confNames(etcDir) {
		inEtc[n] = true
	}
	// One `ip link` call for the whole set, not one per tunnel.
	active := map[string]bool{}
	for _, n := range activeInterfaces(ctx) {
		active[n] = true
	}

	var names []string
	if name != "" {
		names = []string{name}
	} else {
		set := map[string]bool{}
		for n := range managed {
			set[n] = true
		}
		for n := range inEtc {
			set[n] = true
		}
		for n := range active {
			set[n] = true
		}
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
	}

	out := make([]TunnelStatus, 0, len(names))
	for _, n := range names {
		st := TunnelStatus{Name: n, Up: active[n], Managed: managed[n]}
		switch {
		case managed[n]:
			st.Origin = "managed"
		case inEtc[n]:
			st.Origin = "etc"
		default:
			st.Origin = "external"
		}
		if st.Up {
			if hs, rx, tx, ok := wgPeerStats(ctx, n); ok {
				st.HandshakeAge = hs
				st.Rx, st.Tx = rx, tx
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// noDetailBackoff is how long a sudo denial suppresses wg show attempts.
const noDetailBackoff = time.Hour

// noDetailMarker is ~/.cache/phi/vpn-nodetail. Its mtime is the last denial.
func noDetailMarker() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "phi", "vpn-nodetail")
}

func detailBackedOff() bool {
	m := noDetailMarker()
	if m == "" {
		return false
	}
	fi, err := os.Stat(m)
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) < noDetailBackoff
}

func markDetailDenied() {
	m := noDetailMarker()
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

func clearDetailDenied() {
	if m := noDetailMarker(); m != "" {
		_ = os.Remove(m)
	}
}

// wgPeerStats extracts handshake age and byte counters from wg show output.
func wgPeerStats(ctx context.Context, iface string) (handshakeAge, rx, tx string, ok bool) {
	if detailBackedOff() {
		return "", "", "", false
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sudo", "-n", "wg", "show", iface, "dump")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		// Only a sudo denial (no drop-in) earns the hour-long backoff; a
		// timeout or a transient `wg` failure just fails this one poll.
		if s := stderr.String(); strings.Contains(s, "password is required") ||
			strings.Contains(s, "not allowed") || strings.Contains(s, "a terminal is required") {
			markDetailDenied()
		}
		return "", "", "", false
	}
	clearDetailDenied()
	sc := bufio.NewScanner(strings.NewReader(string(stdout)))
	line := 0
	for sc.Scan() {
		line++
		if line == 1 {
			continue // interface line: private/public key, port — skip entirely
		}
		// peer line: public-key, preshared-key, endpoint, allowed-ips,
		// latest-handshake, rx, tx, keepalive. We take fields 5..7 only.
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 7 {
			continue
		}
		hsUnix, _ := strconv.ParseInt(f[4], 10, 64)
		rxB, _ := strconv.ParseInt(f[5], 10, 64)
		txB, _ := strconv.ParseInt(f[6], 10, 64)
		age := "never"
		if hsUnix > 0 {
			age = shortDuration(time.Since(time.Unix(hsUnix, 0)))
		}
		return age, humanBytes(rxB), humanBytes(txB), true
	}
	return "", "", "", false
}

// Up brings a tunnel up, using the managed config path if it exists.
func Up(ctx context.Context, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	if path, err := configPath(name); err == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return runPrivileged(ctx, "wg-quick", "up", path)
		}
	}
	// Not a managed config — let wg-quick find /etc/wireguard/<name>.conf.
	return runPrivileged(ctx, "wg-quick", "up", name)
}

// Down brings a tunnel down by interface name.
func Down(ctx context.Context, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	return runPrivileged(ctx, "wg-quick", "down", name)
}

// Import copies a WireGuard .conf into ~/.config/phi/wireguard, validating
// that it has an [Interface] section.
func Import(src, name string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", src, err)
	}
	if !strings.Contains(string(data), "[Interface]") {
		return "", fmt.Errorf("%s does not look like a WireGuard config (no [Interface] section)", src)
	}
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(src), ".conf")
	}
	dst, err := configPath(name)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("a tunnel named %q is already managed (%s) — forget it first or import with a different name", name, dst)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

// Forget deletes a managed tunnel's config.
func Forget(name string) error {
	if err := validName(name); err != nil {
		return err
	}
	if !isManaged(name) {
		return fmt.Errorf("%q is not a managed tunnel — phi only removes configs in ~/.config/phi/wireguard", name)
	}
	path, err := configPath(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func runPrivileged(ctx context.Context, args ...string) error {
	if _, err := exec.LookPath("wg-quick"); err != nil {
		return fmt.Errorf("wg-quick not installed (package: wireguard-tools)")
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	full := append([]string{"-n"}, args...)
	cmd := exec.CommandContext(cctx, "sudo", full...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s (is profiles/desktop/system/sudoers.d/49-phi-vpn installed?)", msg)
	}
	return nil
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
