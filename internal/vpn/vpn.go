// Package vpn drives WireGuard tunnels for phiOS (settings-overhaul batch
// F). The user placed the VPN work fully in scope this session:
// wireguard-tools becomes a package, `phi vpn` the control surface, and the
// tunnel .conf files live at ~/.config/phi/wireguard/ — OUTSIDE every
// repository, because a WireGuard config holds a private key and an
// endpoint address and the GitHub remote is public (CLAUDE.md rule 5/6).
//
// ADR 067 analog, the same contract Services/Tailscale.qml states for
// itself: NOTHING in this package ever returns or logs an endpoint, an
// allowed-IPs range, or any peer address. It is enforced structurally —
// TunnelStatus has no field for one, and the `wg show` parser reads only
// the handshake age and the byte counters, skipping the endpoint and
// allowed-ips columns entirely. A future field that carried an address
// would be the violation, not anything a caller did.
//
// Privilege: `wg-quick up/down` needs root. `phi vpn` calls it through
// `sudo -n` (non-interactive), which fails cleanly with a clear message
// when profiles/desktop/system/sudoers.d/49-phi-vpn is not installed —
// that drop-in is /etc material this project writes into the repo and never
// applies. `list` and the basic up/down state need no privilege (config
// dir listing + `ip link`); only the handshake/transfer detail does.
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

// ConfigDir is ~/.config/phi/wireguard — never a repository path.
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

// List returns the tunnel names (config basenames without .conf), sorted.
func List() ([]string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
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
	sort.Strings(names)
	return names, nil
}

func configPath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return "", fmt.Errorf("invalid tunnel name %q", name)
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".conf"), nil
}

// TunnelStatus is the safe-to-show state of one tunnel. No address fields,
// by construction (ADR 067 analog).
type TunnelStatus struct {
	Name         string
	Up           bool
	HandshakeAge string // "" when down or never handshaked; else "12s", "3m", "1h"
	Rx           string // human bytes, "" when unknown
	Tx           string
}

// Status reports every tunnel (or just `name` when non-empty).
func Status(ctx context.Context, name string) ([]TunnelStatus, error) {
	var names []string
	if name != "" {
		names = []string{name}
	} else {
		var err error
		if names, err = List(); err != nil {
			return nil, err
		}
	}

	out := make([]TunnelStatus, 0, len(names))
	for _, n := range names {
		st := TunnelStatus{Name: n, Up: linkExists(ctx, n)}
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

func linkExists(ctx context.Context, iface string) bool {
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	// `ip link show <iface>` exits non-zero when the interface is absent.
	return exec.CommandContext(cctx, "ip", "link", "show", iface).Run() == nil
}

// wgPeerStats runs `sudo -n wg show <iface> dump` and extracts ONLY the
// handshake age and byte counters from the first peer line. Endpoint and
// allowed-ips columns are read past and dropped.
func wgPeerStats(ctx context.Context, iface string) (handshakeAge, rx, tx string, ok bool) {
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sudo", "-n", "wg", "show", iface, "dump")
	stdout, err := cmd.Output()
	if err != nil {
		return "", "", "", false
	}
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

// Up brings a tunnel up: `sudo -n wg-quick up <config path>`.
func Up(ctx context.Context, name string) error {
	path, err := configPath(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no config for %q at %s", name, path)
	}
	return runPrivileged(ctx, "wg-quick", "up", path)
}

// Down brings a tunnel down by interface name.
func Down(ctx context.Context, name string) error {
	if _, err := configPath(name); err != nil {
		return err
	}
	return runPrivileged(ctx, "wg-quick", "down", name)
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
