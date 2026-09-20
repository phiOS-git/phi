// Package doctor composes the machine-shape checks `phi doctor` reports.
// It answers: is this machine in the shape the repo expects? Each check
// degrades to Unknown rather than guessing when the tool or privilege it
// needs is not available. An unreachable check is reported as unreachable,
// never silently dropped and never promoted to false ok.
package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Status is one check's verdict. Problem is the only one that makes
// Report.ExitCode non-zero, suitable for timer-driven callers. Unknown
// means the check could not run here, a fact about the environment,
// not a condition on the machine being examined.
type Status string

const (
	OK      Status = "ok"
	Problem Status = "PROBLEM"
	Unknown Status = "unknown"
)

// Check is one composed diagnostic.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Report is one `phi doctor` run.
type Report struct {
	Host   string
	Checks []Check
}

// ExitCode is non-zero exactly when some check is a Problem.
func (r Report) ExitCode() int {
	for _, c := range r.Checks {
		if c.Status == Problem {
			return 1
		}
	}
	return 0
}

// Run composes every check. root/rootErr is tokens.Root()'s result:
// when phios-dotfiles cannot be found, checks that need it (drift, service)
// report that plainly rather than refusing to run altogether. Other checks
// (disk, systemd, SMART, packages) need no checkout and stay useful even on
// machines where phios-dotfiles was never cloned. ctx bounds every external
// command, so a stuck systemctl or smartctl call cannot hang the caller.
func Run(ctx context.Context, root string, rootErr error) Report {
	host := hostName()

	var checks []Check
	checks = append(checks, diskSpace(), failedUnits(ctx))

	if rootErr != nil {
		checks = append(checks, unreachable("dotfiles drift", rootErr))
	} else {
		checks = append(checks, dotfilesDrift(ctx, root))
	}

	checks = append(checks, smartStatus(ctx))

	if rootErr != nil {
		checks = append(checks, unreachable("service status", rootErr))
	} else {
		profiles, found, profilesErr := declaredProfiles(root, host)
		checks = append(checks, serviceStatus(ctx, root, host, profiles, found, profilesErr))
	}

	checks = append(checks, packageCategories(ctx))

	if host == "mini" {
		checks = append(checks, srvMount())
	}

	return Report{Host: host, Checks: checks}
}

func unreachable(name string, err error) Check {
	return Check{Name: name, Status: Unknown, Detail: fmt.Sprintf("cannot locate phios-dotfiles: %v", err)}
}

func hostName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(h, '.'); i >= 0 {
		h = h[:i]
	}
	return h
}

const cmdTimeout = 5 * time.Second

// run executes name with args and returns its combined, trimmed output.
// available is false when name is not on PATH (or, for a path containing a
// slash such as bin/phios-install, not executable there) — the caller's
// signal to report Unknown rather than Problem, since a missing tool is an
// environment fact, not a defect in the machine this command is examining.
func run(ctx context.Context, name string, args ...string) (output string, available bool, err error) {
	return runTimeout(ctx, cmdTimeout, name, args...)
}

func runTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) (output string, available bool, err error) {
	if _, lookErr := exec.LookPath(name); lookErr != nil {
		return "", false, lookErr
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	return strings.TrimSpace(buf.String()), true, err
}

// readList reads a phios-dotfiles list file: one entry per line, truncated
// at the first '#' and trimmed. Mirrors bin/lib/common.sh's phios_read_list,
// with the same lack of quote-awareness (no consumer has needed a literal
// '#' in an entry). A missing file is an empty list, not an error: most
// profiles do not declare every kind of list.
func readList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// declaredProfiles reads hosts/<host>.txt — the same file bin/lib/
// profiles.sh's phios_resolve_profiles reads, in the same order, but purely
// read-only: found is false when the file does not exist at all, which the
// callers report through whichever check asked for it rather than as a hard
// failure, since doctor's job is to report the machine's shape, not to
// enforce it the way phios-install does.
func declaredProfiles(root, host string) (profiles []string, found bool, err error) {
	path := filepath.Join(root, "hosts", host+".txt")
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return nil, false, nil
	} else if statErr != nil {
		return nil, false, statErr
	}
	names, err := readList(path)
	return names, true, err
}
