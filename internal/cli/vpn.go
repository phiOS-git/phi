package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"phi/internal/vpn"
)

const vpnUsage = `usage: phi vpn <verb> [arguments]

Verbs:
  list              list the tunnel names in ~/.config/phi/wireguard
  status [NAME] [--json]
                    per-tunnel up/down, handshake age and transfer — never
                    an endpoint or an address (ADR 067)
  up NAME           bring a tunnel up   (sudo -n wg-quick up)
  down NAME         bring a tunnel down (sudo -n wg-quick down)

Tunnel configs live at ~/.config/phi/wireguard/<name>.conf, outside every
repository. up/down need the sudoers drop-in
profiles/desktop/system/sudoers.d/49-phi-vpn installed.
`

func runVpn(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, vpnUsage)
		return 1
	}
	ctx := context.Background()
	switch args[0] {
	case "list":
		names, err := vpn.List()
		if err != nil {
			fmt.Fprintf(stderr, "%s: vpn: %v\n", progName, err)
			return 1
		}
		for _, n := range names {
			fmt.Fprintln(stdout, n)
		}
		return 0

	case "status":
		name, asJSON := "", false
		for _, a := range args[1:] {
			if a == "--json" {
				asJSON = true
			} else {
				name = a
			}
		}
		st, err := vpn.Status(ctx, name)
		if err != nil {
			fmt.Fprintf(stderr, "%s: vpn: %v\n", progName, err)
			return 1
		}
		if asJSON {
			b, _ := json.Marshal(st)
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		for _, t := range st {
			state := "down"
			if t.Up {
				state = "up"
			}
			line := fmt.Sprintf("%s\t%s", t.Name, state)
			if t.Up {
				line += fmt.Sprintf("\thandshake %s\trx %s\ttx %s", nz(t.HandshakeAge), nz(t.Rx), nz(t.Tx))
			}
			fmt.Fprintln(stdout, line)
		}
		return 0

	case "up", "down":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "%s: vpn: %s needs a tunnel name\n", progName, args[0])
			return 1
		}
		var err error
		if args[0] == "up" {
			err = vpn.Up(ctx, args[1])
		} else {
			err = vpn.Down(ctx, args[1])
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s: vpn: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s %s\n", args[1], map[string]string{"up": "up", "down": "down"}[args[0]])
		return 0

	case "-h", "--help":
		fmt.Fprint(stdout, vpnUsage)
		return 0

	default:
		fmt.Fprintf(stderr, "%s: vpn: unknown verb %q\n", progName, args[0])
		fmt.Fprint(stderr, vpnUsage)
		return 1
	}
}

func nz(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
