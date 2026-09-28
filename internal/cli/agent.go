package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"phi/internal/agent"
	"phi/internal/view"
)

const agentUsage = `usage: phi agent <verb> [arguments]

Verbs:
  broker [--instance a1|a2] [--check]
                    run the provider-credential broker.
  init              create the data root skeleton (binding contract §2) and
                    migrate ~/.local/share/phi-agent/a1/ (legacy) into it,
                    non-destructively. Idempotent.
  project           projects and their structured metadata (project.json):
                    list [--json] | show NAME [--json]
                    | new NAME [--title T] [--description D] [--profile P]
                      [--instruction-add TEXT]... [--folder MODE:PATH]...
                    | set NAME [same flags] [--instruction-remove TEXT]...
                    | folder add NAME PATH [--mode ro|rw] [--as FOLDER]
                    | folder remove NAME FOLDER
                    | folder mode NAME FOLDER ro|rw
                    | delete NAME [--yes]
  memory            review memory proposals at a level:
                    list | list-all | show FILE | accept FILE | reject FILE
                    [--level system|profile|project] [--profile NAME]
                    [--project NAME] [--json]
  chat              pi session transcripts:
                    list [--project NAME | --unfiled] [--json]
                    | show ID [--json] | pin ID | unpin ID | title ID TEXT
                    | delete ID [--yes]
  search QUERY [--project NAME] [--json]
                    search phi-owned text: transcripts, memory, project
                    instructions, and text attachments.
  session           terminal TUI sessions (phi agent code/tui), from
                    phi-owned metadata:
                    list [--json] | show ID
  code [DIR] [--project NAME] [--detach] [--window-addr ADDR] [-- PI_ARGS...]
                    open the coding profile in DIR (default: the current
                    directory; the only rw mount for the session), guarded
                    by the blocklist. Records session metadata.
  tui [--profile general|academic] [--project NAME] [--resume ID]
                    the chat profiles' interactive pi session, in this
                    terminal.
  ask [--profile general|academic] [--project NAME] QUESTION...
                    one print-mode question through pi. No session kept.
  inline            stdin {"instruction","text","filetype"} -> stdout: the
                    replacement text, and nothing else. For editor use.
  serve [--listen 127.0.0.1:4199]
                    the chat profiles' HTTP+SSE API (binding contract §8):
                    one pi --mode rpc child per live session. Loopback only.

phi never assumes pi's on-disk format beyond the documented session JSONL it
reads for transcripts; it never queries pi's runtime state directly.
`

func runAgent(args []string, stdout, stderr io.Writer, styled bool) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, agentUsage)
		return 1
	}
	switch args[0] {
	case "broker":
		return runAgentBroker(args[1:], stdout, stderr)
	case "init":
		return runAgentInit(args[1:], stdout, stderr)
	case "project":
		return runAgentProject(args[1:], stdout, stderr, styled)
	case "memory":
		return runAgentMemory(args[1:], stdout, stderr, styled)
	case "chat":
		return runAgentChat(args[1:], stdout, stderr, styled)
	case "search":
		return runAgentSearch(args[1:], stdout, stderr, styled)
	case "session":
		return runAgentSession(args[1:], stdout, stderr, styled)
	case "code":
		return runAgentCode(args[1:], stdout, stderr)
	case "tui":
		return runAgentTUI(args[1:], stdout, stderr)
	case "ask":
		return runAgentAsk(args[1:], stdout, stderr)
	case "inline":
		return runAgentInline(args[1:], stdout, stderr)
	case "serve":
		return runAgentServe(args[1:], stdout, stderr)
	case "-h", "--help":
		fmt.Fprint(stdout, agentUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: agent: unknown verb %q\n", progName, args[0])
		fmt.Fprint(stderr, agentUsage)
		return 1
	}
}

// --- broker / ask / code (unchanged behaviour) ------------------------------

