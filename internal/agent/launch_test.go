package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeContain writes an executable "phi-agent-contain" into a fresh temp
// dir and puts that dir first on PATH, so exec.LookPath (and, for the tests
// that actually run it, exec.Command) finds a harmless stand-in rather than
// the real dotfiles launcher. body is the script's shell body after the
// shebang; "" makes a no-op that exits 0 without reading its args.
func fakeContain(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if body == "" {
		body = "exit 0\n"
	}
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(filepath.Join(dir, "phi-agent-contain"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// argvHas reports whether argv contains a "--flag value" pair (or a bare
// "--flag" when value == "") as two consecutive elements.
func argvHas(argv []string, flag, value string) bool {
	for i, a := range argv {
		if a != flag {
			continue
		}
		if value == "" {
			return true
		}
		if i+1 < len(argv) && argv[i+1] == value {
			return true
		}
	}
	return false
}

func TestBuildLaunchGeneralWithProject(t *testing.T) {
	fakeContain(t, "")
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	folderHost := t.TempDir()
	if err := m.NewProject("proj", ProjectMeta{Title: "Proj"}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddProjectFolder("proj", folderHost, "vault", "ro"); err != nil {
		t.Fatal(err)
	}
	meta, err := m.LoadProjectMeta("proj")
	if err != nil {
		t.Fatal(err)
	}
	resolved := meta.ResolveFolders(HostShortName())
	if len(resolved) != 1 {
		t.Fatalf("resolved folders = %v", resolved)
	}
	sessDir, err := m.SessionsDir("proj")
	if err != nil {
		t.Fatal(err)
	}

	// Fresh model: system and profile memoria.md do not exist yet, so they
	// must NOT be appended. Project instructions.md/memoria.md always exist
	// (NewProject/SaveProjectMeta write them with real content), so they
	// must be.
	argv, err := BuildLaunch(LaunchSpec{
		Profile: General, Project: "proj", Mode: ModeTUI, SessionID: "20260101-000000-abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !argvHas(argv, "--project", "proj") {
		t.Errorf("missing --project proj: %v", argv)
	}
	if !argvHas(argv, "--folder", "ro:vault:"+resolved[0].Path) {
		t.Errorf("missing --folder for vault: %v", argv)
	}
	if !argvHas(argv, "--sessions", sessDir) {
		t.Errorf("missing --sessions %s: %v", sessDir, argv)
	}
	if !argvHas(argv, "--no-context-files", "") {
		t.Errorf("general profile needs --no-context-files: %v", argv)
	}
	if !argvHas(argv, "--session-id", "20260101-000000-abcdef") {
		t.Errorf("missing --session-id: %v", argv)
	}
	if !argvHas(argv, "--append-system-prompt", "/home/agent/project/instructions.md") {
		t.Errorf("missing project instructions.md append: %v", argv)
	}
	if !argvHas(argv, "--append-system-prompt", "/home/agent/project/memoria.md") {
		t.Errorf("missing project memoria.md append: %v", argv)
	}
	joined := strings.Join(argv, "\x00")
	if strings.Contains(joined, "/home/agent/memory/system/memoria.md") {
		t.Errorf("system memoria.md appended despite not existing: %v", argv)
	}
	if strings.Contains(joined, "/home/agent/memory/profile/memoria.md") {
		t.Errorf("profile memoria.md appended despite not existing: %v", argv)
	}
	foundContext := false
	for _, a := range argv {
		if strings.Contains(a, "phiOS session context") {
			foundContext = true
		}
	}
	if !foundContext {
		t.Errorf("no phiOS session context text in argv: %v", argv)
	}

	// Writing non-empty system/profile memoria.md makes them appear.
	if err := os.WriteFile(filepath.Join(m.Root(), "memoria.md"), []byte("system fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.profileDir(General), "memoria.md"), []byte("profile fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argv2, err := BuildLaunch(LaunchSpec{Profile: General, Project: "proj", Mode: ModeTUI, SessionID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !argvHas(argv2, "--append-system-prompt", "/home/agent/memory/system/memoria.md") {
		t.Errorf("system memoria.md not appended after being written: %v", argv2)
	}
	if !argvHas(argv2, "--append-system-prompt", "/home/agent/memory/profile/memoria.md") {
		t.Errorf("profile memoria.md not appended after being written: %v", argv2)
	}

	// An existing but EMPTY file must not be appended.
	if err := os.WriteFile(filepath.Join(m.profileDir(General), "memoria.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	argv3, err := BuildLaunch(LaunchSpec{Profile: General, Project: "proj", Mode: ModeTUI, SessionID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv3, "\x00"), "/home/agent/memory/profile/memoria.md") {
		t.Errorf("empty profile memoria.md must not be appended: %v", argv3)
	}
}

func TestBuildLaunchCoding(t *testing.T) {
	fakeContain(t, "")
	_ = testModel(t)
	workdir := t.TempDir()
	argv, err := BuildLaunch(LaunchSpec{Profile: Coding, Workdir: workdir, SessionID: "id", Mode: ModeTUI})
	if err != nil {
		t.Fatal(err)
	}
	if !argvHas(argv, "--workdir", workdir) {
		t.Errorf("missing --workdir %s: %v", workdir, argv)
	}
	if !argvHas(argv, "--sessions", "") {
		t.Errorf("coding needs --sessions: %v", argv)
	}
	if strings.Contains(strings.Join(argv, "\x00"), "--no-context-files") {
		t.Errorf("coding must not get --no-context-files: %v", argv)
	}
}

func TestBuildLaunchWorkdirRequiredForCoding(t *testing.T) {
	fakeContain(t, "")
	_ = testModel(t)
	if _, err := BuildLaunch(LaunchSpec{Profile: Coding, Mode: ModeTUI, SessionID: "id"}); err == nil {
		t.Error("want an error: coding needs Workdir")
	}
}

func TestBuildLaunchWorkdirRejectedForOtherProfiles(t *testing.T) {
	fakeContain(t, "")
	_ = testModel(t)
	if _, err := BuildLaunch(LaunchSpec{Profile: General, Workdir: t.TempDir(), Mode: ModeTUI, SessionID: "id"}); err == nil {
		t.Error("want an error: Workdir is coding-only")
	}
}

func TestBuildLaunchInline(t *testing.T) {
	fakeContain(t, "")
	_ = testModel(t)
	argv, err := BuildLaunch(LaunchSpec{Profile: Inline, Mode: ModePrint})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--no-tools", "--no-context-files", "--no-extensions", "--no-skills", "--no-prompt-templates"} {
		if !argvHas(argv, want, "") {
			t.Errorf("inline argv missing %s: %v", want, argv)
		}
	}
	joined := strings.Join(argv, "\x00")
	if strings.Contains(joined, "--sessions") {
		t.Errorf("inline must not get --sessions: %v", argv)
	}
	if strings.Contains(joined, "--append-system-prompt") {
		t.Errorf("inline must not get any --append-system-prompt: %v", argv)
	}
}

func TestBuildLaunchInlineRejectsProject(t *testing.T) {
	fakeContain(t, "")
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("p", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLaunch(LaunchSpec{Profile: Inline, Project: "p", Mode: ModePrint}); err == nil {
		t.Error("want an error: inline mounts no project")
	}
}

func TestBuildLaunchResumeVsSessionIDExclusive(t *testing.T) {
	fakeContain(t, "")
	_ = testModel(t)
	_, err := BuildLaunch(LaunchSpec{Profile: General, Mode: ModeTUI, SessionID: "a", ResumeFile: "/x/y.jsonl"})
	if err == nil {
		t.Error("want an error: SessionID and ResumeFile are mutually exclusive")
	}
}

func TestBuildLaunchResumeFlag(t *testing.T) {
	fakeContain(t, "")
	_ = testModel(t)
	argv, err := BuildLaunch(LaunchSpec{
		Profile: General, Mode: ModeTUI, ResumeFile: "/data/sessions/20260101-000000-abcdef.jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !argvHas(argv, "--session", "/home/agent/sessions/20260101-000000-abcdef.jsonl") {
		t.Errorf("missing --session flag: %v", argv)
	}
	if strings.Contains(strings.Join(argv, "\x00"), "--session-id") {
		t.Errorf("resume must not also set --session-id: %v", argv)
	}
}

func TestBuildLaunchProjectMustExist(t *testing.T) {
	fakeContain(t, "")
	_ = testModel(t)
	if _, err := BuildLaunch(LaunchSpec{Profile: General, Project: "nope", Mode: ModeTUI, SessionID: "id"}); err == nil {
		t.Error("want an error: no such project")
	}
}

func TestBuildLaunchLauncherNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_ = testModel(t)
	_, err := BuildLaunch(LaunchSpec{Profile: General, Mode: ModeTUI, SessionID: "id"})
	if err == nil || !strings.Contains(err.Error(), "phi-agent-contain") {
		t.Errorf("want a clear error naming phi-agent-contain, got %v", err)
	}
}

func TestBuildLaunchAskShape(t *testing.T) {
	fakeContain(t, "")
	_ = testModel(t)
	argv, err := BuildLaunch(LaunchSpec{Profile: General, Mode: ModePrint, Prompt: "what is 2+2"})
	if err != nil {
		t.Fatal(err)
	}
	if !argvHas(argv, "-p", "") || !argvHas(argv, "--no-session", "") {
		t.Errorf("ask needs -p --no-session: %v", argv)
	}
	if argv[len(argv)-2] != "--" || argv[len(argv)-1] != "what is 2+2" {
		t.Errorf("prompt must be the trailing `-- PROMPT`: %v", argv)
	}
}
