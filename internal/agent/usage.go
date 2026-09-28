package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Token and cost accounting across every transcript under the data root
// (plan §5.6): `GET /usage` and `phi agent usage` answer from the same
// ComputeUsage. Parsing reuses the entry shapes timeline.go already defines
// (entryHead, piMessageFull, piUsage) so a usage record and a timeline item
// agree on what a line means; ComputeUsage never resolves branches the way
// BuildTimeline does; an abandoned fork still spent real tokens.

// DayUsage is one calendar day's totals. Usage is embedded so its fields
// flatten into the day's JSON object alongside date, matching the plan's
// {"date","tokens","cost","turns"} shape.
type DayUsage struct {
	Date string `json:"date"` // "2006-01-02", local time
	Usage
}

// UsageReport answers GET /usage and `phi agent usage --json` (plan §5.6).
type UsageReport struct {
	Days      []DayUsage       `json:"days"` // oldest -> newest, exactly `days` entries, zero-filled
	ByProfile map[string]Usage `json:"byProfile"`
	ByModel   map[string]Usage `json:"byModel"`   // "provider/model"; "other" when unknown
	ByProject map[string]Usage `json:"byProject"` // "" = unfiled
	Today     Usage            `json:"today"`
	Week      Usage            `json:"week"`
	Month     Usage            `json:"month"`
}

// usageRecord is one entry's contribution, extracted from a transcript file
// independent of which project/profile it belongs to (that comes from the
// transcript's own metadata, attached by the caller).
type usageRecord struct {
	date   string // local "2006-01-02"
	model  string // "provider/id" or "other", never ""
	tokens Tokens
	cost   float64
	turns  int
}

// usageFileCache holds one file's parsed records, valid as long as size and
// mtime match what was last seen — repeat ComputeUsage calls then only
// re-parse the transcripts that actually changed.
type usageFileCache struct {
	size    int64
	modTime time.Time
	records []usageRecord
}

var (
	usageCacheMu sync.Mutex
	usageCache   = map[string]usageFileCache{}
)

// ComputeUsage sums token/cost usage from every transcript ListTranscripts
// finds (chats, projects, coding — everything under the data root), bucketed
// by local calendar day, profile, model and project. today/week/month are
// fixed 1/7/30-local-day windows ending on now's local date, independent of
// days; Days/ByProfile/ByModel/ByProject cover exactly the `days` window.
func ComputeUsage(m *Model, days int, now time.Time) (UsageReport, error) {
	if days <= 0 {
		days = 30
	}
	today := now.Local()
	todayDate := today.Format("2006-01-02")
	weekStart := today.AddDate(0, 0, -6).Format("2006-01-02")
	monthStart := today.AddDate(0, 0, -29).Format("2006-01-02")
	windowStart := today.AddDate(0, 0, -(days - 1))

	report := UsageReport{
		ByProfile: map[string]Usage{},
		ByModel:   map[string]Usage{},
		ByProject: map[string]Usage{},
	}
	dayIndex := make(map[string]int, days)
	report.Days = make([]DayUsage, days)
	for i := 0; i < days; i++ {
		d := windowStart.AddDate(0, 0, i).Format("2006-01-02")
		report.Days[i] = DayUsage{Date: d}
		dayIndex[d] = i
	}

	metas, err := m.ListTranscripts("", false)
	if err != nil {
		return UsageReport{}, err
	}
	for _, meta := range metas {
		if meta.Path == "" {
			continue // pi has not written a JSONL for this session yet
		}
		records, perr := parseUsageFile(meta.Path)
		if perr != nil {
			continue // an unreadable or corrupt transcript contributes nothing
		}
		for _, rec := range records {
			u := Usage{Tokens: rec.tokens, Cost: rec.cost, Turns: rec.turns}

			if rec.date == todayDate {
				report.Today.Add(u)
			}
			if rec.date >= weekStart && rec.date <= todayDate {
				report.Week.Add(u)
			}
			if rec.date >= monthStart && rec.date <= todayDate {
				report.Month.Add(u)
			}

			idx, inWindow := dayIndex[rec.date]
			if !inWindow {
				continue
			}
			dayTotal := report.Days[idx].Usage
			dayTotal.Add(u)
			report.Days[idx].Usage = dayTotal
			addUsage(report.ByModel, rec.model, u)
			addUsage(report.ByProfile, meta.Profile, u)
			addUsage(report.ByProject, meta.Project, u)
		}
	}
	return report, nil
}