func runAgentBroker(args []string, stdout, stderr io.Writer) int {
	instName := "a1"
	check := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--instance":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: agent broker: --instance needs a name\n", progName)
				return 1
			}
			instName = args[i+1]
			i++
		case "--check":
			check = true
		case "-h", "--help":
			fmt.Fprint(stdout, agentUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "%s: agent broker: unexpected argument %q\n", progName, args[i])
			return 1
		}
	}
	inst, err := agent.ParseInstance(instName)
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent broker: %v\n", progName, err)
		return 1
	}
	b, err := agent.LoadBroker(inst)
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent broker: %v\n", progName, err)
		return 1
	}
	if check {
		fmt.Fprintln(stdout, b.Summary())
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stderr, "%s: agent broker: %s\n", progName, b.Summary())
	if err := b.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(stderr, "%s: agent broker: %v\n", progName, err)
		return 1
	}
	return 0
}

func runAgentAsk(args []string, stdout, stderr io.Writer) int {
	profileName, project := "general", ""
	var promptParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--profile":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: agent ask: --profile needs a name\n", progName)
				return 1
			}
			profileName = args[i+1]
			i++
		case "--project":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: agent ask: --project needs a name\n", progName)
				return 1
			}
			project = args[i+1]
			i++
		case "-h", "--help":
			fmt.Fprint(stdout, agentUsage)
			return 0
		default:
			promptParts = append(promptParts, args[i])
		}
	}
	profile, err := agent.ParseProfile(profileName)
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent ask: %v\n", progName, err)
		return 1
	}
	if profile != agent.General && profile != agent.Academic {
		fmt.Fprintf(stderr, "%s: agent ask: profile %q not supported (want general or academic)\n", progName, profile)
		return 1
	}
	question := strings.TrimSpace(strings.Join(promptParts, " "))
	if question == "" {
		fmt.Fprintf(stderr, "%s: agent ask: needs a question\n", progName)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := agent.Ask(ctx, agent.AskConfig{Profile: profile, Project: project}, question, stdout); err != nil {
		fmt.Fprintf(stderr, "%s: agent ask: %v\n", progName, err)
		return 1
	}
	return 0
}

func runAgentCode(args []string, stdout, stderr io.Writer) int {
	var dir, project string
	var extraArgs []string
	windowAddr := os.Getenv("PHI_AGENT_WINDOW_ADDR")
	detach := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--":
			extraArgs = args[i+1:]
			i = len(args)
		case args[i] == "--project" && i+1 < len(args):
			project = args[i+1]
			i++
		case args[i] == "--detach":
			detach = true
		case args[i] == "--window-addr" && i+1 < len(args):
			windowAddr = args[i+1]
			i++
		case !strings.HasPrefix(args[i], "-") && dir == "":
			dir = args[i]
		default:
			fmt.Fprintf(stderr, "%s: agent code: unexpected argument %q\n", progName, args[i])
			return 1
		}
	}
	if dir == "" {
		dir = "."
	}
	id, err := agent.RunCode(agent.CodeConfig{
		Dir: dir, Project: project, ExtraArgs: extraArgs, WindowAddr: windowAddr, Detach: detach,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent code: %v\n", progName, err)
		return 1
	}
	if detach {
		fmt.Fprintln(stdout, id)
	}
	return 0
}

func runAgentTUI(args []string, stdout, stderr io.Writer) int {
	profileName, project, resumeID := "general", "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--profile":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: agent tui: --profile needs a name\n", progName)
				return 1
			}
			profileName = args[i+1]
			i++
		case "--project":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: agent tui: --project needs a name\n", progName)
				return 1
			}
			project = args[i+1]
			i++
		case "--resume":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: agent tui: --resume needs an id\n", progName)
				return 1
			}
			resumeID = args[i+1]
			i++
		case "-h", "--help":
			fmt.Fprint(stdout, agentUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "%s: agent tui: unexpected argument %q\n", progName, args[i])
			return 1
		}
	}
	profile, err := agent.ParseProfile(profileName)
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent tui: %v\n", progName, err)
		return 1
	}
	if err := agent.RunTUI(profile, project, resumeID); err != nil {
		fmt.Fprintf(stderr, "%s: agent tui: %v\n", progName, err)
		return 1
	}
	return 0
}

