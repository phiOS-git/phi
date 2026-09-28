package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The live half of `phi agent serve`: how one pi RPC child's stdout events
// become the level-2 SSE vocabulary (plan §5.4), and the per-session state a
// client can fetch after connecting mid-turn — the in-progress message, the
// queue, pending dialogs, extension status, and the one-line activity.
//
// Text and thinking deltas arrive at token rate. They are coalesced per
// session and content block and flushed every deltaFlushInterval or at a
// block boundary, so a client repaints a few times a second instead of once
// per token, and the SSE fan-out never outruns a slow reader.

const (
	deltaFlushInterval   = 60 * time.Millisecond
	toolUpdateInterval   = 250 * time.Millisecond
	toolPartialTailChars = 2000
	toolResultCapChars   = 20000
	detailsCapBytes      = 64 * 1024
)

// liveTurnState is the part of liveSession the event handler maintains.
// Every field is guarded by liveSession.mu.
type liveTurnState struct {
	pending    *PendingTurn
	blockAt    map[int]int // pi contentIndex → index into pending.Blocks
	deltaBuf   map[int]*strings.Builder
	deltaKind  map[int]string // contentIndex → "text" | "thinking"
	flushArmed bool

	activity      string
	queueSteer    []string
	queueFollow   []string
	dialogs       map[string]*pendingDialog
	status        map[string]string
	widgets       map[string][]string
	retry         map[string]any
	compacting    bool
	thinkingLevel string
	failed        bool

	toolLastSent map[string]time.Time
}

type pendingDialog struct {
	Dialog
	timer *time.Timer
}

func newLiveTurnState() liveTurnState {
	return liveTurnState{
		blockAt:      map[int]int{},
		deltaBuf:     map[int]*strings.Builder{},
		deltaKind:    map[int]string{},
		dialogs:      map[string]*pendingDialog{},
		status:       map[string]string{},
		widgets:      map[string][]string{},
		toolLastSent: map[string]time.Time{},
	}
}

// setActivity changes the one-line activity and reports whether it changed.
// Caller holds ls.mu.
func (ls *liveSession) setActivityLocked(a string) bool {
	if ls.turn.activity == a {
		return false
	}
	ls.turn.activity = a
	return true
}

func (s *Server) publishActivity(ls *liveSession, a string) {
	s.hub.publish(sseEvent{"type": "session.activity", "session": ls.id, "activity": a})
}

// --- pi event mapping ----------------------------------------------------

