package doctor

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"time"
)

// driftTimeout is longer than cmdTimeout: --check renders every template
// and runs a pacman deptest, unlike the near-instant queries the other
// checks in this package make.
const driftTimeout = 30 * time.Second

// dotfilesDrift shells out to bin/phios-install --check, which already owns
// repository plan verification. Doctor composes it rather than reimplementing
// profile resolution and package/file planning in Go. Exit codes: 0 clean,
// 1 drift, 2 error. Failures surface as Unknown, not fabricated Problem.
func dotfilesDrift(ctx context.Context, root string) Check {
	const name = "dotfiles drift"
	script := filepath.Join(root, "bin", "phios-install")

	out, avail, err := runTimeout(ctx, driftTimeout, script, "--check")
	if !avail {
		return Check{Name: name, Status: Unknown, Detail: "bin/phios-install not found or not executable"}
	}
	if err == nil {
		return Check{Name: name, Status: OK, Detail: out}
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return Check{Name: name, Status: Problem, Detail: out}
	}
	return Check{Name: name, Status: Unknown, Detail: out + "\n(" + err.Error() + ")"}
}
