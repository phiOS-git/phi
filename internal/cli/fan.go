package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"phi/internal/fan"
	"phi/internal/view"
)

const fanUsage = `usage: phi fan <verb>

Verbs:
  status [--json]
         every PWM-controllable fan channel found under
         /sys/class/hwmon, its current duty cycle and enable mode
  list           the four profiles: auto, silent, default, heavy
  set PROFILE    apply PROFILE to every discovered channel — needs
                 profiles/*/system/etc/sudoers.d/49-phi-fan installed

"auto" hands each channel back to its own automatic mode (hwmon ABI value
2 — a real automatic mode, though not necessarily the exact one active
before phi ever touched it). silent/default/heavy set a fixed manual duty
cycle (~25%/50%/85%). Requires a real hwmon PWM interface — most laptops
have none; "status" reports plainly when nothing was found.
`

func runFan(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, fanUsage)
		return 1
	}

	switch args[0] {
	case "status":
		asJSON := false
		for _, a := range args[1:] {
			if a == "--json" {
				asJSON = true
			}
		}
		st, err := fan.GetStatus()
		if err != nil {
			fmt.Fprintf(stderr, "%s: fan: %v\n", progName, err)
			return 1
		}
		if asJSON {
			b, _ := json.Marshal(st)
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprint(stdout, view.FanStatus(st))
		return 0

	case "list":
		fmt.Fprint(stdout, view.FanProfiles())
		return 0

	case "set":
		if len(args) < 2 {
			fmt.Fprintf(stderr, "%s: fan: set requires a profile (%v)\n", progName, fan.Profiles)
			return 1
		}
		if err := fan.Set(context.Background(), args[1]); err != nil {
			fmt.Fprintf(stderr, "%s: fan: %v\n", progName, err)
			return 1
		}
		return 0

	case "-h", "--help":
		fmt.Fprint(stdout, fanUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: fan: unknown verb %q\n", progName, args[0])
		fmt.Fprint(stderr, fanUsage)
		return 1
	}
}