func runAgentInline(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "%s: agent inline: takes no arguments\n", progName)
		return 1
	}
	if err := agent.RunInline(os.Stdin, stdout, stderr); err != nil {
		return 1
	}
	return 0
}

// --- init --------------------------------------------------------------

func runAgentInit(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "%s: agent init: takes no arguments\n", progName)
		return 1
	}
	m, err := agent.OpenModel()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent init: %v\n", progName, err)
		return 1
	}
	report, err := m.Ensure()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent init: %v\n", progName, err)
		return 1
	}
	if len(report) == 0 {
		fmt.Fprintln(stdout, "data model already in place")
	} else {
		for _, line := range report {
			fmt.Fprintln(stdout, line)
		}
	}
	return 0
}

// --- project -----------------------------------------------------------

func runAgentProject(args []string, stdout, stderr io.Writer, styled bool) int {
	m, err := agent.OpenModel()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent project: %v\n", progName, err)
		return 1
	}
	if _, err := m.Ensure(); err != nil {
		fmt.Fprintf(stderr, "%s: agent project: %v\n", progName, err)
		return 1
	}

	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent project: %v\n", progName, e); return 1 }

	switch sub {
	case "list":
		asJSON := false
		for _, a := range args {
			if a == "--json" {
				asJSON = true
			}
		}
		summaries, err := m.ListProjectSummaries()
		if err != nil {
			return fail(err)
		}
		if asJSON {
			chatProfiles := agent.ChatProfiles()
			profileNames := make([]string, len(chatProfiles))
			for i, p := range chatProfiles {
				profileNames[i] = string(p)
			}
			out, _ := json.MarshalIndent(map[string]any{
				"projects": summaries, "profiles": profileNames,
			}, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprint(stdout, view.AgentProjectList(summaries))
		return 0

	case "show":
		if len(args) == 0 {
			return fail(fmt.Errorf("show needs a project name"))
		}
		name := args[0]
		asJSON := false
		for _, a := range args[1:] {
			if a == "--json" {
				asJSON = true
			}
		}
		meta, err := m.LoadProjectMeta(name)
		if err != nil {
			return fail(err)
		}
		host := agent.HostShortName()
		if asJSON {
			type folderOut struct {
				Name  string            `json:"name"`
				Mode  string            `json:"mode"`
				Paths map[string]string `json:"paths"`
				Here  string            `json:"here"`
			}
			folders := make([]folderOut, 0, len(meta.Folders))
			for _, f := range meta.Folders {
				folders = append(folders, folderOut{Name: f.Name, Mode: f.Mode, Paths: f.Paths, Here: f.Paths[host]})
			}
			out, _ := json.MarshalIndent(map[string]any{
				"name": name, "dir": m.ProjectDir(name), "title": meta.Title,
				"description": meta.Description, "instructions": meta.Instructions,
				"default_profile": meta.DefaultProfile, "folders": folders,
			}, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprint(stdout, view.AgentProjectShow(name, meta, m.ProjectDir(name), host))
		return 0

	case "new":
		name, meta, perr := parseProjectFlags(args, agent.ProjectMeta{})
		if perr != nil {
			return fail(perr)
		}
		if name == "" {
			return fail(fmt.Errorf("new needs a project name"))
		}
		if err := m.NewProject(name, meta); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "created project %s\n", name)
		return 0

	case "set":
		if len(args) == 0 {
			return fail(fmt.Errorf("set needs a project name"))
		}
		name := args[0]
		cur, err := m.LoadProjectMeta(name)
		if err != nil {
			return fail(err)
		}
		_, meta, perr := parseProjectFlags(args, cur)
		if perr != nil {
			return fail(perr)
		}
		if err := m.SaveProjectMeta(name, meta); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "updated project %s\n", name)
		return 0

	case "folder":
		if len(args) < 1 {
			return fail(fmt.Errorf("folder <add|remove|mode> ..."))
		}
		op := args[0]
		args = args[1:]
		switch op {
		case "add":
			if len(args) < 2 {
				return fail(fmt.Errorf("folder add NAME PATH [--mode ro|rw] [--as FOLDER]"))
			}
			name, path := args[0], args[1]
			mode, as := "", ""
			for i := 2; i < len(args); i++ {
				switch args[i] {
				case "--mode":
					if i+1 < len(args) {
						mode = args[i+1]
						i++
					}
				case "--as":
					if i+1 < len(args) {
						as = args[i+1]
						i++
					}
				}
			}
			if err := m.AddProjectFolder(name, path, as, mode); err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "added folder to %s\n", name)
			return 0
		case "remove":
			if len(args) != 2 {
				return fail(fmt.Errorf("folder remove NAME FOLDER"))
			}
			if err := m.RemoveProjectFolder(args[0], args[1]); err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "removed folder from %s\n", args[0])
			return 0
		case "mode":
			if len(args) != 3 {
				return fail(fmt.Errorf("folder mode NAME FOLDER ro|rw"))
			}
			if err := m.SetProjectFolderMode(args[0], args[1], args[2]); err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "set folder mode on %s\n", args[0])
			return 0
		default:
			return fail(fmt.Errorf("folder op %q (want add|remove|mode)", op))
		}

	case "delete":
		yes := false
		var name string
		for _, a := range args {
			if a == "--yes" {
				yes = true
				continue
			}
			name = a
		}
		if name == "" {
			return fail(fmt.Errorf("delete needs a project name"))
		}
		if !yes {
			return fail(fmt.Errorf("refusing to delete %q without --yes", name))
		}
		if err := m.DeleteProject(name); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "deleted project %s\n", name)
		return 0

	default:
		return fail(fmt.Errorf("unknown subcommand %q", sub))
	}
}

