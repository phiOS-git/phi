package query

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FilesProvider: file search backed by `fd` (live search, capped in Go).
const filesMaxResults = 8

// fileBaseScore is every non-weak file result's flat Score (fd's own match
// against the query is the filter; there is no title-vs-query quality
// gradient left to express here — see rank_test.go's "file: flat score"
// case, which this constant now names explicitly).
const fileBaseScore = 30

// fileDeepComponents: a file more than this many directories below $HOME is
// "deep" and counts as a weak result (see isWeakFile). ~/code/proj/src/x.go
// is 3 directories down and stays non-weak; ~/code/proj/internal/query/x.go
// is 4 and is weak — at this threshold a fair amount of ordinary project
// source ends up weak too, which is the tradeoff to revisit if this needs
// raising.
const fileDeepComponents = 3

// fileWeakQueryLen: below this many characters, a weak file result is
// suppressed unless it matches strongly (see isStrongFileMatch) — a one- to
// three-letter query pulls too much noise out of a deep project tree or
// ~/.cache to be worth showing before the user has typed enough to mean it.
const fileWeakQueryLen = 4

// fileWeakPenalty is subtracted from fileBaseScore for a weak result once
// the query is long enough to let it through, so it still ranks under an
// equivalent non-weak file instead of tying with it. Must stay below
// fileBaseScore: Rank() (rank.go) only trusts a Result's own Score when it
// is non-zero, so a penalty that reaches 0 would send a weak file back
// through matchWeight instead of the flat score this provider intends — see
// the floor applied where this is used.
const fileWeakPenalty = 10

// systemDirNames are build/dependency output directories that are not
// dot-prefixed, so fd's default hidden-file exclusion (no --hidden flag is
// passed in Query below) does not already keep them out the way it keeps
// out .cache, .config, .git and the rest.
var systemDirNames = map[string]bool{
	"node_modules": true,
	"target":       true,
	"vendor":       true,
	"__pycache__":  true,
}

// isWeakFile reports whether path — an absolute result from fd — is a
// "weak" file result: more than fileDeepComponents directories below home,
// passing through a hidden (dot-prefixed) directory, passing through a
// named systemDirNames directory, or not under home at all. fd is invoked
// without --hidden, so in practice a dot-prefixed component is already
// excluded from its output before this ever runs; this check is a backstop
// for that case and the home-made-explicit gatekeeper for the other three,
// none of which fd's own flags cover.
func isWeakFile(home, path string) bool {
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	dir := filepath.Dir(rel)
	if dir == "." {
		return false
	}
	components := strings.Split(dir, string(filepath.Separator))
	if len(components) > fileDeepComponents {
		return true
	}
	for _, c := range components {
		if strings.HasPrefix(c, ".") || systemDirNames[c] {
			return true
		}
	}
	return false
}

// isStrongFileMatch reports whether path's basename is a strong match for
// q: an exact match or a prefix match, case-insensitively — the two bands
// matchWeight (rank.go) itself trusts most (100 and 80). Only these earn a
// weak result a pass through fileWeakQueryLen; a substring or subsequence
// hit is not enough.
func isStrongFileMatch(path, q string) bool {
	base := strings.ToLower(filepath.Base(path))
	needle := strings.ToLower(q)
	return base == needle || strings.HasPrefix(base, needle)
}

// fileResultScore decides whether path is shown for query q and, if so, its
// Score: fileBaseScore for a non-weak file; fileBaseScore for a weak file
// that earns a short query's exception by matching strongly; fileBaseScore
// minus fileWeakPenalty (floored at 1, never 0 — see fileWeakPenalty) for a
// weak file once q has reached fileWeakQueryLen; ok is false when a weak
// file must be suppressed outright (short query, no strong match).
func fileResultScore(home, path, q string) (score float64, ok bool) {
	if !isWeakFile(home, path) {
		return fileBaseScore, true
	}
	if len(q) < fileWeakQueryLen {
		if !isStrongFileMatch(path, q) {
			return 0, false
		}
		return fileBaseScore, true
	}
	score = fileBaseScore - fileWeakPenalty
	if score <= 0 {
		score = 1
	}
	return score, true
}

// imageExtensions: files opened in phi-shell's native floating window
// (not via xdg-open, which has mimeapps.list uncertainties).
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".bmp": true, ".webp": true, ".tiff": true, ".tif": true,
}

func isImageFile(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

// openCommand is the shell command that opens path: phi-shell's own IPC
// target for an image (the same `qs -p ~/.config/quickshell/phi ipc call
// <target> <fn> ...` shape this codebase already uses elsewhere, e.g.
// internal/query/timer.go's "timer add"/"timer addAlarm" calls); xdg-open
// for everything else. Both arguments are quoted — see shellQuote.
func openCommand(path string) string {
	if isImageFile(path) {
		return "qs -p ~/.config/quickshell/phi ipc call image open " + shellQuote(path)
	}
	return "xdg-open " + shellQuote(path)
}

// shellQuote wraps s as one single-quoted POSIX sh word, escaping any single
// quote it contains. The result of this provider's search is a real
// filesystem path under $HOME, which can contain spaces or shell
// metacharacters — Launcher.qml's "exec" action runs Data["command"]
// through `sh -c`, so the path must never be interpolated unquoted.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type FilesProvider struct{}

func (FilesProvider) Name() string { return "file" }

func (p FilesProvider) Query(ctx context.Context, q string) []Result {
	// runner-bar prefix feature: "file <name>" searches for
	// exactly <name> — without this, fd would search for a file literally
	// named "file <name>", almost never a real match.
	if len(q) >= 5 && strings.EqualFold(q[:5], "file ") {
		q = strings.TrimSpace(q[5:])
	}
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
	type match struct {
		path  string
		score float64
	}
	var matches []match
	atMaxScore := 0 // count of matches already at fileBaseScore — see the break below
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		score, ok := fileResultScore(home, line, q)
		if !ok {
			continue // weak result, query too short for anything but a strong match
		}
		matches = append(matches, match{line, score})
		if score == fileBaseScore {
			atMaxScore++
			if atMaxScore >= filesMaxResults {
				// fileBaseScore is the highest fileResultScore ever returns,
				// so a full filesMaxResults set already at it cannot be
				// outranked by anything still unread — the sort below would
				// place exactly these first regardless of what follows.
				break
			}
		}
	}
	// Sorted by score before the cap: fd's own output order is traversal
	// order, not relevance, so truncating first (absent the break above)
	// could fill filesMaxResults with weak matches from one deep or system
	// directory and cut off a stronger, non-weak result fd printed later.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	if len(matches) > filesMaxResults {
		matches = matches[:filesMaxResults]
	}
	var results []Result
	for _, m := range matches {
		results = append(results, Result{
			ID: "file:" + m.path, Provider: p.Name(),
			Title: filepath.Base(m.path), Subtitle: m.path, Score: m.score,
			Action: Action{Kind: ActionExec, Data: map[string]string{"command": openCommand(m.path), "path": m.path}},
		})
	}
	return results
}
