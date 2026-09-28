package agent

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tlMarshal marshals one fixture line — used instead of hand-written JSON so
// nested arrays/maps stay valid without manual escaping.
func tlMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture line: %v", err)
	}
	return string(b)
}

// buildTimelineFixture writes one JSONL session file exercising every entry
// kind BuildTimeline/ComputeStats handle, and returns its path plus the
// timestamps (whole seconds since a fixed base) assigned to each entry so
// the test can recompute expectations rather than hard-code them twice.
//
// Tree shape (id: parent):
//
//	sys1: root  -- system message, skipped from items
//	u1: sys1    -- user
//	a1: u1      -- assistant: thinking+text+toolCall("read"), merged with t1
//	t1: a1      -- toolResult for a1's call, >20000 runes (truncation)
//	a2: t1      -- assistant: toolCall("plan"), 2/3 steps done
//	a3: a2      -- assistant: trailing toolCall("grep"), no result (leaf path)
//	b1: a2      -- ABANDONED branch: assistant toolCall("bash"), usage only
//	b2: b1      -- ABANDONED branch: toolResult for b1, usage only
//	c1: a3      -- compaction
//	m1: c1      -- model_change
//	us1: m1     -- usage entry (leaf); no item of its own
//
// followed by one corrupt trailing line.
func buildTimelineFixture(t *testing.T) (path string, at func(sec float64) time.Time) {
	t.Helper()
	base := time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC)
	at = func(sec float64) time.Time { return base.Add(time.Duration(sec * float64(time.Second))) }
	iso := func(sec float64) string { return at(sec).Format(time.RFC3339) }
	ms := func(sec float64) int64 { return at(sec).UnixMilli() }

	longResult := strings.Repeat("x", 20005)

	usage := func(in, out, cacheRead, cacheWrite, total int64, cost float64) map[string]any {
		return map[string]any{
			"input": in, "output": out, "cacheRead": cacheRead, "cacheWrite": cacheWrite,
			"totalTokens": total, "cost": map[string]any{"total": cost},
		}
	}

	lines := []string{
		`{"type":"session","version":3,"id":"11111111-1111-1111-1111-111111111111","timestamp":"` + iso(0) + `","cwd":"/tmp/proj"}`,
		tlMarshal(t, map[string]any{
			"type": "message", "id": "sys1", "parentId": nil, "timestamp": iso(0),
			"message": map[string]any{"role": "system", "content": "", "timestamp": ms(0)},
		}),
		tlMarshal(t, map[string]any{
			"type": "message", "id": "u1", "parentId": "sys1", "timestamp": iso(1),
			"message": map[string]any{"role": "user", "content": "Please read a.go and check for TODOs", "timestamp": ms(1)},
		}),
		tlMarshal(t, map[string]any{
			"type": "message", "id": "a1", "parentId": "u1", "timestamp": iso(2),
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "let me look at the file"},
					map[string]any{"type": "text", "text": "I'll read a.go."},
					map[string]any{"type": "toolCall", "id": "call1", "name": "read", "arguments": map[string]any{"path": "a.go"}},
				},
				"provider": "anthropic", "model": "claude-x", "stopReason": "tool_use",
				"usage": usage(100, 50, 10, 5, 165, 0.010), "timestamp": ms(2),
			},
		}),
		tlMarshal(t, map[string]any{
			"type": "message", "id": "t1", "parentId": "a1", "timestamp": iso(3),
			"message": map[string]any{
				"role": "toolResult", "toolCallId": "call1", "toolName": "read",
				"content": longResult, "isError": false, "timestamp": ms(3),
			},
		}),
		tlMarshal(t, map[string]any{
			"type": "message", "id": "a2", "parentId": "t1", "timestamp": iso(4),
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "toolCall", "id": "call2", "name": "plan", "arguments": map[string]any{
						"steps": []any{
							map[string]any{"status": "done"},
							map[string]any{"status": "done"},
							map[string]any{"status": "pending"},
						},
					}},
				},
				"usage": usage(20, 10, 0, 0, 30, 0.002), "timestamp": ms(4),
			},
		}),
		tlMarshal(t, map[string]any{
			"type": "message", "id": "a3", "parentId": "a2", "timestamp": iso(5),
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "toolCall", "id": "call3", "name": "grep", "arguments": map[string]any{"pattern": "TODO", "path": "internal"}},
				},
				"usage": usage(15, 5, 0, 0, 20, 0.001), "timestamp": ms(5),
			},
		}),
		tlMarshal(t, map[string]any{
			"type": "message", "id": "b1", "parentId": "a2", "timestamp": iso(4.5),
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "toolCall", "id": "callB", "name": "bash", "arguments": map[string]any{"command": "echo abandoned"}},
				},
				"usage": usage(200, 100, 0, 0, 300, 0.020), "timestamp": ms(4.5),
			},
		}),
		tlMarshal(t, map[string]any{
			"type": "message", "id": "b2", "parentId": "b1", "timestamp": iso(4.6),
			"message": map[string]any{
				"role": "toolResult", "toolCallId": "callB", "toolName": "bash",
				"content": "ok", "isError": false,
				"usage": usage(0, 0, 50, 0, 50, 0.001), "timestamp": ms(4.6),
			},
		}),
		tlMarshal(t, map[string]any{
			"type": "compaction", "id": "c1", "parentId": "a3", "timestamp": iso(6),
			"summary": "earlier context summarized", "tokensBefore": int64(12345),
			"usage": usage(5, 0, 0, 0, 5, 0.0005),
		}),
		tlMarshal(t, map[string]any{
			"type": "model_change", "id": "m1", "parentId": "c1", "timestamp": iso(7),
			"provider": "openai", "modelId": "gpt-5",
		}),
		tlMarshal(t, map[string]any{
			"type": "usage", "id": "us1", "parentId": "m1", "timestamp": iso(8),
			"kind": "cache_warm", "usage": usage(0, 0, 1000, 0, 1000, 0.050),
		}),
		`{"type":"message","id":`, // corrupt trailing line: truncated JSON
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p, at
}

