package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// logRing is phi agent serve's in-memory log (plan §5.8): the last N entries,
// each with a monotonically increasing sequence number so a client can poll
// for "everything after the last one I saw" without missing or repeating
// lines. It lives in memory only — the journal already keeps stderr — and
// exists so Settings can show a structured, filterable view without reading
// the journal.
type logRing struct {
	mu      sync.Mutex
	entries []LogEntry
	max     int
	seq     int64
}

func newLogRing(max int) *logRing { return &logRing{max: max} }

func (r *logRing) add(level, session, source, text string) LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e := LogEntry{Seq: r.seq, Time: time.Now().UTC(), Level: level, Session: session, Source: source, Text: text}
	r.entries = append(r.entries, e)
	if len(r.entries) > r.max {
		r.entries = append([]LogEntry(nil), r.entries[len(r.entries)-r.max:]...)
	}
	return e
}

var logLevelRank = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}

// since returns up to limit entries with Seq > after, at or above minLevel,
// optionally for one session, oldest first, plus the cursor for the next
// call (the last Seq returned, or after when nothing matched).
func (r *logRing) since(after int64, minLevel, session string, limit int) ([]LogEntry, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	min := logLevelRank[minLevel]
	out := []LogEntry{}
	next := after
	for _, e := range r.entries {
		if e.Seq <= after {
			continue
		}
		next = e.Seq
		if logLevelRank[e.Level] < min || (session != "" && e.Session != session) {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, next
}

// lastErrors returns the newest n error-level entries, newest first.
func (r *logRing) lastErrors(n int) []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []LogEntry{}
	for i := len(r.entries) - 1; i >= 0 && len(out) < n; i-- {
		if r.entries[i].Level == "error" {
			out = append(out, r.entries[i])
		}
	}
	return out
}

// logf records one entry and mirrors it to Stderr (the journal), prefixed
// with the session id the way pi's own stderr lines always were.
func (s *Server) logf(level, session, source, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	s.logs.add(level, session, source, text)
	if session != "" {
		fmt.Fprintf(s.Stderr, "[%s] %s\n", session, text)
	} else {
		fmt.Fprintf(s.Stderr, "%s: %s\n", source, text)
	}
}

// piStderrLevel classifies a pi stderr line: stderr is diagnostics only, so
// everything is info unless it plainly reports a failure.
func piStderrLevel(line string) string {
	l := strings.ToLower(line)
	if strings.Contains(l, "error") || strings.Contains(l, "fail") {
		return "warn"
	}
	return "info"
}
