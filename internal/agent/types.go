package agent

import "time"

// Wire types of the level-2 agent API (docs/agent-panel-plan.md §5–§6 in the
// workspace): the timeline, per-session statistics, usage, dialogs and log
// entries. They are shared by `phi agent serve` and the `--json` CLI verbs,
// so both surfaces always describe the same thing in the same shape.

// APILevel is reported by GET /health. A client compares it against the
// level it needs and degrades instead of failing when phi is older.
const APILevel = 2

// Tokens is a token breakdown, summed the way pi's Usage reports it
// (message-types.md: reasoning is already part of output).
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"`
}

// Add accumulates o into t.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.Total += o.Total
}

// ItemUsage is one assistant message's usage: its tokens and total cost in
// US dollars.
type ItemUsage struct {
	Tokens
	Cost float64 `json:"cost"`
}

// Usage is an aggregate over many messages.
type Usage struct {
	Tokens Tokens  `json:"tokens"`
	Cost   float64 `json:"cost"`
	Turns  int     `json:"turns"`
}

// Add accumulates o into u.
func (u *Usage) Add(o Usage) {
	u.Tokens.Add(o.Tokens)
	u.Cost += o.Cost
	u.Turns += o.Turns
}

// ToolResult is a tool call's outcome, merged into its Block. Text is capped
// (Truncated reports it); Details is passed through only for the tools the
// panel renders specially (see keepDetails).
type ToolResult struct {
	Text      string `json:"text"`
	IsError   bool   `json:"isError"`
	Truncated bool   `json:"truncated"`
	Details   any    `json:"details,omitempty"`
}

// Block is one content block of an assistant message: "thinking", "text" or
// "tool". Fields not used by a block type are omitted.
type Block struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	Redacted bool           `json:"redacted,omitempty"`
	CallID   string         `json:"callId,omitempty"`
	Name     string         `json:"name,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	Summary  string         `json:"summary,omitempty"`
	Result   *ToolResult    `json:"result,omitempty"`
	EndTime  int64          `json:"endTime,omitempty"` // ms; when the tool result arrived
}

// Item is one timeline entry. Kind is "user", "assistant", "compaction",
// "branch", "notice", "bash" or "custom"; each kind uses a subset of the
// fields (plan §6.2). Time is milliseconds since the epoch.
type Item struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Time int64  `json:"time"`

	Text   string `json:"text,omitempty"`   // user, notice, custom
	Images int    `json:"images,omitempty"` // user

	Provider   string     `json:"provider,omitempty"` // assistant
	Model      string     `json:"model,omitempty"`
	StopReason string     `json:"stopReason,omitempty"`
	Error      string     `json:"error,omitempty"`
	Usage      *ItemUsage `json:"usage,omitempty"`
	Blocks     []Block    `json:"blocks,omitempty"`

	TokensBefore int64  `json:"tokensBefore,omitempty"` // compaction
	Summary      string `json:"summary,omitempty"`      // compaction, branch

	Command  string `json:"command,omitempty"` // bash
	Output   string `json:"output,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`

	CustomType string `json:"customType,omitempty"` // custom
}

// PendingTurn is a live session's in-progress assistant message, rebuilt
// from stream events (plan §6.3).
type PendingTurn struct {
	Blocks    []Block `json:"blocks"`
	StartedAt int64   `json:"startedAt"`
}

// Timeline is a session's active branch as renderable items.
type Timeline struct {
	Items   []Item       `json:"items"`
	LeafID  string       `json:"leafId"`
	Pending *PendingTurn `json:"pending"`
}

// ContextUsage is the current context-window fill. Nil fields mean unknown
// (no window for a transcript read from disk, or right after compaction).
type ContextUsage struct {
	Tokens  *int64   `json:"tokens"`
	Window  *int64   `json:"window"`
	Percent *float64 `json:"percent"`
}

// FilesTouched lists paths read and modified by tool calls, deduplicated,
// in first-seen order.
type FilesTouched struct {
	Read     []string `json:"read"`
	Modified []string `json:"modified"`
}

// Stats summarises one session.
type Stats struct {
	UserMessages      int          `json:"userMessages"`
	AssistantMessages int          `json:"assistantMessages"`
	ToolCalls         int          `json:"toolCalls"`
	Tokens            Tokens       `json:"tokens"`
	Cost              float64      `json:"cost"`
	Context           ContextUsage `json:"context"`
	Files             FilesTouched `json:"files"`
	First             time.Time    `json:"first"`
	Last              time.Time    `json:"last"`
}

// ModelInfo is the subset of pi's Model object the panel shows.
type ModelInfo struct {
	Provider      string   `json:"provider"`
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	ContextWindow int64    `json:"contextWindow,omitempty"`
	MaxTokens     int64    `json:"maxTokens,omitempty"`
	Reasoning     bool     `json:"reasoning"`
	Input         []string `json:"input,omitempty"`
	Cost          *struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cacheRead"`
		CacheWrite float64 `json:"cacheWrite"`
	} `json:"cost,omitempty"`
}

// Ref is "provider/id", the form prefs and request bodies use.
func (m ModelInfo) Ref() string { return m.Provider + "/" + m.ID }

// Dialog is a pending pi extension-UI dialog (select, confirm, input,
// editor) waiting for the user (plan §5.3).
type Dialog struct {
	ID          string    `json:"id"`
	Method      string    `json:"method"`
	Title       string    `json:"title"`
	Message     string    `json:"message,omitempty"`
	Options     []string  `json:"options,omitempty"`
	Placeholder string    `json:"placeholder,omitempty"`
	Prefill     string    `json:"prefill,omitempty"`
	Deadline    time.Time `json:"deadline"`
}

// LogEntry is one line of phi agent serve's in-memory log (plan §5.8).
type LogEntry struct {
	Seq     int64     `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"` // debug | info | warn | error
	Session string    `json:"session,omitempty"`
	Source  string    `json:"source"` // serve | pi | broker | schedule
	Text    string    `json:"text"`
}
