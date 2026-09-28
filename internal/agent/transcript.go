package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// Parsing for pi's own JSONL session files (pi docs: session-format.md,
// message-types.md). phi never writes these — pi does, inside the
// containment — phi only reads them, alongside its own sidecar (chat.go).

// Message is one user or assistant turn, in the shape §7 promises the CLI
// and the HTTP API. Tool calls/results and thinking content are omitted;
// role is always "user" or "assistant".
type Message struct {
	Role  string `json:"role"`
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

// sessionEntry is the subset of pi's session-entry shapes phi reads. Every
// other entry type (usage, compaction, branch_summary, custom, label,
// model_change, thinking_level_change, context_edit) is ignored.
type sessionEntry struct {
	Type    string          `json:"type"`
	Name    string          `json:"name,omitempty"`    // session_info
	Message json.RawMessage `json:"message,omitempty"` // message
}

// piMessage is the subset of pi's AgentMessage phi needs. Role kinds other
// than "user" and "assistant" (system, toolResult, bashExecution, custom,
// branchSummary, compactionSummary) are read but produce no Message.
type piMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason,omitempty"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
}

// contentBlock is the subset of TextContent/ThinkingContent/ToolCall/
// ImageContent phi cares about: only "text" blocks contribute to Message.Text.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// parseTranscript reads a pi session JSONL stream (in file order — it does
// not resolve tree branches, so a forked session's abandoned branches are
// included too) and returns its user/assistant messages plus the latest
// session_info name, if any.
func parseTranscript(r io.Reader) (messages []Message, sessionName string, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e sessionEntry
		if jerr := json.Unmarshal([]byte(line), &e); jerr != nil {
			continue // tolerate a corrupt or partial trailing line
		}
		switch e.Type {
		case "session_info":
			if e.Name != "" {
				sessionName = e.Name
			}
		case "message":
			if msg, ok := messageFromPi(e.Message); ok {
				messages = append(messages, msg)
			}
		}
	}
	return messages, sessionName, sc.Err()
}

// messageFromPi converts one pi AgentMessage-shaped JSON value (the "message"
// field of a session-file entry, or one element of get_messages's RPC
// response) into a Message. ok is false for every role but "user" and
// "assistant" (system, toolResult, bashExecution, custom, branchSummary,
// compactionSummary), or if raw does not parse.
func messageFromPi(raw json.RawMessage) (msg Message, ok bool) {
	var pm piMessage
	if err := json.Unmarshal(raw, &pm); err != nil {
		return Message{}, false
	}
	switch pm.Role {
	case "user":
		return Message{Role: "user", Text: extractText(pm.Content)}, true
	case "assistant":
		msg := Message{Role: "assistant", Text: extractText(pm.Content)}
		if pm.StopReason == "error" {
			msg.Error = pm.ErrorMessage
			if msg.Error == "" {
				msg.Error = "error"
			}
		}
		return msg, true
	default:
		return Message{}, false
	}
}

// NormalizeAgentMessages converts a live pi session's get_messages response
// (data.messages, a []AgentMessage per rpc-commands.md) into phi's
// simplified Message shape, applying the exact same role/content rules
// parseTranscript uses for the on-disk JSONL (message-types.md documents
// both as the same AgentMessage union). Always non-nil.
func NormalizeAgentMessages(raws []json.RawMessage) []Message {
	out := []Message{}
	for _, raw := range raws {
		if msg, ok := messageFromPi(raw); ok {
			out = append(out, msg)
		}
	}
	return out
}

// extractText concatenates the text of every "text" content block. content
// may be a bare JSON string (a plain user message) or an array of content
// blocks (text/image/thinking/toolCall); non-text blocks are skipped.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}