func addUsage(m map[string]Usage, key string, u Usage) {
	v := m[key]
	v.Add(u)
	m[key] = v
}

// parseUsageFile returns path's usage records, from the package cache when
// path's size and mtime have not changed since the last call.
func parseUsageFile(path string) ([]usageRecord, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	usageCacheMu.Lock()
	if cached, ok := usageCache[path]; ok && cached.size == fi.Size() && cached.modTime.Equal(fi.ModTime()) {
		usageCacheMu.Unlock()
		return cached.records, nil
	}
	usageCacheMu.Unlock()

	records, err := parseUsageFileUncached(path)
	if err != nil {
		return nil, err
	}

	usageCacheMu.Lock()
	usageCache[path] = usageFileCache{size: fi.Size(), modTime: fi.ModTime(), records: records}
	usageCacheMu.Unlock()
	return records, nil
}

// parseUsageFileUncached reads a pi session JSONL line by line. A
// bufio.Reader (not Scanner) is used, matching ReadSessionEntries, since a
// single line can run to several megabytes; a line that fails to parse or
// carries no usage is simply skipped.
func parseUsageFileUncached(path string) ([]usageRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var records []usageRecord
	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if rec, ok := parseUsageLine(trimmed); ok {
				records = append(records, rec)
			}
		}
		if rerr != nil {
			break // io.EOF, or any other read error: stop, tolerating a partial trailing line
		}
	}
	return records, nil
}

// parseUsageLine extracts at most one usageRecord from one JSONL line: an
// assistant message (full usage, one turn), a toolResult message (tokens/
// cost only, pi does not attribute it to a model), or a standalone usage,
// compaction or branch_summary entry (tokens/cost by the entry's own
// timestamp and, when present, provider/model).
func parseUsageLine(line []byte) (usageRecord, bool) {
	var h entryHead
	if err := json.Unmarshal(line, &h); err != nil {
		return usageRecord{}, false
	}
	switch h.Type {
	case "message":
		var envelope struct {
			Timestamp string          `json:"timestamp"`
			Message   json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil || len(envelope.Message) == 0 {
			return usageRecord{}, false
		}
		var pm piMessageFull
		if err := json.Unmarshal(envelope.Message, &pm); err != nil {
			return usageRecord{}, false
		}
		if pm.Usage == nil {
			return usageRecord{}, false
		}
		t := pm.Timestamp
		if t == 0 {
			t = entryTimeMillis(envelope.Timestamp)
		}
		switch pm.Role {
		case "assistant":
			return usageRecord{
				date: localDate(t), model: usageModelKey(pm.Provider, pm.Model),
				tokens: pm.Usage.tokens(), cost: pm.Usage.Cost.Total, turns: 1,
			}, true
		case "toolResult":
			return usageRecord{
				date: localDate(t), model: "other",
				tokens: pm.Usage.tokens(), cost: pm.Usage.Cost.Total,
			}, true
		default:
			return usageRecord{}, false
		}
	case "usage", "compaction", "branch_summary":
		var e struct {
			Timestamp string   `json:"timestamp"`
			Usage     *piUsage `json:"usage"`
			Provider  string   `json:"provider"`
			Model     string   `json:"model"`
		}
		if err := json.Unmarshal(line, &e); err != nil || e.Usage == nil {
			return usageRecord{}, false
		}
		return usageRecord{
			date: localDate(entryTimeMillis(e.Timestamp)), model: usageModelKey(e.Provider, e.Model),
			tokens: e.Usage.tokens(), cost: e.Usage.Cost.Total,
		}, true
	default:
		return usageRecord{}, false
	}
}

// usageModelKey is the ByModel bucket key: "provider/id" when both are
// known, else "other" — a toolResult usage entry, or a usage/compaction/
// branch_summary entry pi did not tag, has no model to attribute cost to.
func usageModelKey(provider, model string) string {
	if provider == "" || model == "" {
		return "other"
	}
	return provider + "/" + model
}

// localDate formats a millisecond timestamp as the local calendar date the
// usage buckets key on.
func localDate(ms int64) string {
	return time.UnixMilli(ms).Local().Format("2006-01-02")
}
