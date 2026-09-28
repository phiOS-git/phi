package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// The timeline normaliser (plan §6): turns a pi session JSONL — or, for a
// live session, the equivalent get_entries response — into the flat, ordered
// Timeline the panel renders, and ComputeStats into the Stats it shows
// alongside it. Both `phi agent serve` (for a disk-backed session) and the
// CLI `--json` verbs call LoadTimelineFile; a live session instead calls
// BuildTimeline directly on entries fetched over RPC, so the two paths never
// diverge in how an entry becomes an Item.

// entryHead is the handful of fields every tree entry shares
// (session-format.md's SessionEntryBase), used to route a raw line to its
// full type and to walk parentId. The session header also has an "id" field
// (the session's own UUID, not a tree id), so callers must check Type
// against "session" before trusting ID.
type entryHead struct {
	Type     string  `json:"type"`
	ID       string  `json:"id"`
	ParentID *string `json:"parentId"`
}

// ReadSessionEntries reads one pi session JSONL file. It skips the header
// line and any blank or corrupt line, and returns every remaining entry that
// carries an id, in file order. leafID is the id of the last such entry —
// pi always appends at the current leaf, so this is the right fallback when
// no live leafId is known. A bufio.Reader (not Scanner) is used because a
// single line — a long thinking or tool-output block — can run to several
// megabytes, past Scanner's default token limit.
func ReadSessionEntries(path string) (entries []json.RawMessage, leafID string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var h entryHead
			if jerr := json.Unmarshal(trimmed, &h); jerr == nil && h.Type != "session" && h.ID != "" {
				raw := make(json.RawMessage, len(trimmed))
				copy(raw, trimmed)
				entries = append(entries, raw)
				leafID = h.ID
			}
			// A line that fails to parse, or that has no id (the header,
			// or a type phi doesn't recognise), is silently skipped.
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return entries, leafID, rerr
		}
	}
	return entries, leafID, nil
}

// BuildTimeline selects the active branch — entries reachable by walking
// parentId from leafID up to a root — and renders it root-to-leaf into
// Items (plan §6.1). Branches off that path (abandoned forks) contribute no
// items; ComputeStats still counts their usage, since it was spent either
// way. If leafID is empty or not among entries, the last entry stands in for
// it (mirrors ReadSessionEntries's own fallback, so BuildTimeline is safe to
// call with leafID "" on a file that was read some other way).
func BuildTimeline(entries []json.RawMessage, leafID string) Timeline {
	byID := make(map[string]json.RawMessage, len(entries))
	parent := make(map[string]string, len(entries))
	hasParent := make(map[string]bool, len(entries))
	for _, e := range entries {
		var h entryHead
		if err := json.Unmarshal(e, &h); err != nil || h.ID == "" {
			continue
		}
		byID[h.ID] = e
		if h.ParentID != nil {
			parent[h.ID] = *h.ParentID
			hasParent[h.ID] = true
		}
	}

	leaf := leafID
	if _, ok := byID[leaf]; leaf == "" || !ok {
		if len(entries) == 0 {
			return Timeline{Items: []Item{}}
		}
		var h entryHead
		_ = json.Unmarshal(entries[len(entries)-1], &h)
		leaf = h.ID
	}

	// Walk leaf -> root, guarding against a parentId cycle (which would
	// otherwise loop forever on a corrupted tree).
	var chain []string
	visited := map[string]bool{}
	for cur := leaf; cur != "" && !visited[cur]; {
		if _, ok := byID[cur]; !ok {
			break
		}
		visited[cur] = true
		chain = append(chain, cur)
		if hasParent[cur] {
			cur = parent[cur]
		} else {
			cur = ""
		}
	}

	items := []Item{}
	for i := len(chain) - 1; i >= 0; i-- {
		items = appendItems(items, byID[chain[i]])
	}
	return Timeline{Items: items, LeafID: leaf}
}

// LoadTimelineFile is the disk-backed convenience path: read the JSONL, then
// build the timeline from it. entries is also returned so a caller can feed
// it straight into ComputeStats without reading the file twice.
func LoadTimelineFile(path string) (Timeline, []json.RawMessage, error) {
	entries, leafID, err := ReadSessionEntries(path)
	if err != nil {
		return Timeline{}, nil, err
	}
	return BuildTimeline(entries, leafID), entries, nil
}

