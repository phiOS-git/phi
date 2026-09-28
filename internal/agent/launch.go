package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Building the argv phi execs to run pi under phi-agent-contain (binding
// contract §5, §6). This is the one place that knows the shape of that
// command line; `phi agent code`/`tui`/`ask`/`inline` (code.go, ask.go,
// inline.go) all go through BuildLaunch rather than assembling it themselves.

// LaunchMode selects how pi is invoked inside the containment (§6).
type LaunchMode int

const (
	ModeTUI   LaunchMode = iota // interactive: `phi agent code`, `phi agent tui`
	ModeRPC                     // `pi --mode rpc`: the future `phi agent serve`
	ModePrint                   // `pi -p --no-session`: `phi agent ask`, `phi agent inline`
)

// LaunchSpec is everything BuildLaunch needs to resolve one launch.
type LaunchSpec struct {
	Profile Profile
	Project string // "" = none; must already exist when set

	Workdir string // coding only: a host dir, already validated (ValidateCodeDir)

	SessionID  string // a NEW session: passed as pi's --session-id
	ResumeFile string // a host .jsonl to resume: passed as --session /home/agent/sessions/<basename>

	Mode      LaunchMode
	ExtraArgs []string // appended to pi's argv, before any trailing "-- PROMPT"
	Prompt    string   // ModePrint with a question: appended as `-- PROMPT` (agent ask; inline writes its prompt to stdin instead)
}

// BuildLaunch resolves spec into the full argv for exec: the
// phi-agent-contain launcher (found on PATH, absolute), its containment
// flags, then "pi" and pi's own flags (§5, §6). argv[0] == argv[len-...] is
// always the launcher's absolute path, so a caller can exec.Command(argv[0],
// argv[1:]...) or syscall.Exec(argv[0], argv, env) directly.
func BuildLaunch(spec LaunchSpec) ([]string, error) {
	if _, err := ParseProfile(string(spec.Profile)); err != nil {
		return nil, fmt.Errorf("BuildLaunch: %w", err)
	}
	if spec.SessionID != "" && spec.ResumeFile != "" {
		return nil, errors.New("BuildLaunch: SessionID and ResumeFile are mutually exclusive")
	}
	if spec.Profile == Coding {
		if spec.Workdir == "" {
			return nil, errors.New("BuildLaunch: coding profile needs Workdir")
		}
	} else if spec.Workdir != "" {
		return nil, fmt.Errorf("BuildLaunch: Workdir is coding-only, got profile %q", spec.Profile)
	}
	if spec.Profile == Inline && spec.Project != "" {
		return nil, errors.New("BuildLaunch: inline mounts no project")
	}

	launcher, err := exec.LookPath(containLauncher)
	if err != nil {
		return nil, fmt.Errorf("%s not found on PATH (phios-dotfiles, ~/.local/bin)", containLauncher)
	}
	if abs, aerr := filepath.Abs(launcher); aerr == nil {
		launcher = abs
	}

	m, err := OpenModel()
	if err != nil {
		return nil, err
	}
	var meta ProjectMeta
	if spec.Project != "" {
		if !m.HasProject(spec.Project) {
			return nil, fmt.Errorf("no such project: %q", spec.Project)
		}
		meta, err = m.LoadProjectMeta(spec.Project)
		if err != nil {
			return nil, err
		}
	}
	folders := meta.ResolveFolders(HostShortName())

	argv := []string{launcher, string(spec.Profile)}

	if spec.Project != "" {
		argv = append(argv, "--project", spec.Project)
	}
	for _, f := range folders {
		argv = append(argv, "--folder", fmt.Sprintf("%s:%s:%s", f.Mode, f.Name, f.Path))
	}
	if spec.Profile == Coding {
		argv = append(argv, "--workdir", spec.Workdir)
	}
	if spec.Profile != Inline {
		sessDir, err := m.SessionsDir(spec.Project)
		if err != nil {
			return nil, err
		}
		argv = append(argv, "--sessions", sessDir)
	}

	argv = append(argv, "--", "pi")

	switch spec.Mode {
	case ModeRPC:
		argv = append(argv, "--mode", "rpc")
	case ModePrint:
		argv = append(argv, "-p", "--no-session")
	}

	switch spec.Profile {
	case General, Academic:
		argv = append(argv, "--no-context-files")
	case Inline:
		argv = append(argv, "--no-tools", "--no-context-files", "--no-extensions", "--no-skills", "--no-prompt-templates")
	}

	if spec.SessionID != "" {
		argv = append(argv, "--session-id", spec.SessionID)
	} else if spec.ResumeFile != "" {
		argv = append(argv, "--session", "/home/agent/sessions/"+filepath.Base(spec.ResumeFile))
	}

	if spec.Profile != Inline {
		for _, p := range appendSystemPromptPaths(m, spec.Profile, spec.Project) {
			argv = append(argv, "--append-system-prompt", p)
		}
		argv = append(argv, "--append-system-prompt", sessionContext(spec, folders))
	}

	argv = append(argv, spec.ExtraArgs...)

	if spec.Prompt != "" {
		argv = append(argv, "--", spec.Prompt)
	}

	return argv, nil
}

// appendSystemPromptPaths returns the CONTAINER-side paths (§3) of every
// memory/instructions file that exists and is non-empty on the HOST (§2),
// for `--append-system-prompt` (§6: "all profiles except inline" — already
// guarded by the caller). Profile-level memory only applies to the profiles
// that have one (MemoryProfiles: general, academic, coding); project-level
// files only when a project is set.
func appendSystemPromptPaths(m *Model, profile Profile, project string) []string {
	var out []string
	add := func(hostPath, containerPath string) {
		if fi, err := os.Stat(hostPath); err == nil && !fi.IsDir() && fi.Size() > 0 {
			out = append(out, containerPath)
		}
	}
	add(filepath.Join(m.root, "memoria.md"), "/home/agent/memory/system/memoria.md")
	if isMemoryProfile(profile) {
		add(filepath.Join(m.profileDir(profile), "memoria.md"), "/home/agent/memory/profile/memoria.md")
	}
	if project != "" {
		add(m.projectInstructionsPath(project), "/home/agent/project/instructions.md")
		add(filepath.Join(m.projectDir(project), "memoria.md"), "/home/agent/project/memoria.md")
	}
	return out
}

// sessionContext builds the phi-generated "phiOS session context" literal
// text (§6): profile, project, the mounted folders and their modes, where
// outputs go, and — general/academic only — how to propose a memory fact;
// coding gets the commit/network note instead. Never called for inline
// (§3: inline mounts no memory, no project, no sessions).
func sessionContext(spec LaunchSpec, folders []ResolvedFolder) string {
	var b strings.Builder
	b.WriteString("# phiOS session context\n\n")
	fmt.Fprintf(&b, "- Profile: %s\n", spec.Profile)
	if spec.Project != "" {
		fmt.Fprintf(&b, "- Project: %s\n", spec.Project)
	} else {
		b.WriteString("- Project: none\n")
	}
	if len(folders) == 0 {
		b.WriteString("- Folders: none\n")
	} else {
		b.WriteString("- Folders:\n")
		for _, f := range folders {
			fmt.Fprintf(&b, "  - %s (%s) at /home/agent/folders/%s/\n", f.Name, f.Mode, f.Name)
		}
	}
	if spec.Project != "" {
		b.WriteString("- Outputs: write results to /home/agent/project/output/\n")
	}
	switch spec.Profile {
	case General, Academic:
		b.WriteString("- To propose a memory fact: write a new file named YYYYMMDD-HHMMSS-<slug>.md " +
			"into the proposte/ of the right level (/home/agent/memory/system/proposte/, " +
			"/home/agent/memory/profile/proposte/, /home/agent/project/proposte/). " +
			"Never edit memoria.md — it is read-only.\n")
	case Coding:
		b.WriteString("- You may commit but cannot push, and have no network access except the proxy whitelist.\n")
	}
	return b.String()
}
