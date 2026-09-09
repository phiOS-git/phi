package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"phi/internal/agent"
	"phi/internal/view"
)

const agentUsage = `usage: phi agent <verb> [arguments]

Verbs:
  broker [--instance a1|a2] [--check]
                    run the provider-credential broker (phios-agente.md §6.2).
  mcp               run the phi MCP server on stdio (tool 5, §7.1).
  init              create the §8.2 data model (two seed personalities, no
                    projects) under the A1 XDG data directory. Idempotent;
                    migrates personalita/<name>.md -> personalita/<name>/prompt.md.
  project           projects and their structured metadata (project.json):
                    list | current | show NAME | new NAME [flags] | set NAME [flags]
                    | use NAME [--no-restart] | folder add|remove NAME PATH
  personality       personalities, editable from the panel (delta D-08):
                    list | show NAME | new NAME [--from-file F] | write NAME --from-file F
                    | rename OLD NEW | delete NAME
  memory            review memory proposals at a level (delta D-01):
                    list | show FILE | accept FILE | reject FILE
                    [--level system|personality|project] [--personality NAME]
                    [--project NAME]
  chat              the client-side transcript mirror (delta D-05):
                    list [--project NAME] | show ID | sync ID --from-file F
                    [--project NAME] [--title T] | pin ID | unpin ID | title ID TEXT
  search QUERY [--project NAME] [--json]
                    search phi-owned markdown (mirrors, archive, memory,
                    instructions). Never queries opencode (ADR 099).
  session           A2 coding sessions, from phi-owned metadata (delta D-07):
                    list [--json] | show ID
  code DIR [-- ARGS...]
                    open the A2 coding agent in DIR (the only rw mount for the
                    session), guarded by the blocklist. Records session metadata.
  ask [--personality NAME] PROMPT
                    one inline question to the running A1 service (§10.2).

phi never assumes opencode's on-disk format; it talks to opencode only over
its documented loopback HTTP API (ADR 098).
`

func runAgent(args []string, stdout, stderr io.Writer, styled bool) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, agentUsage)
		return 1
	}
	switch args[0] {
	case "broker":
		return runAgentBroker(args[1:], stdout, stderr)
	case "mcp":
		return runAgentMCP(args[1:], stdout, stderr)
	case "init":
		return runAgentInit(args[1:], stdout, stderr)
	case "project":
		return runAgentProject(args[1:], stdout, stderr, styled)
	case "personality":
		return runAgentPersonality(args[1:], stdout, stderr, styled)
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
	case "ask":
		return runAgentAsk(args[1:], stdout, stderr)
	case "-h", "--help":
		fmt.Fprint(stdout, agentUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: agent: unknown verb %q\n", progName, args[0])
		fmt.Fprint(stderr, agentUsage)
		return 1
	}
}

// --- broker / mcp / init / ask (unchanged behaviour) -------------------------

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

func runAgentMCP(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "%s: agent mcp: takes no arguments\n", progName)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := agent.RunMCP(ctx, os.Stdin, stdout); err != nil && err != context.Canceled {
		fmt.Fprintf(stderr, "%s: agent mcp: %v\n", progName, err)
		return 1
	}
	return 0
}

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
	created, err := m.Ensure()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent init: %v\n", progName, err)
		return 1
	}
	if len(created) == 0 {
		fmt.Fprintln(stdout, "data model already in place")
	} else {
		for _, c := range created {
			fmt.Fprintf(stdout, "created %s\n", c)
		}
	}
	return 0
}

func runAgentAsk(args []string, stdout, stderr io.Writer) int {
	var personality string
	var promptParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--personality", "--agent":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: agent ask: %s needs a name\n", progName, args[i])
				return 1
			}
			personality = args[i+1]
			i++
		case "-h", "--help":
			fmt.Fprint(stdout, agentUsage)
			return 0
		default:
			promptParts = append(promptParts, args[i])
		}
	}
	prompt := strings.TrimSpace(strings.Join(promptParts, " "))
	if prompt == "" {
		fmt.Fprintf(stderr, "%s: agent ask: needs a prompt\n", progName)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := agent.Ask(ctx, agent.AskConfig{Personality: personality}, prompt, stdout); err != nil {
		fmt.Fprintf(stderr, "%s: agent ask: %v\n", progName, err)
		return 1
	}
	return 0
}