// appendItems converts one active-branch entry into zero or one Items,
// appending it to items — except a toolResult entry, which never becomes an
// item and instead merges into the tool Block it answers.
func appendItems(items []Item, raw json.RawMessage) []Item {
	var h entryHead
	if err := json.Unmarshal(raw, &h); err != nil {
		return items
	}
	switch h.Type {
	case "message":
		return appendMessageItem(items, raw, h)
	case "compaction":
		var e struct {
			Timestamp    string `json:"timestamp"`
			Summary      string `json:"summary"`
			TokensBefore int64  `json:"tokensBefore"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return items
		}
		return append(items, Item{
			Kind: "compaction", ID: h.ID, Time: entryTimeMillis(e.Timestamp),
			TokensBefore: e.TokensBefore, Summary: e.Summary,
		})
	case "branch_summary":
		var e struct {
			Timestamp string `json:"timestamp"`
			Summary   string `json:"summary"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return items
		}
		return append(items, Item{
			Kind: "branch", ID: h.ID, Time: entryTimeMillis(e.Timestamp), Summary: e.Summary,
		})
	case "model_change":
		var e struct {
			Timestamp string `json:"timestamp"`
			Provider  string `json:"provider"`
			ModelID   string `json:"modelId"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return items
		}
		return append(items, Item{
			Kind: "notice", ID: h.ID, Time: entryTimeMillis(e.Timestamp),
			Text: fmt.Sprintf("Model changed to %s/%s", e.Provider, e.ModelID),
		})
	case "thinking_level_change":
		var e struct {
			Timestamp     string `json:"timestamp"`
			ThinkingLevel string `json:"thinkingLevel"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return items
		}
		return append(items, Item{
			Kind: "notice", ID: h.ID, Time: entryTimeMillis(e.Timestamp),
			Text: fmt.Sprintf("Thinking level set to %s", e.ThinkingLevel),
		})
	case "custom_message":
		var e struct {
			Timestamp  string          `json:"timestamp"`
			CustomType string          `json:"customType"`
			Content    json.RawMessage `json:"content"`
			Display    bool            `json:"display"`
		}
		if err := json.Unmarshal(raw, &e); err != nil || !e.Display {
			return items
		}
		return append(items, Item{
			Kind: "custom", ID: h.ID, Time: entryTimeMillis(e.Timestamp),
			CustomType: e.CustomType, Text: extractText(e.Content),
		})
	default:
		// usage, custom (extension state), context_edit, label,
		// session_info and anything phi doesn't recognise carry no
		// renderable item.
		return items
	}
}

// piUsage mirrors pi's Usage (message-types.md); reasoning is already folded
// into output, so it is not tracked separately here.
type piUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
	Cost        struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

func (u *piUsage) tokens() Tokens {
	return Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Total: u.TotalTokens}
}

func (u *piUsage) itemUsage() ItemUsage {
	return ItemUsage{Tokens: u.tokens(), Cost: u.Cost.Total}
}

// piMessageFull is the union of every AgentMessage field phi's timeline
// needs, across every role — cheaper than one struct per role, and each
// role only ever populates its own subset.
type piMessageFull struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp int64           `json:"timestamp"` // ms

	// assistant
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	StopReason   string   `json:"stopReason"`
	ErrorMessage string   `json:"errorMessage"`
	Usage        *piUsage `json:"usage"`

	// toolResult
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Details    json.RawMessage `json:"details"`
	IsError    bool            `json:"isError"`

	// bashExecution
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode *int   `json:"exitCode"`

	// custom (role "custom", the CustomMessage variant)
	CustomType string `json:"customType"`
	Display    bool   `json:"display"`
}

