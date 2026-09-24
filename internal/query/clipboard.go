package query

// ClipboardProvider answers the runner-bar's copy/clip/cp keywords with
// clipboard history phi-shell records as it happens. This provider never
// writes an entry, only reads what the shell already wrote to
// $XDG_STATE_HOME/phi/clipboard/. It is deliberately narrow: it only
// ever answers when q itself opens with one of its own keywords, so a
// clipboard entry never leaks into ordinary ranking the way every other
// provider's results can (there is no query text a random clipboard
// clipping would ever be expected to match).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"phi/internal/state"
)

type ClipboardProvider struct{}

func (ClipboardProvider) Name() string { return "clipboard" }

// clipboardKeywords are the runner-bar keywords that route here
// (prefixProviders, query.go) — kept alongside Query itself since Query
// has to recognise them independent of whether the tag is locked.
var clipboardKeywords = []string{"copy", "clip", "cp"}

const (
	clipboardMaxResults    = 50
	clipboardTitleMaxRunes = 80
	// clipboardReadBudget bounds how much of a text entry's .data file is
	// read for its title and filter match — at most a few KB per file,
	// never the whole clipping.
	clipboardReadBudget = 4096
)

func (p ClipboardProvider) Query(_ context.Context, q string) []Result {
	remainder, ok := stripClipboardKeyword(q)
	if !ok {
		return nil
	}
	dir, err := clipboardDir()
	if err != nil {
		return nil
	}
	entries, pinned := loadClipboardEntries(dir)
	rankClipboardEntries(entries, pinned)

	filter := strings.ToLower(strings.TrimSpace(remainder))
	var results []Result
	for _, e := range entries {
		r, ok := clipboardResult(p.Name(), dir, e, filter)
		if !ok {
			continue
		}
		results = append(results, r)
		if len(results) >= clipboardMaxResults {
			break
		}
	}
	// Trusted as-is, strictly decreasing: these results are already
	// filtered and ordered (pinned first, then newest) — Rank's own
	// matchWeight (rank.go) would compare a clipping's Title against the
	// full "clip <remainder>" text and almost always score 0, silently
	// dropping every entry rather than ranking it. rank.go additionally
	// skips adding frecency on top of this, so selection history cannot
	// reorder what this loop already decided.
	for i := range results {
		results[i].Score = float64(clipboardMaxResults - i)
	}
	return results
}

// TagDefaults never invents a clipboard default; it handles the "nothing
// typed yet" case the same way Query itself already does: the bare
// keyword IS that query (stripClipboardKeyword treats it as an empty
// remainder, i.e. no filter), so this is a thin wrapper for symmetry with
// every other TagDefaultsProvider rather than a second code path.
func (p ClipboardProvider) TagDefaults(ctx context.Context, keyword string) []Result {
	return p.Query(ctx, keyword)
}

// stripClipboardKeyword reports whether q opens with one of
// clipboardKeywords followed by a space or nothing else, returning
// whatever (possibly empty) text follows. Anything else — including a
// keyword immediately followed by more letters, "copyist" say — is not a
// match.
func stripClipboardKeyword(q string) (remainder string, ok bool) {
	for _, kw := range clipboardKeywords {
		if strings.EqualFold(q, kw) {
			return "", true
		}
		prefix := kw + " "
		if len(q) >= len(prefix) && strings.EqualFold(q[:len(prefix)], prefix) {
			return strings.TrimSpace(q[len(prefix):]), true
		}
	}
	return "", false
}

// clipboardDir is $XDG_STATE_HOME/phi/clipboard — internal/state's own Dir
// helper already resolves $XDG_STATE_HOME/phi (falling back to
// ~/.local/state/phi), so this reuses it rather than re-implementing the
// same XDG fallback a second time (frecency.go's FrecencyPath does the
// same).
func clipboardDir() (string, error) {
	dir, err := state.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clipboard"), nil
}

// clipboardValidMimes is the closed set of mime values an entry's .mime
// file may declare. Enforced because the mime string ends up unquoted in
// a shell command (`wl-copy --type <mime>`, clipboardResult) — a stray or
// malformed .mime file must never reach that command line.
var clipboardValidMimes = map[string]bool{"text/plain": true, "image/png": true}

type clipboardEntry struct {
	id   string
	mime string
}

