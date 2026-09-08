package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// FilesProvider is the file-search provider (S-33 AGENT: "files"), backed
// by `fd` (already in profiles/base/packages.txt, master plan §15 — not a
// new dependency). This is a LIVE search, never a persisted index: I-08's
// constraint on a file index living on the encrypted volume and excluded
// from sync applies to a stored index, which this provider does not build,
// so it does not apply here.
//
// The result count is capped in Go after fd runs, not via an fd flag —
// this project's own discipline elsewhere (S-23's brightness type, C-11)
// is to verify an external tool's exact interface before depending on it,
// and this agent could not confirm the installed fd version's flag name
// for a result cap without guessing; truncating the already-returned
// output needs no such guess.
const filesMaxResults = 8

type FilesProvider struct{}

func (FilesProvider) Name() string { return "file" }

func (p FilesProvider) Query(ctx context.Context, q string) []Result {
	// Two characters minimum: fd across the whole home directory on every
	// single keystroke of a one-letter query is exactly the kind of
	// per-query cost providerTimeout exists to bound, and a one-character
	// file search is rarely useful even when it completes.
	if len(q) < 2 {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	out, available := runCommand(ctx, "fd", "--type", "f", "--", q, home)
	if !available || out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	var results []Result
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		results = append(results, Result{
			ID: "file:" + line, Provider: p.Name(),
			Title: filepath.Base(line), Subtitle: line, Score: 30,
			Action: Action{Kind: ActionExec, Data: map[string]string{"command": "xdg-open " + line}},
		})
		if len(results) >= filesMaxResults {
			break
		}
	}
	return results
}