// piContentBlock is the union of TextContent/ThinkingContent/ToolCall
// (ImageContent carries no field phi's Blocks use, beyond being counted).
type piContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Redacted  bool            `json:"redacted"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// appendMessageItem handles a "message" entry: it dispatches on the nested
// AgentMessage's role, since that — not the entry type — decides the Item
// kind (or, for toolResult, that no Item is produced at all).
func appendMessageItem(items []Item, raw json.RawMessage, h entryHead) []Item {
	var envelope struct {
		Timestamp string          `json:"timestamp"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Message) == 0 {
		return items
	}
	var pm piMessageFull
	if err := json.Unmarshal(envelope.Message, &pm); err != nil {
		return items
	}
	t := pm.Timestamp
	if t == 0 {
		t = entryTimeMillis(envelope.Timestamp)
	}

	switch pm.Role {
	case "user":
		return append(items, Item{
			Kind: "user", ID: h.ID, Time: t,
			Text: extractText(pm.Content), Images: countImages(pm.Content),
		})
	case "assistant":
		errText := pm.ErrorMessage
		if errText == "" && pm.StopReason == "error" {
			errText = "error"
		}
		var usage *ItemUsage
		if pm.Usage != nil {
			u := pm.Usage.itemUsage()
			usage = &u
		}
		return append(items, Item{
			Kind: "assistant", ID: h.ID, Time: t,
			Provider: pm.Provider, Model: pm.Model, StopReason: pm.StopReason, Error: errText,
			Usage: usage, Blocks: blocksFromContent(pm.Content),
		})
	case "toolResult":
		mergeToolResult(items, pm, t)
		return items
	case "bashExecution":
		out, _ := capText(pm.Output, 20000)
		return append(items, Item{
			Kind: "bash", ID: h.ID, Time: t,
			Command: pm.Command, Output: out, ExitCode: pm.ExitCode,
		})
	case "custom":
		if !pm.Display {
			return items
		}
		return append(items, Item{
			Kind: "custom", ID: h.ID, Time: t,
			CustomType: pm.CustomType, Text: extractText(pm.Content),
		})
	default:
		// system, branchSummary, compactionSummary (constructed roles that
		// are never actually persisted this way) and anything unknown.
		return items
	}
}

// keepDetails is the tool allow-list ToolResult.Details is passed through
// for (types.go) — the tools the panel renders with a dedicated card
// (PlanCard, SubagentCard, the ask_user answer), where the structured
// payload carries information the plain result text does not.
func keepDetails(toolName string) bool {
	switch toolName {
	case "plan", "subagent", "ask_user":
		return true
	default:
		return false
	}
}

// mergeToolResult finds the tool Block a toolResult message answers —
// searching already-emitted items latest-first, since a tool call and its
// result are always on the active branch and the call necessarily precedes
// the result — and fills in its Result and EndTime. A callId with no
// matching block (a result for a call outside the active branch, or a
// corrupted transcript) is silently dropped; there is nothing to attach it to.
func mergeToolResult(items []Item, pm piMessageFull, endTime int64) {
	text, truncated := capText(extractText(pm.Content), 20000)
	result := &ToolResult{Text: text, IsError: pm.IsError, Truncated: truncated}
	if keepDetails(pm.ToolName) && len(pm.Details) > 0 && len(pm.Details) <= 64*1024 {
		var v any
		if err := json.Unmarshal(pm.Details, &v); err == nil {
			result.Details = v
		}
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Kind != "assistant" {
			continue
		}
		blocks := items[i].Blocks
		for j := len(blocks) - 1; j >= 0; j-- {
			if blocks[j].Type == "tool" && blocks[j].CallID == pm.ToolCallID {
				blocks[j].Result = result
				blocks[j].EndTime = endTime
				return
			}
		}
	}
}

// blocksFromContent converts an assistant message's content array into
// Blocks, in content order. Image blocks (not part of the Block union) are
// skipped, matching the plan's Block shape.
func blocksFromContent(raw json.RawMessage) []Block {
	var cbs []piContentBlock
	if err := json.Unmarshal(raw, &cbs); err != nil {
		return nil
	}
	var blocks []Block
	for _, cb := range cbs {
		switch cb.Type {
		case "thinking":
			blocks = append(blocks, Block{Type: "thinking", Text: cb.Thinking, Redacted: cb.Redacted})
		case "text":
			blocks = append(blocks, Block{Type: "text", Text: cb.Text})
		case "toolCall":
			var args map[string]any
			if len(cb.Arguments) > 0 {
				_ = json.Unmarshal(cb.Arguments, &args)
			}
			blocks = append(blocks, Block{
				Type: "tool", CallID: cb.ID, Name: cb.Name, Args: args, Summary: ToolSummary(cb.Name, args),
			})
		}
	}
	return blocks
}