// parseProjectFlags applies --title/--description/--profile/
// --instruction-add/--instruction-remove/--folder onto a base meta. args[0]
// is the project name (or a flag for `new` when omitted); it is returned
// separately.
func parseProjectFlags(args []string, base agent.ProjectMeta) (name string, meta agent.ProjectMeta, err error) {
	meta = base
	i := 0
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		i = 1
	}
	host := agent.HostShortName()
	for ; i < len(args); i++ {
		next := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch args[i] {
		case "--title":
			if v, ok := next(); ok {
				meta.Title = v
			}
		case "--description", "--desc":
			if v, ok := next(); ok {
				meta.Description = v
			}
		case "--profile":
			if v, ok := next(); ok {
				p, perr := agent.ParseProfile(v)
				if perr != nil {
					return name, meta, perr
				}
				if p == agent.Inline {
					return name, meta, fmt.Errorf("default profile cannot be inline")
				}
				meta.DefaultProfile = p
			}
		case "--instruction-add":
			if v, ok := next(); ok {
				meta.Instructions = append(meta.Instructions, v)
			}
		case "--instruction-remove":
			if v, ok := next(); ok {
				var kept []string
				for _, ins := range meta.Instructions {
					if ins != v {
						kept = append(kept, ins)
					}
				}
				meta.Instructions = kept
			}
		case "--folder":
			if v, ok := next(); ok {
				mode, path, ok2 := strings.Cut(v, ":")
				if !ok2 {
					return name, meta, fmt.Errorf("--folder wants MODE:PATH, got %q", v)
				}
				if mode != "ro" && mode != "rw" {
					return name, meta, fmt.Errorf("--folder mode %q (want ro or rw)", mode)
				}
				abs, aerr := filepath.Abs(path)
				if aerr != nil {
					return name, meta, aerr
				}
				fname := agent.SanitiseFolderName(filepath.Base(abs))
				meta.Folders = append(meta.Folders, agent.Folder{
					Name: fname, Mode: mode, Paths: map[string]string{host: abs},
				})
			}
		default:
			return name, meta, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return name, meta, nil
}

// --- memory (level-aware) ------------------------------------------------

func runAgentMemory(args []string, stdout, stderr io.Writer, styled bool) int {
	m, err := agent.OpenModel()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent memory: %v\n", progName, err)
		return 1
	}
	if _, err := m.Ensure(); err != nil {
		fmt.Fprintf(stderr, "%s: agent memory: %v\n", progName, err)
		return 1
	}
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent memory: %v\n", progName, e); return 1 }

	levelKind, profileName, project, asJSON := "system", "", "", false
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--level":
			if i+1 < len(args) {
				levelKind = args[i+1]
				i++
			}
		case "--profile":
			if i+1 < len(args) {
				profileName = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				project = args[i+1]
				i++
			}
		case "--json":
			asJSON = true
		default:
			rest = append(rest, args[i])
		}
	}
	level, lerr := agent.ParseMemLevel(levelKind, profileName, project)
	if lerr != nil {
		return fail(lerr)
	}

	sub := "list"
	if len(rest) > 0 {
		sub = rest[0]
		rest = rest[1:]
	}
	switch sub {
	case "list":
		props, err := m.Proposals(level)
		if err != nil {
			return fail(err)
		}
		if asJSON {
			out, _ := json.MarshalIndent(props, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprint(stdout, view.AgentMemoryList(level.String(), props, styled))
		return 0
	case "list-all":
		all, err := m.AllProposals()
		if err != nil {
			return fail(err)
		}
		if asJSON || !styled {
			out, _ := json.MarshalIndent(all, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprint(stdout, view.AgentMemoryListAll(all))
		return 0
	case "show":
		if len(rest) != 1 {
			return fail(fmt.Errorf("show needs a proposal file name"))
		}
		text, err := m.ProposalText(level, rest[0])
		if err != nil {
			return fail(err)
		}
		mem, _ := m.MemoryText(level)
		if asJSON {
			out, _ := json.MarshalIndent(map[string]string{
				"level": level.String(), "file": rest[0], "current": mem, "proposal": text,
			}, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprint(stdout, view.AgentMemoryDiff(rest[0], strings.TrimRight(mem, "\n"), strings.TrimRight(text, "\n")))
		return 0
	case "accept":
		if len(rest) != 1 {
			return fail(fmt.Errorf("accept needs a proposal file name"))
		}
		if err := m.AcceptProposal(level, rest[0]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "appended %s to %s memoria.md\n", rest[0], level.String())
		return 0
	case "reject":
		if len(rest) != 1 {
			return fail(fmt.Errorf("reject needs a proposal file name"))
		}
		if err := m.RejectProposal(level, rest[0]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "removed %s\n", rest[0])
		return 0
	default:
		return fail(fmt.Errorf("unknown subcommand %q", sub))
	}
}

// --- chat (pi session transcripts) --------------------------------------

func runAgentChat(args []string, stdout, stderr io.Writer, styled bool) int {
	m, err := agent.OpenModel()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent chat: %v\n", progName, err)
		return 1
	}
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent chat: %v\n", progName, e); return 1 }

	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list":
		project, unfiled, asJSON := "", false, false
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--project":
				if i+1 < len(args) {
					project = args[i+1]
					i++
				}
			case "--unfiled":
				unfiled = true
			case "--json":
				asJSON = true
			}
		}
		metas, err := m.ListTranscripts(project, unfiled)
		if err != nil {
			return fail(err)
		}
		if asJSON {
			out, _ := json.MarshalIndent(metas, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprint(stdout, view.AgentChatList(metas))
		return 0

	case "show":
		if len(args) == 0 {
			return fail(fmt.Errorf("show needs a chat id"))
		}
		id := args[0]
		asJSON := false
		for _, a := range args[1:] {
			if a == "--json" {
				asJSON = true
			}
		}
		meta, messages, err := m.LoadTranscript(id)
		if err != nil {
			return fail(err)
		}
		if asJSON {
			out, _ := json.MarshalIndent(map[string]any{
				"id": meta.ID, "title": meta.Title, "profile": meta.Profile,
				"project": meta.Project, "messages": messages,
			}, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		io.WriteString(stdout, agent.TranscriptMarkdown(meta, messages))
		return 0

	case "pin", "unpin":
		if len(args) != 1 {
			return fail(fmt.Errorf("%s needs a chat id", sub))
		}
		if err := m.SetChatPinned(args[0], sub == "pin"); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "%sned %s\n", sub, args[0])
		return 0

	case "title":
		if len(args) < 2 {
			return fail(fmt.Errorf("title ID TEXT"))
		}
		if err := m.SetChatTitle(args[0], strings.Join(args[1:], " ")); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "retitled %s\n", args[0])
		return 0

	case "delete":
		if len(args) == 0 {
			return fail(fmt.Errorf("delete needs a chat id"))
		}
		id := args[0]
		yes := false
		for _, a := range args[1:] {
			if a == "--yes" {
				yes = true
			}
		}
		if !yes {
			return fail(fmt.Errorf("refusing to delete %q without --yes", id))
		}
		if err := m.DeleteChat(id); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "deleted %s\n", id)
		return 0

	default:
		return fail(fmt.Errorf("unknown subcommand %q", sub))
	}
}

// --- search --------------------------------------------------------------

func runAgentSearch(args []string, stdout, stderr io.Writer, styled bool) int {
	m, err := agent.OpenModel()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent search: %v\n", progName, err)
		return 1
	}
	project, asJSON := "", false
	var terms []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 < len(args) {
				project = args[i+1]
				i++
			}
		case "--json":
			asJSON = true
		default:
			terms = append(terms, args[i])
		}
	}
	query := strings.TrimSpace(strings.Join(terms, " "))
	if query == "" {
		fmt.Fprintf(stderr, "%s: agent search: needs a query\n", progName)
		return 1
	}
	res, err := m.Search(query, project)
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent search: %v\n", progName, err)
		return 1
	}
	if asJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fmt.Fprint(stdout, view.AgentSearch(query, res))
	return 0
}