// piEventHandler maps one pi stdout record (json.md, rpc-extension-ui.md)
// onto the SSE events of plan §5.4 and keeps the live state current.
func (s *Server) piEventHandler(ls *liveSession) EventFunc {
	return func(kind string, raw json.RawMessage) {
		ls.touch()
		switch kind {
		case "agent_start":
			ls.mu.Lock()
			ls.busy = true
			ls.turn.failed = false
			changed := ls.setActivityLocked("working")
			ls.mu.Unlock()
			s.hub.publish(sseEvent{"type": "session.busy", "session": ls.id})
			if changed {
				s.publishActivity(ls, "working")
			}

		case "turn_start":
			s.hub.publish(sseEvent{"type": "turn.start", "session": ls.id})

		case "turn_end":
			s.hub.publish(sseEvent{"type": "turn.end", "session": ls.id})
			go s.publishStats(ls)

		case "message_start":
			var ev struct {
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			if json.Unmarshal(raw, &ev) == nil && ev.Message.Role == "assistant" {
				ls.mu.Lock()
				ls.turn.pending = &PendingTurn{Blocks: []Block{}, StartedAt: time.Now().UnixMilli()}
				ls.turn.blockAt = map[int]int{}
				ls.mu.Unlock()
			}

		case "message_update":
			s.onMessageUpdate(ls, raw)

		case "message_end":
			s.onMessageEnd(ls, raw)

		case "tool_execution_start":
			var ev struct {
				ToolCallID string         `json:"toolCallId"`
				ToolName   string         `json:"toolName"`
				Args       map[string]any `json:"args"`
			}
			if json.Unmarshal(raw, &ev) != nil {
				return
			}
			summary := ToolSummary(ev.ToolName, ev.Args)
			act := strings.TrimSuffix(ev.ToolName+": "+summary, ": ")
			ls.mu.Lock()
			changed := ls.setActivityLocked(act)
			ls.mu.Unlock()
			s.hub.publish(sseEvent{"type": "tool.start", "session": ls.id, "callId": ev.ToolCallID,
				"name": ev.ToolName, "args": ev.Args, "summary": summary})
			if changed {
				s.publishActivity(ls, act)
			}

		case "tool_execution_update":
			var ev struct {
				ToolCallID    string          `json:"toolCallId"`
				ToolName      string          `json:"toolName"`
				PartialResult json.RawMessage `json:"partialResult"`
			}
			if json.Unmarshal(raw, &ev) != nil {
				return
			}
			now := time.Now()
			ls.mu.Lock()
			last := ls.turn.toolLastSent[ev.ToolCallID]
			throttled := now.Sub(last) < toolUpdateInterval
			if !throttled {
				ls.turn.toolLastSent[ev.ToolCallID] = now
			}
			ls.mu.Unlock()
			if throttled {
				return
			}
			text, details := toolResultParts(ev.ToolName, ev.PartialResult)
			partial := map[string]any{"text": tailRunes(text, toolPartialTailChars)}
			if details != nil {
				partial["details"] = details
			}
			s.hub.publish(sseEvent{"type": "tool.update", "session": ls.id, "callId": ev.ToolCallID,
				"name": ev.ToolName, "partial": partial})

		case "tool_execution_end":
			var ev struct {
				ToolCallID string          `json:"toolCallId"`
				ToolName   string          `json:"toolName"`
				Result     json.RawMessage `json:"result"`
				IsError    bool            `json:"isError"`
			}
			if json.Unmarshal(raw, &ev) != nil {
				return
			}
			text, details := toolResultParts(ev.ToolName, ev.Result)
			capped, truncated := capText(text, toolResultCapChars)
			result := map[string]any{"text": capped, "truncated": truncated}
			if details != nil {
				result["details"] = details
			}
			ls.mu.Lock()
			delete(ls.turn.toolLastSent, ev.ToolCallID)
			if pt := ls.turn.pending; pt != nil {
				for i := range pt.Blocks {
					if pt.Blocks[i].CallID == ev.ToolCallID {
						pt.Blocks[i].Result = &ToolResult{Text: capped, IsError: ev.IsError, Truncated: truncated, Details: details}
						pt.Blocks[i].EndTime = time.Now().UnixMilli()
					}
				}
			}
			changed := ls.setActivityLocked("working")
			ls.mu.Unlock()
			s.hub.publish(sseEvent{"type": "tool.end", "session": ls.id, "callId": ev.ToolCallID,
				"name": ev.ToolName, "isError": ev.IsError, "result": result})
			if changed {
				s.publishActivity(ls, "working")
			}
			if ev.IsError {
				s.logf("warn", ls.id, "pi", "tool %s failed: %s", ev.ToolName, firstLine(capped))
			}

		case "queue_update":
			var ev struct {
				Steering []string `json:"steering"`
				FollowUp []string `json:"followUp"`
			}
			if json.Unmarshal(raw, &ev) != nil {
				return
			}
			ls.mu.Lock()
			ls.turn.queueSteer = nonNil(ev.Steering)
			ls.turn.queueFollow = nonNil(ev.FollowUp)
			ls.mu.Unlock()
			s.hub.publish(sseEvent{"type": "queue", "session": ls.id,
				"steering": nonNil(ev.Steering), "followUp": nonNil(ev.FollowUp)})

		case "session_info_changed":
			var ev struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &ev) == nil {
				s.hub.publish(sseEvent{"type": "session.title", "session": ls.id, "title": ev.Name})
			}

		case "thinking_level_changed":
			var ev struct {
				Level string `json:"level"`
			}
			if json.Unmarshal(raw, &ev) == nil {
				ls.mu.Lock()
				ls.turn.thinkingLevel = ev.Level
				ls.mu.Unlock()
				s.hub.publish(sseEvent{"type": "thinking.level", "session": ls.id, "level": ev.Level})
			}

		case "compaction_start":
			var ev struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(raw, &ev)
			ls.mu.Lock()
			ls.turn.compacting = true
			changed := ls.setActivityLocked("compacting")
			ls.mu.Unlock()
			s.hub.publish(sseEvent{"type": "compaction.start", "session": ls.id, "reason": ev.Reason})
			if changed {
				s.publishActivity(ls, "compacting")
			}

		case "compaction_end":
			var ev struct {
				Reason string `json:"reason"`
				Result *struct {
					TokensBefore         int64 `json:"tokensBefore"`
					EstimatedTokensAfter int64 `json:"estimatedTokensAfter"`
				} `json:"result"`
				Aborted      bool   `json:"aborted"`
				ErrorMessage string `json:"errorMessage"`
			}
			_ = json.Unmarshal(raw, &ev)
			ls.mu.Lock()
			ls.turn.compacting = false
			busy := ls.busy
			act := ""
			if busy {
				act = "working"
			}
			changed := ls.setActivityLocked(act)
			ls.mu.Unlock()
			out := sseEvent{"type": "compaction.end", "session": ls.id, "reason": ev.Reason,
				"aborted": ev.Aborted, "error": ev.ErrorMessage}
			if ev.Result != nil {
				out["tokensBefore"] = ev.Result.TokensBefore
				out["tokensAfter"] = ev.Result.EstimatedTokensAfter
			}
			s.hub.publish(out)
			if changed {
				s.publishActivity(ls, act)
			}
			if ev.ErrorMessage != "" {
				s.logf("error", ls.id, "pi", "compaction failed: %s", ev.ErrorMessage)
			}

		case "auto_retry_start":
			var ev struct {
				Attempt      int    `json:"attempt"`
				MaxAttempts  int    `json:"maxAttempts"`
				DelayMs      int64  `json:"delayMs"`
				ErrorMessage string `json:"errorMessage"`
			}
			_ = json.Unmarshal(raw, &ev)
			act := fmt.Sprintf("retrying %d/%d", ev.Attempt, ev.MaxAttempts)
			ls.mu.Lock()
			ls.turn.retry = map[string]any{"attempt": ev.Attempt, "maxAttempts": ev.MaxAttempts,
				"delayMs": ev.DelayMs, "error": ev.ErrorMessage}
			changed := ls.setActivityLocked(act)
			ls.mu.Unlock()
			s.hub.publish(sseEvent{"type": "retry.start", "session": ls.id, "attempt": ev.Attempt,
				"maxAttempts": ev.MaxAttempts, "delayMs": ev.DelayMs, "error": ev.ErrorMessage})
			if changed {
				s.publishActivity(ls, act)
			}
			s.logf("warn", ls.id, "pi", "provider error, retrying %d/%d: %s", ev.Attempt, ev.MaxAttempts, ev.ErrorMessage)

		case "auto_retry_end":
			var ev struct {
				Success    bool   `json:"success"`
				Attempt    int    `json:"attempt"`
				FinalError string `json:"finalError"`
			}
			_ = json.Unmarshal(raw, &ev)
			ls.mu.Lock()
			ls.turn.retry = nil
			ls.mu.Unlock()
			s.hub.publish(sseEvent{"type": "retry.end", "session": ls.id, "success": ev.Success,
				"attempt": ev.Attempt, "error": ev.FinalError})

		case "agent_settled":
			s.flushDeltas(ls)
			ls.mu.Lock()
			ls.busy = false
			ls.lastActivity = time.Now()
			ls.turn.pending = nil
			ls.turn.retry = nil
			changed := ls.setActivityLocked("")
			failed := ls.turn.failed
			ls.mu.Unlock()
			if changed {
				s.publishActivity(ls, "")
			}
			s.hub.publish(sseEvent{"type": "session.idle", "session": ls.id})
			if s.onSettled != nil {
				go s.onSettled(ls, failed)
			}

		case "extension_ui_request":
			s.onExtensionUI(ls, raw)

		case "extension_error":
			var ev struct {
				ExtensionPath string `json:"extensionPath"`
				Event         string `json:"event"`
				Error         string `json:"error"`
			}
			if json.Unmarshal(raw, &ev) == nil {
				s.logf("error", ls.id, "pi", "extension %s (%s): %s", ev.ExtensionPath, ev.Event, ev.Error)
			}
		}
	}
}