// --- project ---------------------------------------------------------------

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
		projects, err := m.Projects()
		if err != nil {
			return fail(err)
		}
		active, _ := m.ActiveProject()
		personalities, _ := m.Personalities()
		fmt.Fprint(stdout, view.AgentProjectList(projects, active, personalities))
		return 0

	case "current":
		active, err := m.ActiveProject()
		if err != nil {
			return fail(err)
		}
		if active == "" {
			fmt.Fprintln(stderr, "no active project")
			return 1
		}
		fmt.Fprintln(stdout, active)
		return 0

	case "show":
		if len(args) != 1 {
			return fail(fmt.Errorf("show needs a project name"))
		}
		meta, err := m.LoadProjectMeta(args[0])
		if err != nil {
			return fail(err)
		}
		out, _ := json.MarshalIndent(meta, "", "  ")
		fmt.Fprintln(stdout, string(out))
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
		if len(args) < 3 {
			return fail(fmt.Errorf("folder <add|remove> NAME PATH"))
		}
		op, name, path := args[0], args[1], strings.Join(args[2:], " ")
		switch op {
		case "add":
			if err := m.AddProjectFolder(name, path); err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "added folder of interest to %s\n", name)
		case "remove":
			if err := m.RemoveProjectFolder(name, path); err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "removed folder of interest from %s\n", name)
		default:
			return fail(fmt.Errorf("folder op %q (want add|remove)", op))
		}
		return 0

	case "use":
		noRestart := false
		var name string
		for _, a := range args {
			if a == "--no-restart" {
				noRestart = true
				continue
			}
			name = a
		}
		if name == "" {
			return fail(fmt.Errorf("use needs a name"))
		}
		if err := m.SetActiveProject(name); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "active project: %s\n", name)
		if noRestart {
			return 0
		}
		if err := agent.RestartA1(); err != nil {
			fmt.Fprintf(stderr, "%s: agent project: marker written, but %v\n", progName, err)
			return 1
		}
		fmt.Fprintln(stdout, "restarted phi-agent-a1.service")
		return 0

	default:
		return fail(fmt.Errorf("unknown subcommand %q", sub))
	}
}

// parseProjectFlags applies --description / --personality / --instruction-add /
// --instruction-remove / --folder onto a base meta. args[0] is the project
// name (or a flag for `new` when omitted); it is returned separately.
func parseProjectFlags(args []string, base agent.ProjectMeta) (name string, meta agent.ProjectMeta, err error) {
	meta = base
	i := 0
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		i = 1
	}
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
		case "--personality", "--default-personality":
			if v, ok := next(); ok {
				meta.DefaultPersonality = v
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
				meta.Folders = append(meta.Folders, v)
			}
		default:
			return name, meta, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return name, meta, nil
}

// --- personality ----------------------------------------------------------

