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

// dotfilesDrift shells out to bin/phios-install --check (S-01/S-03), the
// tool that already owns "is the repository's plan satisfied on this
// machine" — doctor composes it rather than reimplementing profile
// resolution and package/file planning a second time in Go. Exit codes are
// phios-install's own documented contract: 0 clean, 1 drift, 2 error (e.g.
// this cannot run at all on macOS's BSD realpath, per S-05's note — that
// failure surfaces here as Unknown, not as a fabricated Problem).
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