func (s *Server) onMessageUpdate(ls *liveSession, raw json.RawMessage) {
	var ev struct {
		AssistantMessageEvent struct {
			Type         string          `json:"type"`
			ContentIndex int             `json:"contentIndex"`
			Delta        string          `json:"delta"`
			Content      json.RawMessage `json:"content"`
			ID           string          `json:"id"`
			ToolName     string          `json:"toolName"`
			ToolCall     *struct {
				ID        string         `json:"id"`
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"toolCall"`
		} `json:"assistantMessageEvent"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	ame := ev.AssistantMessageEvent
	idx := ame.ContentIndex

	switch ame.Type {
	case "text_start", "thinking_start":
		s.flushDeltas(ls)
		kind := strings.TrimSuffix(ame.Type, "_start")
		act := "writing"
		if kind == "thinking" {
			act = "thinking"
		}
		ls.mu.Lock()
		ls.ensurePendingLocked()
		ls.turn.blockAt[idx] = len(ls.turn.pending.Blocks)
		ls.turn.pending.Blocks = append(ls.turn.pending.Blocks, Block{Type: kind})
		changed := ls.setActivityLocked(act)
		ls.mu.Unlock()
		s.hub.publish(sseEvent{"type": "block.start", "session": ls.id, "index": idx, "block": kind})
		if changed {
			s.publishActivity(ls, act)
		}

	case "text_delta", "thinking_delta":
		kind := strings.TrimSuffix(ame.Type, "_delta")
		ls.mu.Lock()
		ls.ensurePendingLocked()
		if bi, ok := ls.turn.blockAt[idx]; ok {
			ls.turn.pending.Blocks[bi].Text += ame.Delta
		} else {
			ls.turn.blockAt[idx] = len(ls.turn.pending.Blocks)
			ls.turn.pending.Blocks = append(ls.turn.pending.Blocks, Block{Type: kind, Text: ame.Delta})
		}
		b := ls.turn.deltaBuf[idx]
		if b == nil {
			b = &strings.Builder{}
			ls.turn.deltaBuf[idx] = b
		}
		b.WriteString(ame.Delta)
		ls.turn.deltaKind[idx] = kind
		arm := !ls.turn.flushArmed
		ls.turn.flushArmed = true
		ls.mu.Unlock()
		if arm {
			time.AfterFunc(deltaFlushInterval, func() { s.flushDeltas(ls) })
		}

	case "text_end", "thinking_end":
		s.flushDeltas(ls)
		kind := strings.TrimSuffix(ame.Type, "_end")
		var final string
		if len(ame.Content) > 0 {
			if json.Unmarshal(ame.Content, &final) != nil {
				final = ""
			}
		}
		ls.mu.Lock()
		if bi, ok := ls.turn.blockAt[idx]; ok && ls.turn.pending != nil && final != "" {
			ls.turn.pending.Blocks[bi].Text = final
		}
		ls.mu.Unlock()
		out := sseEvent{"type": "block.end", "session": ls.id, "index": idx, "block": kind}
		if final != "" {
			out["text"] = final
		}
		s.hub.publish(out)

	case "toolcall_start":
		s.flushDeltas(ls)
		ls.mu.Lock()
		ls.ensurePendingLocked()
		ls.turn.blockAt[idx] = len(ls.turn.pending.Blocks)
		ls.turn.pending.Blocks = append(ls.turn.pending.Blocks, Block{Type: "tool", CallID: ame.ID, Name: ame.ToolName})
		ls.mu.Unlock()
		s.hub.publish(sseEvent{"type": "block.start", "session": ls.id, "index": idx, "block": "tool",
			"callId": ame.ID, "name": ame.ToolName})

	case "toolcall_end":
		if ame.ToolCall == nil {
			return
		}
		tc := ame.ToolCall
		blk := Block{Type: "tool", CallID: tc.ID, Name: tc.Name, Args: tc.Arguments, Summary: ToolSummary(tc.Name, tc.Arguments)}
		ls.mu.Lock()
		ls.ensurePendingLocked()
		if bi, ok := ls.turn.blockAt[idx]; ok {
			ls.turn.pending.Blocks[bi] = blk
		} else {
			ls.turn.blockAt[idx] = len(ls.turn.pending.Blocks)
			ls.turn.pending.Blocks = append(ls.turn.pending.Blocks, blk)
		}
		ls.mu.Unlock()
		s.hub.publish(sseEvent{"type": "block.end", "session": ls.id, "index": idx, "block": "tool", "tool": blk})
	}
}

func (ls *liveSession) ensurePendingLocked() {
	if ls.turn.pending == nil {
		ls.turn.pending = &PendingTurn{Blocks: []Block{}, StartedAt: time.Now().UnixMilli()}
		ls.turn.blockAt = map[int]int{}
	}
}

// flushDeltas publishes every buffered text/thinking delta, one event per
// content block, in index order.
func (s *Server) flushDeltas(ls *liveSession) {
	ls.mu.Lock()
	ls.turn.flushArmed = false
	if len(ls.turn.deltaBuf) == 0 {
		ls.mu.Unlock()
		return
	}
	idxs := make([]int, 0, len(ls.turn.deltaBuf))
	for i := range ls.turn.deltaBuf {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	type out struct {
		idx  int
		kind string
		text string
	}
	var outs []out
	for _, i := range idxs {
		if t := ls.turn.deltaBuf[i].String(); t != "" {
			outs = append(outs, out{i, ls.turn.deltaKind[i], t})
		}
	}
	ls.turn.deltaBuf = map[int]*strings.Builder{}
	ls.mu.Unlock()
	for _, o := range outs {
		typ := "message.delta"
		if o.kind == "thinking" {
			typ = "thinking.delta"
		}
		s.hub.publish(sseEvent{"type": typ, "session": ls.id, "index": o.idx, "text": o.text})
	}
}

func (s *Server) onMessageEnd(ls *liveSession, raw json.RawMessage) {
	var ev struct {
		Message struct {
			Role         string          `json:"role"`
			Content      json.RawMessage `json:"content"`
			StopReason   string          `json:"stopReason"`
			ErrorMessage string          `json:"errorMessage"`
			Provider     string          `json:"provider"`
			Model        string          `json:"model"`
			Usage        *piUsage        `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	m := ev.Message
	switch m.Role {
	case "user":
		s.hub.publish(sseEvent{"type": "message.user", "session": ls.id,
			"text": extractText(m.Content), "images": countImages(m.Content)})
	case "assistant":
		s.flushDeltas(ls)
		usage := ItemUsage{}
		if m.Usage != nil {
			usage = m.Usage.itemUsage()
		}
		out := sseEvent{"type": "message.done", "session": ls.id, "usage": usage,
			"stopReason": m.StopReason, "model": m.Model, "provider": m.Provider}
		failed := m.StopReason == "error"
		if failed {
			errText := m.ErrorMessage
			if errText == "" {
				errText = "error"
			}
			out["error"] = errText
		}
		ls.mu.Lock()
		// The message is now part of the session's entries; what is still
		// pending is only its tool executions, which get_entries shows as
		// tool blocks without a result.
		ls.turn.pending = nil
		if failed {
			ls.turn.failed = true
		}
		ls.mu.Unlock()
		s.hub.publish(out)
		if failed {
			s.hub.publish(sseEvent{"type": "session.error", "session": ls.id, "error": out["error"]})
			s.logf("error", ls.id, "pi", "turn failed: %v", out["error"])
		}
	}
}

// --- extension UI ------------------------------------------------------

// onExtensionUI forwards pi's extension-UI requests (rpc-extension-ui.md).
// Dialogs wait for the panel until their own timeout or the server's
// dialog timeout, then are cancelled, so a closed panel never blocks a
// session forever.
func (s *Server) onExtensionUI(ls *liveSession, raw json.RawMessage) {
	var req struct {
		ID          string   `json:"id"`
		Method      string   `json:"method"`
		Title       string   `json:"title"`
		Message     string   `json:"message"`
		Options     []string `json:"options"`
		Placeholder string   `json:"placeholder"`
		Prefill     string   `json:"prefill"`
		Timeout     int64    `json:"timeout"`
		NotifyType  string   `json:"notifyType"`
		StatusKey   string   `json:"statusKey"`
		StatusText  *string  `json:"statusText"`
		WidgetKey   string   `json:"widgetKey"`
		WidgetLines []string `json:"widgetLines"`
	}
	if json.Unmarshal(raw, &req) != nil || req.ID == "" {
		return
	}
	switch req.Method {
	case "select", "confirm", "input", "editor":
		timeout := s.dialogTimeout()
		if req.Timeout > 0 {
			timeout = time.Duration(req.Timeout) * time.Millisecond
		}
		d := &pendingDialog{Dialog: Dialog{ID: req.ID, Method: req.Method, Title: req.Title, Message: req.Message,
			Options: req.Options, Placeholder: req.Placeholder, Prefill: req.Prefill, Deadline: time.Now().Add(timeout).UTC()}}
		d.timer = time.AfterFunc(timeout, func() { s.resolveDialog(ls, req.ID, map[string]any{"cancelled": true}, "timeout") })
		ls.mu.Lock()
		ls.turn.dialogs[req.ID] = d
		changed := ls.setActivityLocked("waiting for you")
		ls.mu.Unlock()
		s.hub.publish(sseEvent{"type": "dialog.open", "session": ls.id, "dialog": d.Dialog})
		if changed {
			s.publishActivity(ls, "waiting for you")
		}
	case "notify":
		s.hub.publish(sseEvent{"type": "ext.notify", "session": ls.id, "level": orString(req.NotifyType, "info"), "message": req.Message})
	case "setStatus":
		text := ""
		if req.StatusText != nil {
			text = *req.StatusText
		}
		ls.mu.Lock()
		if text == "" {
			delete(ls.turn.status, req.StatusKey)
		} else {
			ls.turn.status[req.StatusKey] = text
		}
		ls.mu.Unlock()
		s.hub.publish(sseEvent{"type": "ext.status", "session": ls.id, "key": req.StatusKey, "text": text})
	case "setWidget":
		ls.mu.Lock()
		if len(req.WidgetLines) == 0 {
			delete(ls.turn.widgets, req.WidgetKey)
		} else {
			ls.turn.widgets[req.WidgetKey] = req.WidgetLines
		}
		ls.mu.Unlock()
		s.hub.publish(sseEvent{"type": "ext.widget", "session": ls.id, "key": req.WidgetKey, "lines": nonNil(req.WidgetLines)})
	}
}

// resolveDialog answers a pending dialog once: the panel's answer, a
// timeout, or the session closing. It reports false when the dialog was
// already resolved.
func (s *Server) resolveDialog(ls *liveSession, id string, answer map[string]any, reason string) bool {
	ls.mu.Lock()
	d, ok := ls.turn.dialogs[id]
	if ok {
		delete(ls.turn.dialogs, id)
	}
	remaining := len(ls.turn.dialogs)
	busy := ls.busy
	ls.mu.Unlock()
	if !ok {
		return false
	}
	d.timer.Stop()
	resp := map[string]any{"type": "extension_ui_response", "id": id}
	for k, v := range answer {
		resp[k] = v
	}
	if ls.proc != nil {
		_ = ls.proc.sendRaw(resp)
	}
	s.hub.publish(sseEvent{"type": "dialog.close", "session": ls.id, "id": id, "reason": reason})
	if remaining == 0 {
		act := ""
		if busy {
			act = "working"
		}
		ls.mu.Lock()
		changed := ls.setActivityLocked(act)
		ls.mu.Unlock()
		if changed {
			s.publishActivity(ls, act)
		}
	}
	if reason == "timeout" {
		s.logf("warn", ls.id, "serve", "dialog %q timed out and was cancelled", d.Title)
	}
	return true
}

// cancelDialogs resolves every pending dialog of a closing session.
func (s *Server) cancelDialogs(ls *liveSession) {
	ls.mu.Lock()
	ids := make([]string, 0, len(ls.turn.dialogs))
	for id := range ls.turn.dialogs {
		ids = append(ids, id)
	}
	ls.mu.Unlock()
	for _, id := range ids {
		s.resolveDialog(ls, id, map[string]any{"cancelled": true}, "cancelled")
	}
}

// --- helpers ---------------------------------------------------------------

// toolResultParts extracts the text of a pi tool result ({content, details})
// and, for the tools the panel renders specially, its details.
func toolResultParts(toolName string, raw json.RawMessage) (string, any) {
	if len(raw) == 0 {
		return "", nil
	}
	var r struct {
		Content json.RawMessage `json:"content"`
		Details json.RawMessage `json:"details"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return "", nil
	}
	text := extractText(r.Content)
	var details any
	if keepDetails(toolName) && len(r.Details) > 0 && len(r.Details) <= detailsCapBytes {
		_ = json.Unmarshal(r.Details, &details)
	}
	return text, details
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