// countImages counts "image" blocks in a user message's content; a bare
// string content (no attachments) has none.
func countImages(raw json.RawMessage) int {
	var blocks []piContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return 0
	}
	n := 0
	for _, b := range blocks {
		if b.Type == "image" {
			n++
		}
	}
	return n
}

// capText truncates s to at most max runes, reporting whether it cut
// anything — the 20 000-char cap plan §6.2 puts on tool output and result
// text, so one runaway tool call cannot blow up a session's payload size.
func capText(s string, max int) (text string, truncated bool) {
	r := []rune(s)
	if len(r) <= max {
		return s, false
	}
	return string(r[:max]), true
}

// truncateRunes shortens s to at most n runes, appending an ellipsis when it
// had to cut — used for the one-line tool summaries (plan §6.2), which must
// stay short regardless of what a tool argument actually contains.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// firstLine returns s up to its first newline, or all of s if it has none —
// bash and subagent summaries show only the command/task's opening line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// entryTimeMillis parses a session entry's own ISO 8601 timestamp (used as
// Item.Time when the entry has no nested message with its own ms
// timestamp). An unparseable or empty string yields 0 rather than an error,
// since a malformed timestamp should not stop the rest of the timeline from
// rendering.
func entryTimeMillis(iso string) int64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// planStepCount reads {steps:[{status,...}]} out of a plan tool's arguments
// — shared by ToolSummary (from a toolCall's own args) and PlanProgress
// (from the last plan block anywhere in the timeline), so both agree on
// what counts as "done".
func planStepCount(args map[string]any) (done, total int, ok bool) {
	stepsRaw, exists := args["steps"]
	if !exists {
		return 0, 0, false
	}
	steps, isSlice := stepsRaw.([]any)
	if !isSlice {
		return 0, 0, false
	}
	total = len(steps)
	for _, s := range steps {
		m, isMap := s.(map[string]any)
		if !isMap {
			continue
		}
		if status, _ := m["status"].(string); status == "done" || status == "skipped" {
			done++
		}
	}
	return done, total, true
}

// ToolSummary is the one-line gist of a tool call the timeline row and the
// inspector both show, from its arguments alone — it never waits for the
// result. Rules are plan §6.2's table; anything phi doesn't special-case
// falls back to the first string argument, in sorted key order, so the
// summary is at least deterministic across runs.
func ToolSummary(name string, args map[string]any) string {
	str := func(k string) string {
		s, _ := args[k].(string)
		return s
	}
	switch name {
	case "read", "write", "edit", "ls":
		return str("path")
	case "bash":
		return truncateRunes(firstLine(str("command")), 80)
	case "grep":
		pattern := str("pattern")
		if path := str("path"); path != "" {
			return pattern + " in " + path
		}
		return pattern
	case "find":
		return str("pattern")
	case "plan":
		done, total, _ := planStepCount(args)
		return fmt.Sprintf("%d/%d steps", done, total)
	case "subagent":
		return truncateRunes(firstLine(str("task")), 80)
	case "ask_user":
		return truncateRunes(str("question"), 80)
	default:
		keys := make([]string, 0, len(args))
		for k := range args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := args[k].(string); ok {
				return truncateRunes(s, 80)
			}
		}
		return ""
	}
}

// FilesFromTimeline collects the paths read and modified across every tool
// call on the active branch (plan §6.2's inspector "Files touched" group),
// deduplicated in first-seen order. It never returns nil slices, so a
// client can range over the result without a nil check.
func FilesFromTimeline(t Timeline) FilesTouched {
	var read, modified []string
	seenRead := map[string]bool{}
	seenMod := map[string]bool{}
	for _, it := range t.Items {
		if it.Kind != "assistant" {
			continue
		}
		for _, b := range it.Blocks {
			if b.Type != "tool" {
				continue
			}
			path, _ := b.Args["path"].(string)
			if path == "" {
				continue
			}
			switch b.Name {
			case "read", "ls", "grep":
				if !seenRead[path] {
					seenRead[path] = true
					read = append(read, path)
				}
			case "write", "edit":
				if !seenMod[path] {
					seenMod[path] = true
					modified = append(modified, path)
				}
			}
		}
	}
	if read == nil {
		read = []string{}
	}
	if modified == nil {
		modified = []string{}
	}
	return FilesTouched{Read: read, Modified: modified}
}