func TestLoadTimelineFileFixture(t *testing.T) {
	path, at := buildTimelineFixture(t)

	tl, entries, err := LoadTimelineFile(path)
	if err != nil {
		t.Fatalf("LoadTimelineFile: %v", err)
	}

	// The corrupt line and the "session" header both carry no usable id, so
	// only the 11 real tree entries survive.
	if len(entries) != 11 {
		t.Fatalf("len(entries) = %d, want 11", len(entries))
	}
	// The corrupt line is skipped without disturbing the leaf fallback: the
	// last valid entry in file order (the top-level usage entry) wins.
	if tl.LeafID != "us1" {
		t.Fatalf("LeafID = %q, want %q", tl.LeafID, "us1")
	}

	wantKinds := []string{"user", "assistant", "assistant", "assistant", "compaction", "notice"}
	if len(tl.Items) != len(wantKinds) {
		t.Fatalf("len(Items) = %d, want %d: %+v", len(tl.Items), len(wantKinds), tl.Items)
	}
	for i, k := range wantKinds {
		if tl.Items[i].Kind != k {
			t.Errorf("Items[%d].Kind = %q, want %q", i, tl.Items[i].Kind, k)
		}
	}
	// The abandoned branch (b1, b2) never appears among the ids of any item.
	for _, it := range tl.Items {
		if it.ID == "b1" || it.ID == "b2" {
			t.Errorf("abandoned entry %q leaked into items", it.ID)
		}
	}

	// a1: thinking + text + tool, with the toolResult merged into the tool
	// block and its 20000-rune cap applied.
	a1 := tl.Items[1]
	if a1.ID != "a1" || len(a1.Blocks) != 3 {
		t.Fatalf("a1 = %+v", a1)
	}
	tool := a1.Blocks[2]
	if tool.Type != "tool" || tool.Name != "read" || tool.CallID != "call1" {
		t.Fatalf("a1 tool block = %+v", tool)
	}
	if tool.Result == nil {
		t.Fatalf("a1 tool block has no merged Result")
	}
	if !tool.Result.Truncated {
		t.Errorf("Result.Truncated = false, want true for a >20000-rune result")
	}
	if got := len([]rune(tool.Result.Text)); got != 20000 {
		t.Errorf("len(Result.Text) = %d, want 20000", got)
	}
	if tool.Result.IsError {
		t.Errorf("Result.IsError = true, want false")
	}
	if tool.EndTime != at(3).UnixMilli() {
		t.Errorf("EndTime = %d, want %d (t1's timestamp)", tool.EndTime, at(3).UnixMilli())
	}

	// a3: a trailing tool call with no result yet — drives the
	// ActivityFromTimeline assertion below.
	a3 := tl.Items[3]
	if a3.ID != "a3" || len(a3.Blocks) != 1 || a3.Blocks[0].Result != nil {
		t.Fatalf("a3 = %+v", a3)
	}

	// Compaction and notice (model_change) items.
	comp := tl.Items[4]
	if comp.TokensBefore != 12345 || comp.Summary != "earlier context summarized" {
		t.Errorf("compaction item = %+v", comp)
	}
	notice := tl.Items[5]
	if notice.Text != "Model changed to openai/gpt-5" {
		t.Errorf("notice.Text = %q", notice.Text)
	}

	if got := ActivityFromTimeline(tl); got != "grep: TODO in internal" {
		t.Errorf("ActivityFromTimeline = %q, want %q", got, "grep: TODO in internal")
	}

	if done, total, ok := PlanProgress(tl); !ok || done != 2 || total != 3 {
		t.Errorf("PlanProgress = (%d, %d, %v), want (2, 3, true)", done, total, ok)
	}

	files := FilesFromTimeline(tl)
	if want := []string{"a.go", "internal"}; !equalStrings(files.Read, want) {
		t.Errorf("FilesFromTimeline.Read = %v, want %v", files.Read, want)
	}
	if len(files.Modified) != 0 {
		t.Errorf("FilesFromTimeline.Modified = %v, want none", files.Modified)
	}

	stats := ComputeStats(entries, tl)
	if stats.UserMessages != 1 {
		t.Errorf("UserMessages = %d, want 1", stats.UserMessages)
	}
	// a1, a2, a3 only: the abandoned branch's assistant message (b1) is not
	// on the active branch and must not be counted here.
	if stats.AssistantMessages != 3 {
		t.Errorf("AssistantMessages = %d, want 3", stats.AssistantMessages)
	}
	if stats.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", stats.ToolCalls)
	}
	// Every entry's usage is summed regardless of branch: a1+a2+a3+b1+b2+c1+us1
	// = 165+30+20+300+50+5+1000, so the abandoned branch's 350 tokens must
	// show up in the total even though it contributed zero items/counts above.
	if stats.Tokens.Total != 1570 {
		t.Errorf("Tokens.Total = %d, want 1570", stats.Tokens.Total)
	}
	if math.Abs(stats.Cost-0.0845) > 1e-9 {
		t.Errorf("Cost = %v, want 0.0845", stats.Cost)
	}
	if stats.Context.Tokens == nil || *stats.Context.Tokens != 20 {
		t.Errorf("Context.Tokens = %v, want 20 (a3's own input+cacheRead+cacheWrite+output)", stats.Context.Tokens)
	}
	if !stats.First.Equal(at(1)) {
		t.Errorf("First = %v, want %v", stats.First, at(1))
	}
	if !stats.Last.Equal(at(7)) {
		t.Errorf("Last = %v, want %v (the notice item's time)", stats.Last, at(7))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestToolSummaryCases(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "bash",
			args: map[string]any{"command": strings.Repeat("a", 100) + "\nsecond line"},
			want: strings.Repeat("a", 80) + "…",
		},
		{
			name: "grep",
			args: map[string]any{"pattern": "pat", "path": "dir"},
			want: "pat in dir",
		},
		{
			name: "grep",
			args: map[string]any{"pattern": "onlypattern"},
			want: "onlypattern",
		},
		{
			// Not a recognised tool: falls back to the first string
			// argument, in sorted key order ("alpha" before "zeta"; the
			// non-string "num" key is skipped).
			name: "mystery_tool",
			args: map[string]any{"zeta": "last", "alpha": "first", "num": 42},
			want: "first",
		},
	}
	for _, c := range cases {
		if got := ToolSummary(c.name, c.args); got != c.want {
			t.Errorf("ToolSummary(%q, %v) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}
