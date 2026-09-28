package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"

	"phi/internal/agent"
)

// Small `phi agent` verbs added alongside the panel API (binding contract
// §8): usage accounting, UI prefs, a health summary, broker request
// history, project attachments and a plain memoria.md read, plus terminal
// session-record pruning. Each mirrors runAgentChat's shape: parse args,
// call the agent package, print styled or --json.

// --- usage -----------------------------------------------------------------

func runAgentUsage(args []string, stdout, stderr io.Writer, styled bool) int {
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent usage: %v\n", progName, e); return 1 }

	days, asJSON := 30, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--days":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--days needs a number"))
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return fail(fmt.Errorf("bad --days %q", args[i+1]))
			}
			days = n
			i++
		case "--json":
			asJSON = true
		default:
			return fail(fmt.Errorf("unexpected argument %q", args[i]))
		}
	}
	if days < 1 {
		days = 1
	} else if days > 365 {
		days = 365
	}

	m, err := agent.OpenModel()
	if err != nil {
		return fail(err)
	}
	report, err := agent.ComputeUsage(m, days, time.Now())
	if err != nil {
		return fail(err)
	}
	if asJSON {
		out, _ := json.MarshalIndent(report, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fmt.Fprintf(stdout, "today   %6d tokens  $%.2f  %d turns\n", report.Today.Tokens.Total, report.Today.Cost, report.Today.Turns)
	fmt.Fprintf(stdout, "week    %6d tokens  $%.2f  %d turns\n", report.Week.Tokens.Total, report.Week.Cost, report.Week.Turns)
	fmt.Fprintf(stdout, "month   %6d tokens  $%.2f  %d turns\n", report.Month.Tokens.Total, report.Month.Cost, report.Month.Turns)
	return 0
}

// --- prefs -------------------------------------------------------------

func runAgentPrefs(args []string, stdout, stderr io.Writer, styled bool) int {
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent prefs: %v\n", progName, e); return 1 }
	if len(args) == 0 {
		return fail(fmt.Errorf("needs a subcommand (get or set)"))
	}
	sub, rest := args[0], args[1:]

	asJSON := false
	var positional []string
	for _, a := range rest {
		if a == "--json" {
			asJSON = true
		} else {
			positional = append(positional, a)
		}
	}

	switch sub {
	case "get":
		prefs, err := agent.LoadPrefs()
		if err != nil {
			return fail(err)
		}
		if len(positional) == 0 {
			if asJSON {
				out, _ := json.MarshalIndent(prefs, "", "  ")
				fmt.Fprintln(stdout, string(out))
				return 0
			}
			printPrefs(stdout, prefs)
			return 0
		}
		if len(positional) != 1 {
			return fail(fmt.Errorf("get takes at most one key"))
		}
		v, err := prefs.Get(positional[0])
		if err != nil {
			return fail(err)
		}
		if asJSON {
			out, _ := json.MarshalIndent(v, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprintf(stdout, "%v\n", v)
		return 0

	case "set":
		if len(positional) != 2 {
			return fail(fmt.Errorf("set needs KEY VALUE"))
		}
		prefs, err := agent.LoadPrefs()
		if err != nil {
			return fail(err)
		}
		if err := prefs.Set(positional[0], positional[1]); err != nil {
			return fail(err)
		}
		if err := agent.SavePrefs(prefs); err != nil {
			return fail(err)
		}
		if asJSON {
			out, _ := json.MarshalIndent(prefs, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprintf(stdout, "set %s = %s\n", positional[0], positional[1])
		return 0

	default:
		return fail(fmt.Errorf("unknown subcommand %q (want get or set)", sub))
	}
}

// printPrefs is prefs get's plain-text form: every dotted key Prefs.Set/Get
// recognise, one per line.
func printPrefs(stdout io.Writer, p agent.Prefs) {
	fmt.Fprintf(stdout, "defaultProfile: %s\n", p.DefaultProfile)
	for _, prof := range []string{"general", "academic", "coding"} {
		fmt.Fprintf(stdout, "models.%s: %s\n", prof, p.Models[prof])
		fmt.Fprintf(stdout, "thinking.%s: %s\n", prof, p.Thinking[prof])
	}
	fmt.Fprintf(stdout, "idleMinutes: %d\n", p.IdleMinutes)
	fmt.Fprintf(stdout, "dialogTimeoutSeconds: %d\n", p.DialogTimeoutSeconds)
	fmt.Fprintf(stdout, "scheduler.enabled: %v\n", p.Scheduler.Enabled)
	fmt.Fprintf(stdout, "scheduler.dailyCap: %v\n", p.Scheduler.DailyCap)
}

// --- status --------------------------------------------------------------

func runAgentStatus(args []string, stdout, stderr io.Writer, styled bool) int {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}
	st := agent.CollectStatus()
	if asJSON {
		out, _ := json.MarshalIndent(st, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	for _, u := range st.Units {
		fmt.Fprintf(stdout, "%-32s active=%-10s enabled=%s\n", u.Name, u.Active, u.Enabled)
	}
	for _, p := range []string{"general", "academic", "coding"} {
		pm := st.Profiles[p]
		fmt.Fprintf(stdout, "profile %-10s present=%-5v providers=%d\n", p, pm.Present, len(pm.Providers))
	}
	for _, inst := range []string{"a1", "a2"} {
		b := st.Brokers[inst]
		fmt.Fprintf(stdout, "broker %-3s configured=%-5v keyPresent=%-5v requests=%d\n", inst, b.Configured, b.KeyPresent, b.Requests)
	}
	fmt.Fprintf(stdout, "whitelist entries: %d\n", st.WhitelistEntries)
	fmt.Fprintf(stdout, "config root: %s\n", st.ConfigRoot)
	fmt.Fprintf(stdout, "state root: %s\n", st.StateRoot)
	return 0
}

// --- broker-requests -------------------------------------------------------

func runAgentBrokerRequests(args []string, stdout, stderr io.Writer, styled bool) int {
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent broker-requests: %v\n", progName, e); return 1 }

	instName, limit, asJSON := "a1", 50, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--instance":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--instance needs a name"))
			}
			instName = args[i+1]
			i++
		case "--limit":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--limit needs a number"))
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return fail(fmt.Errorf("bad --limit %q", args[i+1]))
			}
			limit = n
			i++
		case "--json":
			asJSON = true
		default:
			return fail(fmt.Errorf("unexpected argument %q", args[i]))
		}
	}
	inst, err := agent.ParseInstance(instName)
	if err != nil {
		return fail(err)
	}
	reqs, err := agent.RecentBrokerRequests(inst, limit)
	if err != nil {
		return fail(err)
	}
	if asJSON {
		out, _ := json.MarshalIndent(reqs, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	if len(reqs) == 0 {
		fmt.Fprintln(stdout, "no requests recorded")
		return 0
	}
	for _, r := range reqs {
		fmt.Fprintf(stdout, "%s  %-4s %3d  %-24s  %-20s  %dms\n", r.Time, r.Method, r.Status, r.Model, r.Path, r.DurMillis)
	}
	return 0
}

// --- attachment ------------------------------------------------------------

func runAgentAttachment(args []string, stdout, stderr io.Writer, styled bool) int {
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent attachment: %v\n", progName, e); return 1 }
	if len(args) == 0 {
		return fail(fmt.Errorf("needs a subcommand (list, add or remove)"))
	}
	sub, rest := args[0], args[1:]

	asJSON := false
	var positional []string
	for _, a := range rest {
		if a == "--json" {
			asJSON = true
		} else {
			positional = append(positional, a)
		}
	}

	m, err := agent.OpenModel()
	if err != nil {
		return fail(err)
	}

	switch sub {
	case "list":
		if len(positional) != 1 {
			return fail(fmt.Errorf("list needs a project name"))
		}
		atts, err := m.Attachments(positional[0])
		if err != nil {
			return fail(err)
		}
		if asJSON {
			out, _ := json.MarshalIndent(atts, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		if len(atts) == 0 {
			fmt.Fprintln(stdout, "no attachments")
			return 0
		}
		for _, a := range atts {
			kind := "file"
			if a.IsDir {
				kind = "dir"
			}
			fmt.Fprintf(stdout, "%-4s %10d  %s\n", kind, a.Size, a.Name)
		}
		return 0

	case "add":
		if len(positional) != 2 {
			return fail(fmt.Errorf("add needs NAME PATH"))
		}
		name, err := m.AddAttachment(positional[0], positional[1])
		if err != nil {
			return fail(err)
		}
		if asJSON {
			out, _ := json.MarshalIndent(map[string]string{"name": name}, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		fmt.Fprintf(stdout, "added %s\n", name)
		return 0

	case "remove":
		if len(positional) != 2 {
			return fail(fmt.Errorf("remove needs NAME FILE"))
		}
		if err := m.RemoveAttachment(positional[0], positional[1]); err != nil {
			return fail(err)
		}
		if asJSON {
			fmt.Fprintln(stdout, "{}")
			return 0
		}
		fmt.Fprintf(stdout, "removed %s\n", positional[1])
		return 0

	default:
		return fail(fmt.Errorf("unknown subcommand %q (want list, add or remove)", sub))
	}
}

// --- memory-read -----------------------------------------------------------

func runAgentMemoryRead(args []string, stdout, stderr io.Writer, styled bool) int {
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent memory-read: %v\n", progName, e); return 1 }

	levelKind, profileName, project, asJSON := "", "", "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--level":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--level needs a value"))
			}
			levelKind = args[i+1]
			i++
		case "--profile":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--profile needs a value"))
			}
			profileName = args[i+1]
			i++
		case "--project":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--project needs a value"))
			}
			project = args[i+1]
			i++
		case "--json":
			asJSON = true
		default:
			return fail(fmt.Errorf("unexpected argument %q", args[i]))
		}
	}
	if levelKind == "" {
		return fail(fmt.Errorf("needs --level system|profile|project"))
	}
	level, err := agent.ParseMemLevel(levelKind, profileName, project)
	if err != nil {
		return fail(err)
	}
	m, err := agent.OpenModel()
	if err != nil {
		return fail(err)
	}
	text, err := m.MemoryText(level)
	if err != nil {
		return fail(err)
	}
	path := memoryPath(m, level)

	if asJSON {
		out, _ := json.MarshalIndent(map[string]string{"text": text, "path": path}, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fmt.Fprintf(stdout, "%s\n\n%s\n", path, text)
	return 0
}

// memoryPath rebuilds a level's memoria.md path from Model's public
// surface (Root, ProjectDir) rather than its private memoriaPath — the §2
// layout it encodes (<root>/memoria.md, <root>/profiles/<p>/memoria.md,
// <root>/projects/<n>/memoria.md) is the binding contract, stable across
// the API.
func memoryPath(m *agent.Model, level agent.MemLevel) string {
	switch level.Kind {
	case agent.MemProfile:
		return filepath.Join(m.Root(), "profiles", level.Name, "memoria.md")
	case agent.MemProject:
		return filepath.Join(m.ProjectDir(level.Name), "memoria.md")
	default: // agent.MemSystem
		return filepath.Join(m.Root(), "memoria.md")
	}
}

// --- session-prune -----------------------------------------------------

func runAgentSessionPrune(args []string, stdout, stderr io.Writer, styled bool) int {
	fail := func(e error) int { fmt.Fprintf(stderr, "%s: agent session-prune: %v\n", progName, e); return 1 }

	olderThanDays, asJSON := 30, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--older-than":
			if i+1 >= len(args) {
				return fail(fmt.Errorf("--older-than needs a number of days"))
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return fail(fmt.Errorf("bad --older-than %q", args[i+1]))
			}
			olderThanDays = n
			i++
		case "--json":
			asJSON = true
		default:
			return fail(fmt.Errorf("unexpected argument %q", args[i]))
		}
	}

	n, err := agent.PruneSessionRecords(time.Duration(olderThanDays)*24*time.Hour, time.Now())
	if err != nil {
		return fail(err)
	}
	if asJSON {
		out, _ := json.MarshalIndent(map[string]int{"removed": n}, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	fmt.Fprintf(stdout, "removed %d session record(s)\n", n)
	return 0
}
