// Package update implements `phi update` (S-45, master plan §7.3: "Snapshot
// preventivo, aggiornamento, rigenerazione config, esito"). This is the one
// verb in phi that deliberately does NOT follow the styled/structured
// output contract every other verb keeps (phi/CLAUDE.md): pacman -Syu is a
// real, interactive, privileged system operation — conflict prompts,
// [Y/n] confirmations, a sudo password — so this package connects the
// child process directly to the real terminal's stdin/stdout/stderr
// instead of capturing anything, the one place in this whole codebase that
// does. It is also why this is a command the USER runs themselves, never
// something the settings panel's Updates section triggers (that section
// stays read-only, S-40/S-45's own AGENT contract).
package update

import (
	"fmt"
	"io"
	"os/exec"

	"phi/internal/theme"
	"phi/internal/tokens"
)

// Step is one phase's outcome, printed as it happens (Run's own caller
// writes progress lines, this package returns structured facts).
type Result struct {
	SnapshotTaken bool
	SnapshotError error // non-fatal — recorded, never stops the upgrade
	UpgradeRan    bool
	UpgradeError  error
	ThemeApplied  bool
	ThemeError    error
}

// Run performs the three phases in order, stopping after the upgrade phase
// if it fails (regenerating theme configs against a broken package
// transaction would be actively misleading). stdin/stdout/stderr are
// passed straight through to pacman — the caller must be a real
// interactive terminal, not a redirected/scripted context, or the sudo
// prompt has nowhere to go.
func Run(stdin io.Reader, stdout, stderr io.Writer) Result {
	var r Result

	// Snapshot: best-effort. "root" is this project's own installation
	// procedures' own snapper config name (phios-procedura-base-2.md,
	// razer-procedura-completata.md both set up Btrfs+snapper this way) —
	// not independently re-verified against a live snapper config from
	// here, so a wrong guess degrades to SnapshotError, never blocks the
	// upgrade phase that follows.
	if _, err := exec.LookPath("snapper"); err == nil {
		cmd := exec.Command("snapper", "-c", "root", "create", "-d", "phi update: pre-upgrade snapshot")
		r.SnapshotError = cmd.Run()
		r.SnapshotTaken = r.SnapshotError == nil
	} else {
		r.SnapshotError = fmt.Errorf("snapper not on PATH")
	}

	// Upgrade: sudo pacman -Syu, real passthrough I/O — see this package's
	// own header for why this is the one place in phi that does this.
	upgradeCmd := exec.Command("sudo", "pacman", "-Syu")
	upgradeCmd.Stdin = stdin
	upgradeCmd.Stdout = stdout
	upgradeCmd.Stderr = stderr
	r.UpgradeError = upgradeCmd.Run()
	r.UpgradeRan = r.UpgradeError == nil
	if !r.UpgradeRan {
		return r
	}

	// Regenerate: the pacman hook (phi-packages, S-45) already does this
	// for a targeted package's own upgrade — this is the belt-and-braces
	// pass covering EVERY themed target after a full -Syu, in case the
	// hook's own package list ever misses one.
	root, err := tokens.Root()
	if err != nil {
		r.ThemeError = err
		return r
	}
	if _, err := theme.Set(root, theme.CurrentVariant(), false); err != nil {
		r.ThemeError = err
		return r
	}
	r.ThemeApplied = true
	return r
}
