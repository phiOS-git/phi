package agent

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// `phi agent tui`: an interactive pi session in THIS terminal, for the chat
// profiles only (§1 — general, academic; coding has its own `phi agent
// code`, and inline has no interactive use at all). A new session writes a
// sidecar (chat.go) and a terminal SessionRecord (session.go), exactly like
// `phi agent code`; resuming an existing one reads its sidecar/transcript via
// FindTranscript instead of minting a new id.

func isChatProfile(p Profile) bool {
	for _, cp := range ChatProfiles() {
		if cp == p {
			return true
		}
	}
	return false
}

// RunTUI execs pi, interactively, under the containment. It replaces this
// process on success; it only returns when something fails before that exec.
func RunTUI(profile Profile, project, resumeID string) error {
	if !isChatProfile(profile) {
		return fmt.Errorf("phi agent tui: profile %q has no interactive chat (want general or academic)", profile)
	}

	m, err := OpenModel()
	if err != nil {
		return err
	}

	spec := LaunchSpec{Profile: profile, Mode: ModeTUI}
	var id string

	if resumeID != "" {
		dir, jsonlPath, sc, err := m.FindTranscript(resumeID)
		if err != nil {
			return err
		}
		if jsonlPath == "" {
			return fmt.Errorf("session %q has no transcript yet", resumeID)
		}
		found := m.projectForSessionsDir(dir)
		if project != "" && project != found {
			return fmt.Errorf("session %q belongs to project %q, not %q", resumeID, found, project)
		}
		spec.Project = found
		spec.ResumeFile = jsonlPath
		id = sc.ID
		if id == "" {
			id = resumeID
		}
	} else {
		if project != "" && !m.HasProject(project) {
			return fmt.Errorf("no such project: %q", project)
		}
		spec.Project = project
		id = NewSessionID()
		sessDir, err := m.SessionsDir(project)
		if err != nil {
			return err
		}
		if err := WriteSidecar(sessDir, Sidecar{
			ID: id, Profile: string(profile), Project: project, Created: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("writing session sidecar: %w", err)
		}
		spec.SessionID = id
	}

	if !unitActive("phi-agent-broker@a1.service") {
		fmt.Fprintln(os.Stderr, "warning: phi-agent-broker@a1.service is not active")
	}

	rec := SessionRecord{
		ID:      id,
		Profile: string(profile),
		Project: spec.Project,
		PID:     os.Getpid(),
	}
	if err := RecordSessionStart(rec); err != nil {
		return fmt.Errorf("recording session: %w", err)
	}

	argv, err := BuildLaunch(spec)
	if err != nil {
		_ = RecordSessionEnd(id, "build launch: "+err.Error())
		return err
	}

	env := os.Environ()
	if err := syscall.Exec(argv[0], argv, env); err != nil {
		_ = RecordSessionEnd(id, "exec failed: "+err.Error())
		return err
	}
	return nil // unreachable
}
