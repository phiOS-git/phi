package view

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"phi/internal/fan"
)

// FanStatus renders `phi fan status`.
func FanStatus(s fan.Status) string {
	if !s.Available {
		return "no PWM-controllable fan channel found under /sys/class/hwmon\n"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 2, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "chip\tchannel\tduty\tenable\n")
	for _, c := range s.Channels {
		fmt.Fprintf(tw, "%s\t%s\t%d%%\t%d\n", c.Chip, c.PWMPath, c.DutyPercent, c.Enable)
	}
	tw.Flush()
	return b.String()
}

// FanProfiles renders `phi fan list`.
func FanProfiles() string {
	return strings.Join(fan.Profiles, "\n") + "\n"
}