// --- session ------------------------------------------------------------

func runAgentSession(args []string, stdout, stderr io.Writer, styled bool) int {
	_ = agent.ReconcileActiveSessions()
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list":
		recs, err := agent.ListSessions()
		if err != nil {
			fmt.Fprintf(stderr, "%s: agent session: %v\n", progName, err)
			return 1
		}
		asJSON := false
		for _, a := range args {
			if a == "--json" {
				asJSON = true
			}
		}
		if asJSON {
			out, _ := json.MarshalIndent(recs, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprint(stdout, view.AgentSessionList(recs))
		return 0
	case "show":
		if len(args) != 1 {
			fmt.Fprintf(stderr, "%s: agent session: show needs an id\n", progName)
			return 1
		}
		rec, err := agent.GetSession(args[0])
		if err != nil {
			fmt.Fprintf(stderr, "%s: agent session: %v\n", progName, err)
			return 1
		}
		out, _ := json.MarshalIndent(rec, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	default:
		fmt.Fprintf(stderr, "%s: agent session: unknown subcommand %q\n", progName, sub)
		return 1
	}
}

// runAgentServe runs `phi agent serve` (binding contract §8): the loopback
// HTTP+SSE API, one pi --mode rpc child per live session. It blocks until
// SIGINT/SIGTERM, closing every live session before returning.
func runAgentServe(args []string, stdout, stderr io.Writer) int {
	addr := "127.0.0.1:4199"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--listen":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: agent serve: --listen needs an address\n", progName)
				return 1
			}
			addr = args[i+1]
			i++
		case "-h", "--help":
			fmt.Fprint(stdout, agentUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "%s: agent serve: unexpected argument %q\n", progName, args[i])
			return 1
		}
	}

	m, err := agent.OpenModel()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent serve: %v\n", progName, err)
		return 1
	}
	srv := agent.NewServer(m)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(stderr, "%s: agent serve: listening on %s\n", progName, addr)
	if err := srv.Serve(ctx, agent.LoopbackListener{Addr: addr}); err != nil {
		fmt.Fprintf(stderr, "%s: agent serve: %v\n", progName, err)
		return 1
	}
	return 0
}
