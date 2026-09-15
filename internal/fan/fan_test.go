package fan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeHwmon builds a fake /sys/class/hwmon tree under dir: one chip
// directory with a `name` file and the given pwm channels, each as a
// pwmN + pwmN_enable pair. Mirrors the real files this package's Discover
// reads, confirmed against the real installed nct6798 tree on zotac
// (chip name, pwmN/pwmN_enable siblings, plain decimal ASCII content).
func writeHwmon(t *testing.T, dir, chip string, channels map[string][2]string) {
	t.Helper()
	chipDir := filepath.Join(dir, "hwmon0")
	if err := os.MkdirAll(chipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chipDir, "name"), []byte(chip+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for n, vals := range channels {
		if err := os.WriteFile(filepath.Join(chipDir, "pwm"+n), []byte(vals[0]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(chipDir, "pwm"+n+"_enable"), []byte(vals[1]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoverFindsPWMChannels(t *testing.T) {
	dir := t.TempDir()
	writeHwmon(t, dir, "nct6798", map[string][2]string{
		"1": {"255", "5"},
		"2": {"128", "1"},
	})
	old := hwmonRoot
	hwmonRoot = dir
	defer func() { hwmonRoot = old }()

	channels, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 {
		t.Fatalf("got %d channels, want 2: %+v", len(channels), channels)
	}
	if channels[0].Chip != "nct6798" {
		t.Errorf("Chip = %q, want nct6798", channels[0].Chip)
	}
	if channels[0].DutyPercent != 100 {
		t.Errorf("channel 1 DutyPercent = %d, want 100 (255/255)", channels[0].DutyPercent)
	}
	if channels[1].DutyPercent != 50 {
		t.Errorf("channel 2 DutyPercent = %d, want 50 (128/255 rounds to 50)", channels[1].DutyPercent)
	}
}

// TestDiscoverIgnoresRelatedFiles guards the exact bug an over-eager
// "starts with pwm" match would hit: nct6798 alone exposes a dozen
// pwmN_auto_point*_{pwm,temp} files per channel (confirmed against the
// real chip on zotac) that are not themselves controllable channels.
func TestDiscoverIgnoresRelatedFiles(t *testing.T) {
	dir := t.TempDir()
	chipDir := filepath.Join(dir, "hwmon0")
	if err := os.MkdirAll(chipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(chipDir, "name"), []byte("nct6798\n"), 0o644)
	os.WriteFile(filepath.Join(chipDir, "pwm1"), []byte("128\n"), 0o644)
	os.WriteFile(filepath.Join(chipDir, "pwm1_enable"), []byte("1\n"), 0o644)
	// Related files that must NOT be mistaken for their own channel.
	os.WriteFile(filepath.Join(chipDir, "pwm1_auto_point1_pwm"), []byte("0\n"), 0o644)
	os.WriteFile(filepath.Join(chipDir, "pwm1_auto_point1_temp"), []byte("30000\n"), 0o644)
	os.WriteFile(filepath.Join(chipDir, "pwm1_mode"), []byte("1\n"), 0o644)
	// A pwmN with no pwmN_enable sibling is not a controllable channel.
	os.WriteFile(filepath.Join(chipDir, "pwm9"), []byte("0\n"), 0o644)

	old := hwmonRoot
	hwmonRoot = dir
	defer func() { hwmonRoot = old }()

	channels, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 {
		t.Fatalf("got %d channels, want exactly 1 (pwm1): %+v", len(channels), channels)
	}
	if channels[0].PWMPath != filepath.Join(chipDir, "pwm1") {
		t.Errorf("PWMPath = %q, want the pwm1 file", channels[0].PWMPath)
	}
}

func TestDiscoverNoHwmonDirectory(t *testing.T) {
	old := hwmonRoot
	hwmonRoot = filepath.Join(t.TempDir(), "does-not-exist")
	defer func() { hwmonRoot = old }()

	channels, err := Discover()
	if err != nil {
		t.Fatalf("Discover on a missing hwmon root should not error, got: %v", err)
	}
	if channels != nil {
		t.Errorf("channels = %v, want nil", channels)
	}
}

func TestSetRejectsUnknownProfile(t *testing.T) {
	old := hwmonRoot
	hwmonRoot = t.TempDir() // empty — Set should fail on the unknown profile before ever looking here
	defer func() { hwmonRoot = old }()

	if err := Set(context.Background(), "turbo"); err == nil {
		t.Fatal("Set(\"turbo\") should have failed — not one of Profiles")
	}
}
