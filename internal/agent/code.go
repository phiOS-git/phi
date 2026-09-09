package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// `phi agent code DIR [-- ARGS...]`, phios-agente-delta.md D-03 / §3.3.
// Replaces the fixed PHI_AGENT_CODE_ROOT: the chosen DIR is the ONLY working
// directory mounted read-write for this A2 session. The blocklist
// (blocklist.go) is a selector guard-rail; the real boundary is
// phi-agent-contain's from-empty mount namespace.
//
// This records a session metadata file (session.go) so the shell panel can
// list and manage the session without ever reaching A2's server (ADR 084).

// containLauncher is the contained launch path, shipped in phios-dotfiles.
const containLauncher = "phi-agent-contain"

// CodeConfig configures a coding-session launch.
type CodeConfig struct {
	Dir        string   // the working directory (validated, mounted rw at /home/agent/work)
	EngineArgs []string // extra args after `-- ` (e.g. resume flags); empty = opencode default TUI
	WindowAddr string   // Hyprland window address override; auto-detected from the ancestry when empty and not Detach
	Detach     bool     // panel use: record + spawn, do not replace this process
}

// RunCode validates DIR, records the session, and hands off to the contained
// A2 engine. On a normal terminal invocation it execs (replaces the process);
// with Detach it spawns and returns the session id.
func RunCode(cfg CodeConfig) (string, error) {
	abs, err := ValidateCodeDir(cfg.Dir)
	if err != nil {
		return "", fmt.Errorf("refusing to open %q: %w", cfg.Dir, err)
	}

	launcher, err := exec.LookPath(containLauncher)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH (phios-dotfiles, ~/.local/bin)", containLauncher)
	}

	id := NewSessionID()
	windowAddr := cfg.WindowAddr
	if windowAddr == "" && !cfg.Detach {
		// A terminal launch: record the terminal window so the panel's
		// "Focus terminal" action works. Detached spawns have no window.
		windowAddr = selfTerminalWindowAddr()
	}
	rec := SessionRecord{
		ID:         id,
		Dir:        abs,
		PID:        os.Getpid(),
		WindowAddr: windowAddr,
	}
	if err := RecordSessionStart(rec); err != nil {
		return "", fmt.Errorf("recording session: %w", err)
	}

	argv := []string{launcher, "a2", "--workdir", abs, "--"}
	if len(cfg.EngineArgs) > 0 {
		argv = append(argv, cfg.EngineArgs...)
	} else {
		argv = append(argv, "opencode")
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
	if err := syscall.Exec(launcher, argv, env); err != nil {
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

var errNoWorkdir = errors.New("phi agent code: needs a directory")
