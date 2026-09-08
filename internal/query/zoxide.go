package query

import (
	"context"
	"path/filepath"
	"strings"
)

// ZoxideProvider is the directory/project-jump provider (S-33 AGENT:
// "directory/project jump via zoxide"). zoxide is already in
// profiles/base/packages.txt (pre-approved, master plan §15) for shell
// integration; this reuses its own database rather than reimplementing
// frecency-over-paths a second time.
type ZoxideProvider struct{}

func (ZoxideProvider) Name() string { return "directory" }

func (p ZoxideProvider) Query(ctx context.Context, q string) []Result {
	if q == "" {
		return nil
	}
	// `zoxide query --list` (not the bare, single-best-match form) returns
	// every candidate, already ranked by zoxide's own frecency — that
	// order is preserved below via a descending explicit Score, rather
	// than handing zoxide's own results to matchWeight and re-deciding an
	// order it already computed more accurately (it scores the whole path,
	// this provider's Title is only the basename).
	out, available := runCommand(ctx, "zoxide", "query", "--list", q)
	if !available || out == "" {
		return nil
	}
	var results []Result
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		results = append(results, Result{
			ID: "dir:" + line, Provider: p.Name(),
			Title: filepath.Base(line), Subtitle: line, Score: 75 - float64(i),
			Action: Action{Kind: ActionChangeDir, Data: map[string]string{"path": line}},
		})
	}
	return results
}
