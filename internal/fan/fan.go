// Package fan controls PWM-capable fan channels via Linux hwmon sysfs.
// Discovery is by file presence (pwmN + pwmN_enable pair under
// /sys/class/hwmon/hwmonN), host-agnostic. Profiles (auto/silent/default/
// heavy) write duty cycles or enable flags. Writes privileged, gated by
// sudoers. Read-only operations tested on real hardware; writes reasoned
// correct against the ABI but never executed.
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

// hwmonRoot is a var so tests can point to a fake directory.
var hwmonRoot = "/sys/class/hwmon"

// Profiles is the set of available fan profiles.
var Profiles = []string{"auto", "silent", "default", "heavy"}

// dutyFor maps manual profiles to 0-255 PWM duty-cycle bytes (hwmon units).
var dutyFor = map[string]int{
	"silent":  64,  // ~25%
	"default": 128, // ~50%
	"heavy":   217, // ~85%
}

// Channel is one controllable PWM output.
type Channel struct {
	Chip        string // the hwmon chip's own `name` file, e.g. "nct6798"
	PWMPath     string // .../hwmonN/pwmM
	EnablePath  string // .../hwmonN/pwmM_enable
	DutyPercent int    // current pwmM value, rounded to 0-100
	Enable      int    // current pwmM_enable value (ABI: 0=full speed forced, 1=manual, >=2=some automatic mode)
}

// Status is the result of `phi fan status`.
type Status struct {
	Available bool
	Channels  []Channel
}

// Discover finds all PWM-controllable channels (read-only, unprivileged).
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
			// Match pwmN (all-digit suffix only, not pwmN_enable or variants).
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
				continue // pwmN must have pwmN_enable sibling
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

// GetStatus returns the current fan status.
func GetStatus() (Status, error) {
	channels, err := Discover()
	if err != nil {
		return Status{}, err
	}
	return Status{Available: len(channels) > 0, Channels: channels}, nil
}

// Set applies a profile to all discovered channels.
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
