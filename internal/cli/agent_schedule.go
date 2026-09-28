package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"phi/internal/agent"
)

// `phi agent schedule` (binding contract §5.7): list/add/set/rm edit
// schedule.json directly through a non-ticking Scheduler(nil), the same
// file `phi agent serve` ticks — no serve process needs to be running for
// those. `run` is the exception: only a live serve holds the ScheduleHost
// that can actually start a pi session, so it reaches over HTTP instead.

const agentScheduleUsage = `usage: phi agent schedule <verb> [arguments]

Verbs:
  list [--json]              list scheduled jobs.
  add --json-body JSON       create a job from a JSON Job body (§5.7);
                              prints the created Job as JSON.
  set ID --json-body JSON    replace job ID's rule; prints the updated Job
                              as JSON.
  rm ID                      delete a job.
  run ID                     run a job now, through a live
                              'phi agent serve' (even if disabled).
`

// scheduleServeAddr is the fixed address runAgentServe's --listen defaults
// to; `schedule run` has no flag of its own to point elsewhere.
const scheduleServeAddr = "http://127.0.0.1:4199"

func runAgentSchedule(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, agentScheduleUsage)
		return 1
	}
	sub := args[0]
	args = args[1:]

	switch sub {
	case "list":
		asJSON := false
		for _, a := range args {
			if a == "--json" {
				asJSON = true
			}
		}
		f, err := agent.NewScheduler(nil).List()
		if err != nil {
			fmt.Fprintf(stderr, "%s: agent schedule: %v\n", progName, err)
			return 1
		}
		if asJSON {
			out, _ := json.MarshalIndent(f, "", "  ")
			fmt.Fprintln(stdout, string(out))
			return 0
		}
		if len(f.Jobs) == 0 {
			fmt.Fprintln(stdout, "no scheduled jobs")
			return 0
		}
		for _, j := range f.Jobs {
			state := "off"
			if j.Enabled {
				state = "on"
			}
			next := "-"
			if j.Next != nil {
				next = j.Next.Local().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(stdout, "%s  %-3s  %-16s  %s\n", j.ID, state, next, j.Title)
		}
		return 0

	case "add":
		body, rest, ok := takeFlagValue(args, "--json-body")
		if !ok {
			fmt.Fprintf(stderr, "%s: agent schedule: add needs --json-body JSON\n", progName)
			return 1
		}
		if len(rest) > 0 {
			fmt.Fprintf(stderr, "%s: agent schedule: unexpected argument %q\n", progName, rest[0])
			return 1
		}
		var j agent.Job
		if err := json.Unmarshal([]byte(body), &j); err != nil {
			fmt.Fprintf(stderr, "%s: agent schedule: invalid --json-body: %v\n", progName, err)
			return 1
		}
		created, err := agent.NewScheduler(nil).Create(j)
		if err != nil {
			fmt.Fprintf(stderr, "%s: agent schedule: %v\n", progName, err)
			return 1
		}
		out, _ := json.MarshalIndent(created, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0

	case "set":
		if len(args) == 0 {
			fmt.Fprintf(stderr, "%s: agent schedule: set needs a job id\n", progName)
			return 1
		}
		id := args[0]
		body, rest, ok := takeFlagValue(args[1:], "--json-body")
		if !ok {
			fmt.Fprintf(stderr, "%s: agent schedule: set needs --json-body JSON\n", progName)
			return 1
		}
		if len(rest) > 0 {
			fmt.Fprintf(stderr, "%s: agent schedule: unexpected argument %q\n", progName, rest[0])
			return 1
		}
		var j agent.Job
		if err := json.Unmarshal([]byte(body), &j); err != nil {
			fmt.Fprintf(stderr, "%s: agent schedule: invalid --json-body: %v\n", progName, err)
			return 1
		}
		updated, err := agent.NewScheduler(nil).Update(id, j)
		if err != nil {
			fmt.Fprintf(stderr, "%s: agent schedule: %v\n", progName, err)
			return 1
		}
		out, _ := json.MarshalIndent(updated, "", "  ")
		fmt.Fprintln(stdout, string(out))
		return 0

	case "rm":
		if len(args) != 1 {
			fmt.Fprintf(stderr, "%s: agent schedule: rm needs a job id\n", progName)
			return 1
		}
		if err := agent.NewScheduler(nil).Delete(args[0]); err != nil {
			fmt.Fprintf(stderr, "%s: agent schedule: %v\n", progName, err)
			return 1
		}
		fmt.Fprintf(stdout, "removed %s\n", args[0])
		return 0

	case "run":
		if len(args) != 1 {
			fmt.Fprintf(stderr, "%s: agent schedule: run needs a job id\n", progName)
			return 1
		}
		return runAgentScheduleRun(args[0], stdout, stderr)

	case "-h", "--help":
		fmt.Fprint(stdout, agentScheduleUsage)
		return 0

	default:
		fmt.Fprintf(stderr, "%s: agent schedule: unknown verb %q\n", progName, sub)
		fmt.Fprint(stderr, agentScheduleUsage)
		return 1
	}
}

// takeFlagValue pulls "name value" out of args wherever it appears,
// returning the value, the remaining arguments in their original order,
// and whether the flag was present at all.
func takeFlagValue(args []string, name string) (value string, rest []string, ok bool) {
	for i, a := range args {
		if a != name {
			continue
		}
		if i+1 >= len(args) {
			return "", args, false
		}
		rest = make([]string, 0, len(args)-2)
		rest = append(rest, args[:i]...)
		rest = append(rest, args[i+2:]...)
		return args[i+1], rest, true
	}
	return "", args, false
}

// runAgentScheduleRun asks a live `phi agent serve` to run job id now
// (§5.7: POST /schedule/{id}/run "runs now, even when disabled"). Unlike
// list/add/set/rm this cannot be done against schedule.json alone: only
// serve's Scheduler carries the ScheduleHost able to start a pi session.
func runAgentScheduleRun(id string, stdout, stderr io.Writer) int {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(scheduleServeAddr+"/schedule/"+id+"/run", "application/json", nil)
	if err != nil {
		fmt.Fprintf(stderr, "%s: agent schedule: phi agent serve is not running\n", progName)
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Fprintln(stdout, strings.TrimSpace(string(body)))
	if resp.StatusCode >= 400 {
		return 1
	}
	return 0
}
