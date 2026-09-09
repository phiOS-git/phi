package cli

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"phi/internal/agent"
)

const agentUsage = `usage: phi agent <verb> [arguments]

Verbs:
  broker [--instance a1|a2] [--check]
                    run the provider-credential broker (phios-agente.md §6.2):
                    a loopback service, outside the containment, that holds the
                    key and streams the provider response back unbuffered.
                    --check validates config and key without serving.

The engine (opencode) and the containment (phi-agent-contain, in
phios-dotfiles) are not phi's: phi only talks to opencode over its
documented loopback HTTP API. mcp, ask and project land at S-73/S-74.
`

func runAgent(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, agentUsage)
		return 1
	}
	switch args[0] {
	case "broker":
		return runAgentBroker(args[1:], stdout, stderr)
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
		// Fail-closed: a broker that cannot load its key or config does not
		// start (§6.2). The unit then stays failed and no agent works.
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
