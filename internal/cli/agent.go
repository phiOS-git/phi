package cli

import (
	"context"
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
                    run the provider-credential broker (phios-agente.md §6.2):
                    a loopback service, outside the containment, that holds the
                    key and streams the provider response back unbuffered.
  mcp               run the phi MCP server on stdio (tool 5, §7.1). One
                    read-only verb, phi_context. opencode spawns this.
  init              create the §8.2 data model (two seed personalities, no
                    projects) under the A1 XDG data directory. Idempotent.
  project [list|current|new NAME|use NAME [--no-restart]]
                    personalities and projects are orthogonal axes (§8.1).
                    'use' rewrites the active-project marker and restarts the
                    A1 service so the containment is rebuilt for it (§4.3).
  memory [list|show FILE|accept FILE|reject FILE] [--project NAME]
                    review pending memory proposals. 'accept' appends the
                    LITERAL proposal text to memoria.md and removes it — the
                    client promoting an approved proposal (§8.4), the only
                    path by which memory is ever written.
  ask [--personality NAME] PROMPT
                    one inline question to the already-running A1 service.
                    The session is created, used, and deleted — it never
                    reaches the panel list or memory (§10.2).

The engine (opencode) and the containment (phi-agent-contain, in
phios-dotfiles) are not phi's: phi only talks to opencode over its
documented loopback HTTP API.
`

func runAgent(args []string, stdout, stderr io.Writer) int {
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
		return runAgentProject(args[1:], stdout, stderr)
	case "memory":
		return runAgentMemory(args[1:], stdout, stderr)
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

func runAgentProject(args []string, stdout, stderr io.Writer) int {
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
	switch sub {
	case "list":
		projects, err := m.Projects()
		if err != nil {
			fmt.Fprintf(stderr, "%s: agent project: %v\n", progName, err)
			return 1
		}
		active, _ := m.ActiveProject()
		personalities, _ := m.Personalities()
		fmt.Fprint(stdout, view.AgentProjectList(projects, active, personalities))
		return 0
	case "current":
		active, err := m.ActiveProject()
		if err != nil {
			fmt.Fprintf(stderr, "%s: agent project: %v\n", progName, err)
			return 1
		}
		if active == "" {
			fmt.Fprintln(stderr, "no active project")
			return 1
		}
		fmt.Fprintln(stdout, active)
		return 0
	case "new":
		if len(args) != 1 {
			fmt.Fprintf(stderr, "%s: agent project new: needs a name\n", progName)
			return 1
		}
		if err := m.NewProject(args[0]); err != nil {
			fmt.Fprintf(stderr, "%s: agent project: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "created project %s\n", args[0])
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
			fmt.Fprintf(stderr, "%s: agent project use: needs a name\n", progName)
			return 1
		}
		if err := m.SetActiveProject(name); err != nil {
			fmt.Fprintf(stderr, "%s: agent project: %v\n", progName, err)
			return 1
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
		fmt.Fprintf(stderr, "%s: agent project: unknown subcommand %q\n", progName, sub)
		return 1
	}
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

	err := agent.Ask(ctx, agent.AskConfig{Personality: personality}, prompt, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent ask: %v\n", progName, err)
		return 1
	}
	return 0
}

func runAgentMemory(args []string, stdout, stderr io.Writer) int {
	m, err := agent.OpenModel()
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent memory: %v\n", progName, err)
		return 1
	}
	project := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--project" && i+1 < len(args) {
			project = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	if project == "" {
		project, _ = m.ActiveProject()
	}
	if project == "" {
		fmt.Fprintln(stderr, "no active project; pass --project NAME")
		return 1
	}

	sub := "list"
	if len(rest) > 0 {
		sub = rest[0]
		rest = rest[1:]
	}
	switch sub {
	case "list":
		props, err := m.Proposals(project)
		if err != nil {
			fmt.Fprintf(stderr, "%s: agent memory: %v\n", progName, err)
			return 1
		}
		fmt.Fprint(stdout, view.AgentMemoryList(project, props))
		return 0
	case "show":
		if len(rest) != 1 {
			fmt.Fprintf(stderr, "%s: agent memory show: needs a proposal file name\n", progName)
			return 1
		}
		text, err := m.ProposalText(project, rest[0])
		if err != nil {
			fmt.Fprintf(stderr, "%s: agent memory: %v\n", progName, err)
			return 1
		}
		mem, _ := m.MemoryText(project)
		fmt.Fprint(stdout, view.AgentMemoryDiff(rest[0], strings.TrimRight(mem, "\n"), strings.TrimRight(text, "\n")))
		return 0
	case "accept":
		if len(rest) != 1 {
			fmt.Fprintf(stderr, "%s: agent memory accept: needs a proposal file name\n", progName)
			return 1
		}
		if err := m.AcceptProposal(project, rest[0]); err != nil {
			fmt.Fprintf(stderr, "%s: agent memory: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "appended %s to %s/memoria.md\n", rest[0], project)
		return 0
	case "reject":
		if len(rest) != 1 {
			fmt.Fprintf(stderr, "%s: agent memory reject: needs a proposal file name\n", progName)
			return 1
		}
		if err := m.RejectProposal(project, rest[0]); err != nil {
			fmt.Fprintf(stderr, "%s: agent memory: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "removed %s\n", rest[0])
		return 0
	default:
		fmt.Fprintf(stderr, "%s: agent memory: unknown subcommand %q\n", progName, sub)
		return 1
	}
}