func runAgentPersonality(args []string, stdout, stderr io.Writer, styled bool) int {
	m, err := agent.OpenModel()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent personality: %v\n", progName, err)
		return 1
	}
	if _, err := m.Ensure(); err != nil {
		fmt.Fprintf(stderr, "%s: agent personality: %v\n", progName, err)
		return 1
	}
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent personality: %v\n", progName, e); return 1 }

	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list":
		ps, err := m.Personalities()
		if err != nil {
			return fail(err)
		}
		for _, p := range ps {
			fmt.Fprintln(stdout, p)
		}
		return 0
	case "show":
		if len(args) != 1 {
			return fail(fmt.Errorf("show needs a name"))
		}
		txt, err := m.PersonalityPrompt(args[0])
		if err != nil {
			return fail(err)
		}
		fmt.Fprint(stdout, txt)
		return 0
	case "new", "write":
		name, fromFile := "", ""
		for i := 0; i < len(args); i++ {
			if args[i] == "--from-file" && i+1 < len(args) {
				fromFile = args[i+1]
				i++
			} else if !strings.HasPrefix(args[i], "-") && name == "" {
				name = args[i]
			}
		}
		if name == "" {
			return fail(fmt.Errorf("%s needs a name", sub))
		}
		if sub == "new" && m.HasPersonality(name) {
			return fail(fmt.Errorf("personality %q already exists", name))
		}
		body, rerr := readInput(fromFile)
		if rerr != nil {
			return fail(rerr)
		}
		if strings.TrimSpace(body) == "" && sub == "new" {
			body = "You are a personality of the phiOS assistant.\n\n(Describe its disposition, scope, and how it should reason.)\n"
		}
		if err := m.WritePersonality(name, body); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "wrote personality %s\n", name)
		return 0
	case "rename":
		if len(args) != 2 {
			return fail(fmt.Errorf("rename OLD NEW"))
		}
		if err := m.RenamePersonality(args[0], args[1]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "renamed %s -> %s\n", args[0], args[1])
		return 0
	case "delete":
		if len(args) != 1 {
			return fail(fmt.Errorf("delete needs a name"))
		}
		if err := m.DeletePersonality(args[0]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "deleted %s\n", args[0])
		return 0
	default:
		return fail(fmt.Errorf("unknown subcommand %q", sub))
	}
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

	levelKind, personality, project := "project", "", ""
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--level":
			if i+1 < len(args) {
				levelKind = args[i+1]
				i++
			}
		case "--personality":
			if i+1 < len(args) {
				personality = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				project = args[i+1]
				i++
			}
		default:
			rest = append(rest, args[i])
		}
	}
	if levelKind == "project" && project == "" {
		project, _ = m.ActiveProject()
	}
	name := project
	if levelKind == "personality" {
		name = personality
	}
	level, lerr := agent.ParseMemLevel(levelKind, name)
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
		fmt.Fprint(stdout, view.AgentMemoryList(level.String(), props, styled))
		return 0
	case "list-all":
		all, err := m.AllProposals()
		if err != nil {
			return fail(err)
		}
		out, _ := json.MarshalIndent(all, "", "  ")
		fmt.Fprintln(stdout, string(out))
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

// --- chat (transcript mirror) ------------------------------------------

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
		project := ""
		for i := 0; i < len(args); i++ {
			if args[i] == "--project" && i+1 < len(args) {
				project = args[i+1]
				i++
			}
		}
		metas, err := m.ListTranscripts(project)
		if err != nil {
			return fail(err)
		}
		out, _ := json.MarshalIndent(metas, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	case "show":
		if len(args) != 1 {
			return fail(fmt.Errorf("show needs a conversation id"))
		}
		_, md, ferr := m.TranscriptByID(args[0])
		if ferr != nil {
			return fail(ferr)
		}
		io.WriteString(stdout, md)
		return 0
	case "sync":
		id, project, title, fromFile := "", "", "", ""
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--project":
				if i+1 < len(args) {
					project = args[i+1]
					i++
				}
			case "--title":
				if i+1 < len(args) {
					title = args[i+1]
					i++
				}
			case "--from-file":
				if i+1 < len(args) {
					fromFile = args[i+1]
					i++
				}
			default:
				if !strings.HasPrefix(args[i], "-") && id == "" {
					id = args[i]
				}
			}
		}
		if id == "" {
			return fail(fmt.Errorf("sync needs a conversation id"))
		}
		body, rerr := readInput(fromFile)
		if rerr != nil {
			return fail(rerr)
		}
		if err := m.WriteTranscript(project, id, title, body); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "mirrored %s\n", id)
		return 0
	case "pin", "unpin":
		if len(args) != 1 {
			return fail(fmt.Errorf("%s needs a conversation id", sub))
		}
		if err := m.SetTranscriptPin(args[0], sub == "pin"); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "%sned %s\n", sub, args[0])
		return 0
	case "title":
		if len(args) < 2 {
			return fail(fmt.Errorf("title ID TEXT"))
		}
		if err := m.SetTranscriptTitle(args[0], strings.Join(args[1:], " ")); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "retitled %s\n", args[0])
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

// --- code -------------------------------------------------------------

func runAgentCode(args []string, stdout, stderr io.Writer) int {
	var dir string
	var engineArgs []string
	windowAddr := os.Getenv("PHI_AGENT_WINDOW_ADDR")
	detach := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--":
			engineArgs = args[i+1:]
			i = len(args)
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
		fmt.Fprintf(stderr, "%s: agent code: needs a directory\n", progName)
		return 1
	}
	id, err := agent.RunCode(agent.CodeConfig{
		Dir: dir, EngineArgs: engineArgs, WindowAddr: windowAddr, Detach: detach,
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

// --- helpers ---------------------------------------------------------

// readInput reads from a file path, or from stdin when path is "" or "-".
func readInput(path string) (string, error) {
	if path == "" || path == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(path)
	return string(b), err
}