// loadClipboardEntries lists every entry phi-shell has written under
// dir/entries (a matching <id>.mime and <id>.data pair, mime one of
// clipboardValidMimes), plus the pinned id set from dir/pins.json. Missing
// or unreadable directories/files degrade to an empty result, never an
// error — the same tolerance every other provider gives a missing
// external file.
func loadClipboardEntries(dir string) (entries []clipboardEntry, pinned map[string]bool) {
	pinned = loadClipboardPins(dir)

	entriesDir := filepath.Join(dir, "entries")
	files, err := os.ReadDir(entriesDir)
	if err != nil {
		return nil, pinned
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".mime") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".mime")
		mimeData, err := os.ReadFile(filepath.Join(entriesDir, f.Name()))
		if err != nil {
			continue
		}
		mime := strings.TrimSpace(string(mimeData))
		if !clipboardValidMimes[mime] {
			continue
		}
		if _, err := os.Stat(filepath.Join(entriesDir, id+".data")); err != nil {
			continue
		}
		entries = append(entries, clipboardEntry{id: id, mime: mime})
	}
	return entries, pinned
}

// loadClipboardPins reads dir/pins.json — a JSON array of pinned entry
// ids. Tolerant of either JSON strings or JSON numbers for each id (a
// nanosecond timestamp overflows a JS/QML number's safe integer range, so
// in practice the shell always writes strings, but a number is accepted
// too rather than silently losing every pin if that ever changes).
func loadClipboardPins(dir string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(dir, "pins.json"))
	if err != nil {
		return out
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return out
	}
	for _, item := range raw {
		var s string
		if err := json.Unmarshal(item, &s); err == nil && s != "" {
			out[s] = true
			continue
		}
		var n int64
		if err := json.Unmarshal(item, &n); err == nil {
			out[strconv.FormatInt(n, 10)] = true
		}
	}
	return out
}

// rankClipboardEntries sorts entries in place: pinned first, then newest
// first by id (a nanosecond timestamp — parsed numerically, since string
// comparison only stays correct while every id has the same digit count).
// An unparseable id sorts last within its pinned/unpinned group.
func rankClipboardEntries(entries []clipboardEntry, pinned map[string]bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		pi, pj := pinned[entries[i].id], pinned[entries[j].id]
		if pi != pj {
			return pi
		}
		ni, errI := strconv.ParseInt(entries[i].id, 10, 64)
		nj, errJ := strconv.ParseInt(entries[j].id, 10, 64)
		if errI != nil || errJ != nil {
			return errI == nil // a parseable id still beats an unparseable one
		}
		return ni > nj
	})
}

// clipboardResult builds e's Result, or ok=false when e is filtered out
// (an image entry only matches an empty filter or the word "image"; a
// text entry matches a case-insensitive substring of its own content
// within clipboardReadBudget).
func clipboardResult(provider, dir string, e clipboardEntry, filter string) (Result, bool) {
	path := filepath.Join(dir, "entries", e.id+".data")
	age := clipboardAge(e.id, time.Now())
	command := "wl-copy --type " + e.mime + " < " + shellQuote(path)

	if e.mime == "image/png" {
		if filter != "" && filter != "image" {
			return Result{}, false
		}
		return Result{
			ID: "clip:" + e.id, Provider: provider,
			Title: "Image", Subtitle: age,
			Action: Action{Kind: ActionExec, Data: map[string]string{"command": command}},
		}, true
	}

	content, ok := readClipboardText(path)
	if !ok {
		return Result{}, false
	}
	if filter != "" && !strings.Contains(strings.ToLower(content), filter) {
		return Result{}, false
	}
	return Result{
		ID: "clip:" + e.id, Provider: provider,
		Title: clipboardTitle(content), Subtitle: age,
		Action: Action{Kind: ActionExec, Data: map[string]string{"command": command}},
	}, true
}

// readClipboardText reads at most clipboardReadBudget bytes of path.
func readClipboardText(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, clipboardReadBudget)
	n, err := f.Read(buf)
	if n == 0 && err != nil {
		return "", false
	}
	return string(buf[:n]), true
}

// clipboardTitle is content's first line, trimmed to clipboardTitleMaxRunes.
func clipboardTitle(content string) string {
	line := content
	if i := strings.IndexAny(content, "\r\n"); i >= 0 {
		line = content[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "(empty)"
	}
	runes := []rune(line)
	if len(runes) > clipboardTitleMaxRunes {
		return string(runes[:clipboardTitleMaxRunes]) + "…"
	}
	return line
}

// clipboardAge formats id (a nanosecond Unix timestamp) as a short
// relative age against now, falling back to an absolute date once the
// entry is a week or older. Pure (now passed in) so it is deterministic
// under test.
func clipboardAge(id string, now time.Time) string {
	ns, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return ""
	}
	d := now.Sub(time.Unix(0, ns))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	case d < 7*24*time.Hour:
		return strconv.Itoa(int(d.Hours())/24) + "d ago"
	default:
		return time.Unix(0, ns).Format("2006-01-02")
	}
}
