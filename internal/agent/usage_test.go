package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeUsageTranscript drops a minimal pi session JSONL with one assistant
// usage entry (local date `date`, model provider/model) at dir/id, plus its
// sidecar, matching pi's own <timestamp>_<id>.jsonl naming.
func writeUsageTranscript(t *testing.T, dir, id, date, provider, model string, outputTokens int64, cost float64, sidecar Sidecar) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Noon local time on `date`, so it never rolls to a neighbouring day
	// regardless of the test machine's timezone offset.
	when, err := time.ParseInLocation("2006-01-02 15:04", date+" 12:00", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(
		`{"type":"message","id":"a1","parentId":null,"timestamp":%q,`+
			`"message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"timestamp":%d,`+
			`"provider":%q,"model":%q,`+
			`"usage":{"input":10,"output":%d,"cacheRead":0,"cacheWrite":0,"totalTokens":%d,"cost":{"total":%f}}}}`+"\n",
		when.UTC().Format(time.RFC3339), when.UnixMilli(), provider, model,
		outputTokens, outputTokens+10, cost,
	)
	jsonl := filepath.Join(dir, "20260101-000000_"+id+".jsonl")
	if err := os.WriteFile(jsonl, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar.ID = id
	if err := WriteSidecar(dir, sidecar); err != nil {
		t.Fatal(err)
	}
	return jsonl
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestComputeUsageBucketsAndZeroFills(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("study", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}
	prjDir, err := m.SessionsDir("study")
	if err != nil {
		t.Fatal(err)
	}
	sysDir := m.sessionsDirSystem()

	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	today := "2026-09-28"
	yesterday := "2026-09-27"

	writeUsageTranscript(t, sysDir, "s1", today, "anthropic", "claude", 100, 0.50,
		Sidecar{Profile: "general"})
	writeUsageTranscript(t, prjDir, "s2", yesterday, "anthropic", "claude", 200, 1.00,
		Sidecar{Profile: "coding", Project: "study"})

	report, err := ComputeUsage(m, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Days) != 7 {
		t.Fatalf("len(Days) = %d, want 7", len(report.Days))
	}
	if report.Days[6].Date != today {
		t.Errorf("last day = %q, want %q", report.Days[6].Date, today)
	}
	if report.Days[5].Date != yesterday {
		t.Errorf("second-to-last day = %q, want %q", report.Days[5].Date, yesterday)
	}
	if report.Days[6].Cost != 0.50 {
		t.Errorf("today cost = %v, want 0.50", report.Days[6].Cost)
	}
	if report.Days[5].Cost != 1.00 {
		t.Errorf("yesterday cost = %v, want 1.00", report.Days[5].Cost)
	}
	// A day with no usage at all must still appear, zeroed.
	if report.Days[0].Turns != 0 || report.Days[0].Cost != 0 {
		t.Errorf("zero-fill day = %+v, want zeroed", report.Days[0])
	}

	if got := report.ByProfile["general"]; got.Cost != 0.50 || got.Turns != 1 {
		t.Errorf("ByProfile[general] = %+v", got)
	}
	if got := report.ByProfile["coding"]; got.Cost != 1.00 {
		t.Errorf("ByProfile[coding] = %+v", got)
	}
	if got := report.ByProject[""]; got.Cost != 0.50 {
		t.Errorf(`ByProject[""] = %+v`, got)
	}
	if got := report.ByProject["study"]; got.Cost != 1.00 {
		t.Errorf(`ByProject["study"] = %+v`, got)
	}
	if got := report.ByModel["anthropic/claude"]; got.Turns != 2 || got.Cost != 1.50 {
		t.Errorf("ByModel[anthropic/claude] = %+v", got)
	}

	if report.Today.Cost != 0.50 {
		t.Errorf("Today.Cost = %v, want 0.50", report.Today.Cost)
	}
	if report.Week.Cost != 1.50 {
		t.Errorf("Week.Cost = %v, want 1.50", report.Week.Cost)
	}
	if report.Month.Cost != 1.50 {
		t.Errorf("Month.Cost = %v, want 1.50", report.Month.Cost)
	}
}

func TestComputeUsageUnknownModelAndCacheRefresh(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	dir := m.sessionsDirSystem()
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.Local)
	today := now.Format("2006-01-02")

	jsonl := writeUsageTranscript(t, dir, "s1", today, "", "", 50, 0.10, Sidecar{Profile: "general"})

	report, err := ComputeUsage(m, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.ByModel["other"]; got.Cost != 0.10 {
		t.Errorf(`ByModel["other"] = %+v, want cost 0.10`, got)
	}

	// Overwrite the transcript with a second, costlier turn. Without a cache
	// refresh ComputeUsage would still see the stale single-turn total.
	line := fmt.Sprintf(
		`{"type":"message","id":"a2","parentId":null,"timestamp":%q,`+
			`"message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"timestamp":%d,`+
			`"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"total":0.20}}}}`+"\n",
		now.UTC().Format(time.RFC3339), now.UnixMilli(),
	)
	// Ensure a distinct mtime even on filesystems with 1s resolution.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(jsonl, future, future); err != nil {
		t.Fatal(err)
	}
	appendFile(t, jsonl, line)
	if err := os.Chtimes(jsonl, future, future); err != nil {
		t.Fatal(err)
	}

	report2, err := ComputeUsage(m, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := report2.ByModel["other"]; got.Cost < 0.29 || got.Cost > 0.31 {
		t.Errorf(`ByModel["other"] after refresh = %+v, want cost ~0.30`, got)
	}
}
