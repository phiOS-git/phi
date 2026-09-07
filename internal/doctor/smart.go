package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// nonPhysicalBlockDevice prefixes exclude anything under /sys/block that is
// not a physical disk with a SMART attribute table: loopback, RAM-backed,
// zram, and optical drives.
var nonPhysicalBlockDevice = []string{"loop", "ram", "zram", "sr"}

// smartStatus enumerates real disks from /sys/block — the same sysfs-first
// preference S-04's capability probes established, rather than a heavier
// device-listing tool — then runs smartctl -H against each. Parsing is
// text-based rather than smartctl's own exit-code bit field (smartctl(8),
// not reachable from here to confirm the bit layout, the same gap S-05
// flagged for smartd.conf's -m token): "PASSED"/"OK" and "FAILED"/"FAILING"
// are the phrases every SMART implementation's self-assessment line uses.
func smartStatus(ctx context.Context) Check {
	const name = "SMART status"

	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return Check{Name: name, Status: Unknown, Detail: fmt.Sprintf("/sys/block: %v", err)}
	}

	var devices []string
	for _, e := range entries {
		n := e.Name()
		skip := false
		for _, p := range nonPhysicalBlockDevice {
			if strings.HasPrefix(n, p) {
				skip = true
				break
			}
		}
		if !skip {
			devices = append(devices, n)
		}
	}
	if len(devices) == 0 {
		return Check{Name: name, Status: Unknown, Detail: "no physical block devices found under /sys/block"}
	}

	var lines []string
	status := OK
	for _, dev := range devices {
		out, avail, err := run(ctx, "smartctl", "-H", "/dev/"+dev)
		if !avail {
			return Check{Name: name, Status: Unknown, Detail: "smartctl not available"}
		}
		lower := strings.ToLower(out)
		var verdict string
		switch {
		case strings.Contains(lower, "test result: passed"), strings.Contains(lower, "health status: ok"):
			verdict = "PASSED"
		case strings.Contains(lower, "failed"), strings.Contains(lower, "failing"):
			verdict = "FAILED"
			status = Problem
		case err != nil:
			verdict = fmt.Sprintf("could not read (%v)", err)
		default:
			verdict = "could not parse smartctl output"
		}
		lines = append(lines, fmt.Sprintf("/dev/%s: %s", dev, verdict))
	}
	return Check{Name: name, Status: status, Detail: strings.Join(lines, "\n")}
}