// PlanProgress reports the latest plan tool call's step count, searching
// the timeline newest-first so a later `plan` call always supersedes an
// earlier one. ok is false when the branch has no plan call at all — the
// caller (the inspector's Plan group, CodingRow.Plan) then shows nothing
// rather than a stale or fabricated 0/0.
func PlanProgress(t Timeline) (done, total int, ok bool) {
	for i := len(t.Items) - 1; i >= 0; i-- {
		it := t.Items[i]
		if it.Kind != "assistant" {
			continue
		}
		for j := len(it.Blocks) - 1; j >= 0; j-- {
			b := it.Blocks[j]
			if b.Type == "tool" && b.Name == "plan" {
				return planStepCount(b.Args)
			}
		}
	}
	return 0, 0, false
}

// lastAssistantItem returns the timeline's most recent assistant item, or
// nil when there is none — shared by ComputeStats (context estimate) and
// coding.go (state derived from the last turn's stop reason).
func lastAssistantItem(t Timeline) *Item {
	for i := len(t.Items) - 1; i >= 0; i-- {
		if t.Items[i].Kind == "assistant" {
			return &t.Items[i]
		}
	}
	return nil
}

// ActivityFromTimeline reports what the agent is doing right now, read off
// the timeline alone (no live event stream): if the last assistant item's
// last block is a tool call still waiting on its result, that call is the
// activity; otherwise the turn is finished and there is nothing to report.
func ActivityFromTimeline(t Timeline) string {
	last := lastAssistantItem(t)
	if last == nil || len(last.Blocks) == 0 {
		return ""
	}
	b := last.Blocks[len(last.Blocks)-1]
	if b.Type == "tool" && b.Result == nil {
		return b.Name + ": " + b.Summary
	}
	return ""
}

// ComputeStats sums token and cost totals across every entry — every
// branch, not just the active one, because a compaction, an abandoned fork
// or a cache-warm usage entry still spent real tokens — while every count
// and file derived from actual conversation shape comes from the active
// branch's items alone, since those are the only ones a client ever
// renders. Context.Tokens is the last assistant turn's own usage
// (input+cacheRead+cacheWrite+output): the only number available without a
// live model handle to ask for the real context window.
func ComputeStats(entries []json.RawMessage, t Timeline) Stats {
	var tokens Tokens
	var cost float64
	for _, raw := range entries {
		var h entryHead
		if err := json.Unmarshal(raw, &h); err != nil {
			continue
		}
		var usage *piUsage
		switch h.Type {
		case "message":
			var envelope struct {
				Message json.RawMessage `json:"message"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Message) == 0 {
				continue
			}
			var pm struct {
				Role  string   `json:"role"`
				Usage *piUsage `json:"usage"`
			}
			if err := json.Unmarshal(envelope.Message, &pm); err != nil {
				continue
			}
			if pm.Role == "assistant" || pm.Role == "toolResult" {
				usage = pm.Usage
			}
		case "usage", "compaction", "branch_summary":
			var e struct {
				Usage *piUsage `json:"usage"`
			}
			if err := json.Unmarshal(raw, &e); err != nil {
				continue
			}
			usage = e.Usage
		}
		if usage != nil {
			tokens.Add(usage.tokens())
			cost += usage.Cost.Total
		}
	}

	stats := Stats{Tokens: tokens, Cost: cost, Files: FilesFromTimeline(t)}
	for _, it := range t.Items {
		switch it.Kind {
		case "user":
			stats.UserMessages++
		case "assistant":
			stats.AssistantMessages++
			for _, b := range it.Blocks {
				if b.Type == "tool" {
					stats.ToolCalls++
				}
			}
		}
	}
	if n := len(t.Items); n > 0 {
		stats.First = time.UnixMilli(t.Items[0].Time).UTC()
		stats.Last = time.UnixMilli(t.Items[n-1].Time).UTC()
	}
	if last := lastAssistantItem(t); last != nil && last.Usage != nil {
		v := last.Usage.Input + last.Usage.CacheRead + last.Usage.CacheWrite + last.Usage.Output
		stats.Context.Tokens = &v
	}
	return stats
}
