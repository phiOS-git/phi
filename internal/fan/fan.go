// Package fan controls PWM-capable fan channels through the Linux hwmon
// sysfs ABI (Requested: "the stats overlay's fan-profile buttons ...
// have no real backend"). A live check on zotac (2026-09-15) found a real
// interface the user's own `sensors-detect` run had missed: hwmon6 is
// `nct6798` (an ASUS ROG STRIX B550-I's Super I/O chip), exposing pwm1,
// pwm2 and pwm5 alongside their pwmN_enable siblings — the standard,
// driver-agnostic control surface documented in the kernel's own
// Documentation/hwmon/sysfs-interface.rst, not a vendor tool. `lm_sensors`
// (extra/T0, already installed) and the in-kernel nct6775 driver family
// are both official — no rule-2 violation.
//
// Discovery is by file presence (a pwmN + pwmN_enable pair under any
// /sys/class/hwmon/hwmonN), never a hardcoded chip name. This avoids
// host-specific branching, so the same code works on any machine that
// exposes a pwm channel.
//
// Profiles: silent/default/heavy write pwmN_enable=1 (the ABI's universal
// "manual" value) then a fixed 0-255 duty-cycle byte to pwmN — the ABI
// units, not a chip-specific curve. auto writes pwmN_enable=2 — the ABI
// guarantees any value >=2 is SOME automatic mode, and 2 is the first and
// (for the nct6775 family specifically, Thermal Cruise) a genuinely
// automatic one; this is a deliberate simplification, NOT a restore of
// whichever specific automatic mode was active before phi ever touched
// it — documented here rather than hidden, since a different board could
// reasonably have shipped in a different automatic mode number.
//
// Every write is privileged (root owns these sysfs files) and goes
// through `sudo -n tee`, the exact shape internal/firewall's own
// sudoStdin already uses, gated by profiles/*/system/etc/sudoers.d/
// 49-phi-fan (/etc material this repo ships and never applies, same
// convention as 49-phi-vpn/49-phi-firewall).
//
// UNTESTED against a real write: this package was developed on the real
// zotac machine (confirmed by hostname and hwmon contents), but every
// write path was deliberately never exercised from here — the workspace
// rules this project runs under forbid touching the live machine (no
// `sudo`, no `/etc` writes) regardless of which repository the code
// changing it lives in. Discovery/read paths were exercised for real
// (see the man page's own worked example); Set() is reasoned correct
// against the documented ABI, not run.
package fan

import (
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

// hwmonRoot is a var, not a const, so fan_test.go can point discovery at a
// temporary directory built to look like /sys/class/hwmon instead of the
// real one.
var hwmonRoot = "/sys/class/hwmon"

// Profiles is the closed set the Stats overlay's four buttons offer.
var Profiles = []string{"auto", "silent", "default", "heavy"}

// dutyFor maps a manual profile to a 0-255 PWM duty-cycle byte — the
// hwmon ABI's own unit, not a chip-specific value.
var dutyFor = map[string]int{
	"silent":  64,  // ~25%
	"default": 128, // ~50%
	"heavy":   217, // ~85%
}

// Channel is one controllable PWM output found under /sys/class/hwmon.
type Channel struct {
	Chip        string // the hwmon chip's own `name` file, e.g. "nct6798"
	PWMPath     string // .../hwmonN/pwmM
	EnablePath  string // .../hwmonN/pwmM_enable
	DutyPercent int    // current pwmM value, rounded to 0-100
	Enable      int    // current pwmM_enable value (ABI: 0=full speed forced, 1=manual, >=2=some automatic mode)
}

// Status is `phi fan status`'s result.
type Status struct {
	Available bool
	Channels  []Channel
}

// Discover finds every PWM-controllable channel under /sys/class/hwmon.
// Read-only and unprivileged — every file it reads here is world-readable
// on a stock Arch kernel (confirmed: this package's own discovery has been
// run for real, on zotac, read-only).
func Discover() ([]Channel, error) {
	entries, err := os.ReadDir(hwmonRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var channels []Channel
	for _, e := range entries {
		dir := filepath.Join(hwmonRoot, e.Name())
		chip := strings.TrimSpace(readFileOrEmpty(filepath.Join(dir, "name")))

		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			// Matches "pwm1", "pwm12", ... but not "pwm1_enable",
			// "pwm1_auto_point1_pwm", etc: the suffix after "pwm" must be
			// all-digit, nothing else.
			name := f.Name()
			if !strings.HasPrefix(name, "pwm") {
				continue
			}
			suffix := name[len("pwm"):]
			if suffix == "" {
				continue
			}
			if _, err := strconv.Atoi(suffix); err != nil {
				continue
			}

			pwmPath := filepath.Join(dir, name)
			enablePath := pwmPath + "_enable"
			if _, err := os.Stat(enablePath); err != nil {
				continue // a pwmN with no pwmN_enable sibling is not controllable the way this package models it
			}

			duty, _ := readInt(pwmPath)
			enable, _ := readInt(enablePath)
			channels = append(channels, Channel{
				Chip:        chip,
				PWMPath:     pwmPath,
				EnablePath:  enablePath,
				DutyPercent: duty * 100 / 255,
				Enable:      enable,
			})
		}
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].PWMPath < channels[j].PWMPath })
	return channels, nil
}

// GetStatus is `phi fan status`'s data.
func GetStatus() (Status, error) {
	channels, err := Discover()
	if err != nil {
		return Status{}, err
	}
	return Status{Available: len(channels) > 0, Channels: channels}, nil
}

// Set applies profile to every discovered channel. See the package
// header for exactly what "auto" and the three manual profiles write and
// why.
func Set(ctx context.Context, profile string) error {
	if profile != "auto" {
		if _, ok := dutyFor[profile]; !ok {
			return fmt.Errorf("unknown fan profile %q (want: %s)", profile, strings.Join(Profiles, ", "))
		}
	}

	channels, err := Discover()
	if err != nil {
		return err
	}
	if len(channels) == 0 {
		return fmt.Errorf("no PWM-controllable fan channel found under %s", hwmonRoot)
	}

	for _, c := range channels {
		if profile == "auto" {
			if err := sudoTee(ctx, c.EnablePath, "2"); err != nil {
				return fmt.Errorf("%s (is profiles/desktop/system/etc/sudoers.d/49-phi-fan installed?)", err)
			}
			continue
		}
		if err := sudoTee(ctx, c.EnablePath, "1"); err != nil {
			return fmt.Errorf("%s (is profiles/desktop/system/etc/sudoers.d/49-phi-fan installed?)", err)
		}
		if err := sudoTee(ctx, c.PWMPath, strconv.Itoa(dutyFor[profile])); err != nil {
			return err
		}
	}
	return nil
}

func readFileOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func readInt(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// sudoTee runs `sudo -n tee <path>` feeding value on stdin — the exact
// shape internal/firewall's own sudoStdin already uses for its two
// privileged writes.
func sudoTee(ctx context.Context, path, value string) error {
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sudo", "-n", "tee", path)
	cmd.Stdin = strings.NewReader(value)
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
