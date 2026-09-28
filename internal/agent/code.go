package agent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// `phi agent code DIR [-- PI_ARGS...]`.
// The chosen DIR is the ONLY working directory mounted read-write for this
// coding session (§1: profile coding, broker a2). The blocklist
// (blocklist.go) is a selector guard-rail; the real boundary is
// phi-agent-contain's from-empty mount namespace.
//
// This records a session metadata file (session.go) so the shell panel can
// list and manage the session without ever reaching pi's runtime state, plus
// a sidecar (chat.go) next to where pi will write its transcript.

// containLauncher is the contained launch path, shipped in phios-dotfiles.
const containLauncher = "phi-agent-contain"

// CodeConfig configures a coding-session launch.
type CodeConfig struct {
	Dir        string   // the working directory (validated, mounted rw at /home/agent/work)
	Project    string   // "" = none; must already exist when set
	ExtraArgs  []string // extra pi args after `-- `
	WindowAddr string   // Hyprland window address override; auto-detected from the ancestry when empty and not Detach
	Detach     bool     // panel use: record + spawn, do not replace this process
}

// RunCode validates DIR, records the session, and hands off to pi under the
// containment. On a normal terminal invocation it execs (replaces the
// process); with Detach it spawns and returns the session id.
func RunCode(cfg CodeConfig) (string, error) {
	abs, err := ValidateCodeDir(cfg.Dir)
	if err != nil {
		return "", fmt.Errorf("refusing to open %q: %w", cfg.Dir, err)
	}

	if err := checkA2Services(); err != nil {
		return "", err
	}

	m, err := OpenModel()
	if err != nil {
		return "", err
	}
	if cfg.Project != "" && !m.HasProject(cfg.Project) {
		return "", fmt.Errorf("no such project: %q", cfg.Project)
	}

	id := NewSessionID()
	sessDir, err := m.SessionsDir(cfg.Project)
	if err != nil {
		return "", err
	}
	if err := WriteSidecar(sessDir, Sidecar{
		ID: id, Profile: string(Coding), Project: cfg.Project, Created: time.Now().UTC(),
	}); err != nil {
		return "", fmt.Errorf("writing session sidecar: %w", err)
	}

	windowAddr := cfg.WindowAddr
	if windowAddr == "" && !cfg.Detach {
		// A terminal launch: record the terminal window so the panel's
		// "Focus terminal" action works. Detached spawns have no window.
		windowAddr = selfTerminalWindowAddr()
	}
	rec := SessionRecord{
		ID:         id,
		Profile:    string(Coding),
		Project:    cfg.Project,
		Dir:        abs,
		PID:        os.Getpid(),
		WindowAddr: windowAddr,
		// TranscriptPath is left empty: pi has not written its .jsonl yet,
		// and its exact name is not known until it does. ListSessions fills
		// it in lazily once the file appears (session.go).
	}
	if err := RecordSessionStart(rec); err != nil {
		return "", fmt.Errorf("recording session: %w", err)
	}

	argv, err := BuildLaunch(LaunchSpec{
		Profile:   Coding,
		Project:   cfg.Project,
		Workdir:   abs,
		SessionID: id,
		Mode:      ModeTUI,
		ExtraArgs: cfg.ExtraArgs,
	})
	if err != nil {
		_ = RecordSessionEnd(id, "build launch: "+err.Error())
		return "", err
	}

	if cfg.Detach {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			_ = RecordSessionEnd(id, "failed to start: "+err.Error())
			return "", err
		}
		_ = UpdateSession(id, func(r *SessionRecord) { r.PID = cmd.Process.Pid })
		go func() {
			err := cmd.Wait()
			note := ""
			if err != nil {
				note = err.Error()
			}
			_ = RecordSessionEnd(id, note)
		}()
		return id, nil
	}

	// Terminal invocation: exec so signals and the TTY pass straight through.
	// The record stays "active" until `phi agent code` is re-run to reconcile
	// it, or ListSessions notices the pid is gone. Best-effort end-marking via
	// a fork is not worth a second process here.
	env := append(os.Environ(), "PHI_AGENT_SESSION_ID="+id)
	if err := syscall.Exec(argv[0], argv, env); err != nil {
		_ = RecordSessionEnd(id, "exec failed: "+err.Error())
		return "", err
	}
	return id, nil // unreachable
}

// ReconcileActiveSessions marks any "active" session whose process is gone as
// ended. Called by `phi agent code` housekeeping and `phi agent session list`.
func ReconcileActiveSessions() error {
	recs, err := ListSessions()
	if err != nil {
		return err
	}
	var firstErr error
	for _, r := range recs {
		if r.Status == "active" && r.PID > 0 && !processAlive(r.PID) {
			if e := RecordSessionEnd(r.ID, "process exited"); e != nil && firstErr == nil {
				firstErr = e
			}
		}
	}
	return firstErr
}

// a2SupportUnits are the systemd user services the coding containment needs
// already running before it starts: phi-agent-contain bind-mounts the net/
// bridge dir as-is and never waits for it, so a coding launch with any
// of these down does not fail closed at the mount — it starts, then fails
// deep inside the container with a bare `socat: No such file or directory`
// connecting to proxy.sock, which reads as a broken feature rather than
// "start these services first." Checking here, before the exec, turns that
// into one clear message naming exactly what to start.
var a2SupportUnits = []string{
	"phi-agent-broker@a2.service",
	"phi-agent-proxy.service",
	"phi-agent-net-bridge.service",
}

// checkA2Services fails closed, before RunCode ever execs into the
// containment, when any of a2SupportUnits is not active.
func checkA2Services() error {
	var down []string
	for _, u := range a2SupportUnits {
		if !unitActive(u) {
			down = append(down, u)
		}
	}
	if len(down) == 0 {
		return nil
	}
	return fmt.Errorf("A2 support service(s) not running: %s — start them first: systemctl --user start %s",
		strings.Join(down, ", "), strings.Join(down, " "))
}
