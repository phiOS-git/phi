package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// nonPhysicalBlockDevice prefixes exclude anything under /sys/block that is
// not a physical disk with a SMART attribute table: loopback, RAM-backed,
// zram, optical drives, and the two common virtual-block-device layers —
// device-mapper (dm-N, what a LUKS volume like mini's /srv shows up as) and
// software RAID (mdN). Real hardware review on mini caught dm-0 (the /srv
// crypt mapping) in this sweep before this exclusion existed.
var nonPhysicalBlockDevice = []string{"loop", "ram", "zram", "sr", "dm", "md"}

// smartStatus enumerates real disks from /sys/block — the same sysfs-first
// preference S-04's capability probes established, rather than a heavier
// device-listing tool — then runs smartctl -H against each.
//
// Real hardware review on mini found the first version of this function
// misreporting a plain permission error as a disk failure: it searched the
// *whole* smartctl output for the bare substring "failed", and smartctl's
// own access-error text also contains that word ("Smartctl open device:
// /dev/sda failed: Permission denied" — confirmed verbatim on mini, running
// unprivileged; the same command with sudo printed "SMART overall-health
// self-assessment test result: PASSED", proving the disk itself is fine).
// assessmentVerdict now looks for that one specific line instead of the
// substring anywhere in the buffer, so an unrelated error can no longer be
// mistaken for a real result.
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
	sawPass, sawFail := false, false
	for _, dev := range devices {
		out, avail, err := run(ctx, "smartctl", "-H", "/dev/"+dev)
		if !avail {
			return Check{Name: name, Status: Unknown, Detail: "smartctl not available"}
		}
		verdict, pass, found := assessmentVerdict(out)
		switch {
		case found:
			if pass {
				sawPass = true
			} else {
				sawFail = true
			}
		case err != nil:
			detail := lastNonEmptyLine(out)
			if strings.Contains(strings.ToLower(detail), "permission denied") {
				detail += " (needs root)"
			}
			verdict = "could not read: " + detail
		default:
			verdict = "could not parse smartctl output: " + lastNonEmptyLine(out)
		}
		lines = append(lines, fmt.Sprintf("/dev/%s: %s", dev, verdict))
	}

	// A device that could not be read is visible in its own detail line
	// above, never hidden — but it must not silently promote the check's
	// own status to "ok": that word means at least one drive's health was
	// actually confirmed, not merely "nothing confirmed failed". A real
	// FAILED always wins; short of that, this is ok only once something
	// was actually verified PASSED, and unknown otherwise (real-hardware
	// review on mini: every device unreadable — one denied permission,
	// one an unsupported USB-bridge device smartctl couldn't open — which
	// the first version of this function still reported as "ok").
	status := Unknown
	switch {
	case sawFail:
		status = Problem
	case sawPass:
		status = OK
	}
	return Check{Name: name, Status: status, Detail: strings.Join(lines, "\n")}
}

// assessmentVerdict looks for smartctl's own self-assessment line —
// "SMART overall-health self-assessment test result: PASSED/FAILED" (ATA)
// or "SMART Health Status: OK" (some SAS/NVMe drives) — and reports pass
// only when that specific line is found and says so. found is false for
// any other output, access errors included, which is what keeps an error
// message from ever being read as a health verdict.
func assessmentVerdict(output string) (verdict string, pass bool, found bool) {
	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "self-assessment test result: passed"),
			strings.Contains(lower, "health status: ok"):
			return "PASSED", true, true
		case strings.Contains(lower, "self-assessment test result: failed"):
			return "FAILED", false, true
		}
	}
	return "", false, false
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return strings.TrimSpace(s)
}
