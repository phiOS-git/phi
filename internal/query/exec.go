package query

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// runCommand is the one place a provider shells out, matching
// internal/doctor's own run() discipline (S-14): LookPath first so a
// missing tool is silently "no results," never an error a user sees, and
// bounded by whatever context the caller (query.Run's providerTimeout)
// already set — this never adds its own separate timeout on top.
func runCommand(ctx context.Context, name string, args ...string) (output string, available bool) {
	if _, err := exec.LookPath(name); err != nil {
		return "", false
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = nil // provider output only; a tool's own stderr chatter is not a Result
	if err := cmd.Run(); err != nil {
		return "", true // ran, but failed or was killed by the context deadline — caller decides what that means
	}
	return strings.TrimSpace(buf.String()), true
}
