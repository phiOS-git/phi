package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"phi/internal/firewall"
)

const firewallUsage = `usage: phi firewall <verb> [arguments]

Verbs:
  status [--json]      on/off, preset, logging, allow-rules, and whether the
                       ruleset is actually loaded in the kernel
  enable | disable     turn the inbound firewall on or off (default-drop when on)
  preset NAME          home | public | paranoid
  allow PORT[/tcp|/udp] [--from CIDR]
                       open an inbound port (or N-M range); tcp if no proto
  remove ID            drop an allow-rule by its id (from ` + "`phi firewall status`" + `)
  log on | off         log dropped packets to the kernel log
  blocked [--json]     recent dropped inbound packets (needs ` + "`log on`" + `)

State lives in ~/.config/phi/firewall.json; enforcement is one nftables
table (inet phi) written to /etc/nftables.conf. up/down go through sudo -n
and need profiles/desktop/system/etc/sudoers.d/49-phi-firewall installed.
`

func runFirewall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, firewallUsage)
		return 1
	}
	ctx := context.Background()

	switch args[0] {
	case "status":
		asJSON := false
		for _, a := range args[1:] {
			if a == "--json" {
				asJSON = true
			}
		}
		st, err := firewall.Status(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "%s: firewall: %v\n", progName, err)
			return 1
		}
		if asJSON {
			b, _ := json.Marshal(st)
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		state := "off"
		if st.Enabled {
			state = "on"
		}
		fmt.Fprintf(stdout, "state\t%s\n", state)
		fmt.Fprintf(stdout, "preset\t%s\n", st.Preset)
		fmt.Fprintf(stdout, "logging\t%s\n", onOff(st.Logging))
		fmt.Fprintf(stdout, "enforced\t%s\n", st.Enforced)
		if !st.NftAvailable {
			fmt.Fprintf(stdout, "note\tnftables is not installed\n")
		}
		if len(st.Rules) == 0 {
			fmt.Fprintln(stdout, "rules\t(none)")
		} else {
			for _, r := range st.Rules {
				from := "any"
				if r.From != "" {
					from = r.From
				}
				fmt.Fprintf(stdout, "rule\t%s\t%s/%s\tfrom %s\n", r.ID, r.Port, r.Proto, from)
			}
		}
		return 0

	case "enable":
		if err := firewall.Enable(ctx); err != nil {
			fmt.Fprintf(stderr, "%s: firewall: %v\n", progName, err)
			return 1
		}
		fmt.Fprintln(stdout, "firewall enabled")
		warnLockout(stderr)
		return 0

	case "disable":
		if err := firewall.Disable(ctx); err != nil {
			fmt.Fprintf(stderr, "%s: firewall: %v\n", progName, err)
			return 1
		}
		fmt.Fprintln(stdout, "firewall disabled")
		return 0

	case "preset":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "%s: firewall: preset needs a name (%s)\n", progName, strings.Join(firewall.PresetNames, ", "))
			return 1
		}
		if err := firewall.SetPreset(ctx, args[1]); err != nil {
			fmt.Fprintf(stderr, "%s: firewall: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "preset %s\n", strings.ToLower(args[1]))
		warnLockout(stderr)
		return 0

	case "allow":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "%s: firewall: allow needs a port (e.g. 22, 443/tcp, 1714-1764/udp)\n", progName)
			return 1
		}
		port, proto := args[1], "tcp"
		if i := strings.IndexByte(port, '/'); i >= 0 {
			port, proto = port[:i], port[i+1:]
		}
		from := ""
		for i := 2; i < len(args); i++ {
			if args[i] == "--from" && i+1 < len(args) {
				from = args[i+1]
				i++
			}
		}
		r, err := firewall.Allow(ctx, firewall.Rule{Port: port, Proto: proto, From: from})
		if err != nil {
			fmt.Fprintf(stderr, "%s: firewall: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "allowed %s/%s (id %s)\n", r.Port, r.Proto, r.ID())
		return 0

	case "remove":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "%s: firewall: remove needs a rule id (see `phi firewall status`)\n", progName)
			return 1
		}
		if err := firewall.Remove(ctx, args[1]); err != nil {
			fmt.Fprintf(stderr, "%s: firewall: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "removed %s\n", args[1])
		warnLockout(stderr)
		return 0

	case "log":
		if len(args) < 2 || (args[1] != "on" && args[1] != "off") {
			fmt.Fprintf(stderr, "%s: firewall: log needs on or off\n", progName)
			return 1
		}
		if err := firewall.SetLogging(ctx, args[1] == "on"); err != nil {
			fmt.Fprintf(stderr, "%s: firewall: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "logging %s\n", args[1])
		return 0

	case "blocked":
		asJSON := false
		for _, a := range args[1:] {
			if a == "--json" {
				asJSON = true
			}
		}
		entries, err := firewall.RecentBlocked(ctx, 200)
		if err != nil {
			fmt.Fprintf(stderr, "%s: firewall: %v\n", progName, err)
			return 1
		}
		if asJSON {
			b, _ := json.Marshal(entries)
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		if len(entries) == 0 {
			fmt.Fprintln(stdout, "no blocked packets logged")
			return 0
		}
		for _, e := range entries {
			fmt.Fprintf(stdout, "%s\t%s\t%s\tdport %s\n",
				e.Time.Format("Jan 2 15:04"), nz(e.Src), nz(e.Proto), nz(e.Dport))
		}
		return 0

	case "-h", "--help":
		fmt.Fprint(stdout, firewallUsage)
		return 0

	default:
		fmt.Fprintf(stderr, "%s: firewall: unknown verb %q\n", progName, args[0])
		fmt.Fprint(stderr, firewallUsage)
		return 1
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// warnLockout prints a warning (it never fails the command) when the
// firewall as now configured would drop this SSH session on reconnect.
func warnLockout(stderr io.Writer) {
	c, err := firewall.Load()
	if err != nil {
		return
	}
	if w := firewall.LockoutRisk(c); w != "" {
		fmt.Fprintf(stderr, "%s: firewall: warning: %s\n", progName, w)
	}
}
