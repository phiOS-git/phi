package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// --- fake pi (TestHelperProcess pattern, os/exec_test.go's own idiom) -----
//
// fakePiLaunch is the injected Server.Launch: instead of BuildLaunch's real
// phi-agent-contain argv, it re-execs THIS test binary with
// -test.run=TestFakePi. That re-exec only behaves as a fake pi when
// PHI_AGENT_TEST_FAKE_PI=1 is in its environment (set by newTestServer, via
// t.Setenv, for the lifetime of one top-level test) — an ordinary `go test`
// run invokes TestFakePi too, sees the variable unset, and returns
// immediately as a no-op test.

func fakePiLaunch(spec LaunchSpec) ([]string, error) {
	return []string{os.Args[0], "-test.run=TestFakePi", "--"}, nil
}

// TestFakePi is not a real test: run normally it is a no-op. Spawned by
// fakePiLaunch (env var set) it acts as a minimal `pi --mode rpc` speaking
// just enough of the protocol (rpc.md, rpc-commands.md, json.md,
// rpc-extension-ui.md) for serve_test.go's scenarios.
func TestFakePi(t *testing.T) {
	if os.Getenv("PHI_AGENT_TEST_FAKE_PI") != "1" {
		return
	}
	runFakePi()
	os.Exit(0)
}

func runFakePi() {
	r := bufio.NewReaderSize(os.Stdin, 1<<20)
	write := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		b = append(b, '\n')
		os.Stdout.Write(b)
	}
	// next reads one JSONL record, per rpc.md's framing: split only on '\n',
	// trailing '\r' stripped. A nil map with a non-nil error means stdin
	// closed (orderly shutdown, §8).
	next := func() (map[string]any, error) {
		line, err := r.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			return nil, err
		}
		var m map[string]any
		_ = json.Unmarshal(line, &m)
		return m, err
	}
	finishTurn := func(text string) {
		write(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": text}})
		write(map[string]any{"type": "message_end", "message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "stopReason": "stop",
		}})
		write(map[string]any{"type": "agent_end", "messages": []any{}, "willRetry": false})
		write(map[string]any{"type": "agent_settled"})
	}

	hanging := false

	for {
		cmd, err := next()
		if cmd != nil {
			id, _ := cmd["id"].(string)
			typ, _ := cmd["type"].(string)
			switch typ {
			case "prompt":
				msg, _ := cmd["message"].(string)
				sb, _ := cmd["streamingBehavior"].(string)

				if hanging {
					// A follow-up while busy (§8: "prompt while busy -> add
					// streamingBehavior:followUp"). Verify the flag and end
					// the hung turn.
					hanging = false
					write(map[string]any{"type": "response", "id": id, "command": "prompt", "success": true})
					if sb == "followUp" {
						finishTurn("followup-ok")
					} else {
						finishTurn("followup-missing:" + sb)
					}
					continue
				}

				write(map[string]any{"type": "response", "id": id, "command": "prompt", "success": true})
				write(map[string]any{"type": "agent_start"})
				switch msg {
				case "hang":
					hanging = true // stays "busy" until a follow-up or an abort arrives
				case "fail":
					write(map[string]any{"type": "message_end", "message": map[string]any{
						"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": "boom",
					}})
					write(map[string]any{"type": "agent_end", "messages": []any{}, "willRetry": false})
					write(map[string]any{"type": "agent_settled"})
				case "dialog", "dialog-timeout":
					write(map[string]any{"type": "extension_ui_request", "id": "ui1", "method": "confirm", "title": "ok?", "message": "proceed?"})
					reply, _ := next()
					text := "dialog-mismatch"
					if reply != nil {
						rt, _ := reply["type"].(string)
						rid, _ := reply["id"].(string)
						cancelled, _ := reply["cancelled"].(bool)
						confirmed, _ := reply["confirmed"].(bool)
						if rt == "extension_ui_response" && rid == "ui1" {
							if msg == "dialog" && confirmed {
								text = "dialog-ok"
							}
							if msg == "dialog-timeout" && cancelled {
								text = "dialog-timeout-ok"
							}
						}
					}
					finishTurn(text)
				case "rich":
					richTurn(write)
				default:
					write(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "Hello "}})
					write(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "world"}})
					write(map[string]any{"type": "message_end", "message": map[string]any{
						"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Hello world"}}, "stopReason": "stop",
					}})
					write(map[string]any{"type": "agent_end", "messages": []any{}, "willRetry": false})
					write(map[string]any{"type": "agent_settled"})
				}

			case "get_messages":
				write(map[string]any{"type": "response", "id": id, "command": "get_messages", "success": true, "data": map[string]any{
					"messages": []any{
						map[string]any{"role": "user", "content": "hi"},
						map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "hello"}}},
					},
				}})

			case "abort":
				if hanging {
					hanging = false
					write(map[string]any{"type": "response", "id": id, "command": "abort", "success": true})
					finishTurn("aborted")
				} else {
					write(map[string]any{"type": "response", "id": id, "command": "abort", "success": false, "error": "nothing to abort"})
				}

			case "clear_queue":
				write(map[string]any{"type": "response", "id": id, "command": "clear_queue", "success": true,
					"data": map[string]any{"steering": []any{"steer me"}, "followUp": []any{}}})

			case "get_entries":
				write(map[string]any{"type": "response", "id": id, "command": "get_entries", "success": true,
					"data": map[string]any{"entries": fakeEntries(), "leafId": "e3"}})

			case "get_session_stats":
				write(map[string]any{"type": "response", "id": id, "command": "get_session_stats", "success": true,
					"data": map[string]any{"tokens": map[string]any{"input": 100, "output": 20, "cacheRead": 0, "cacheWrite": 0, "total": 120},
						"cost": 0.5, "contextUsage": map[string]any{"tokens": 1000, "contextWindow": 10000, "percent": 10}}})

			case "get_state":
				write(map[string]any{"type": "response", "id": id, "command": "get_state", "success": true,
					"data": map[string]any{"model": map[string]any{"provider": "p", "id": "m", "contextWindow": 10000, "reasoning": true}, "thinkingLevel": "medium"}})

			case "get_available_thinking_levels":
				write(map[string]any{"type": "response", "id": id, "command": typ, "success": true,
					"data": map[string]any{"levels": []any{"off", "low", "medium"}}})

			case "set_session_name":
				write(map[string]any{"type": "response", "id": id, "command": "set_session_name", "success": true})

			default:
				write(map[string]any{"type": "response", "id": id, "command": typ, "success": false, "error": "fake pi: unknown command"})
			}
		}
		if err != nil {
			return // stdin closed: orderly shutdown
		}
	}
}

// --- test harness ---------------------------------------------------------

// newTestServer returns a Server wired to fakePiLaunch, with the fake-pi
// switch set for the caller's whole test (t.Setenv, restored on cleanup).
func newTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("PHI_AGENT_TEST_FAKE_PI", "1")
	srv := NewServer(testModel(t))
	srv.Launch = fakePiLaunch
	srv.Stderr = io.Discard
	t.Cleanup(srv.closeAllSessions)
	return srv
}

// sseClient reads a subscribed /events response and decodes each `data:`
// line into a map, off the test's critical path.
type sseClient struct {
	body   io.ReadCloser
	events chan map[string]any
}

func subscribeSSE(t *testing.T, base string) *sseClient {
	t.Helper()
	resp, err := http.Get(base + "/events")
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events: status %d", resp.StatusCode)
	}
	c := &sseClient{body: resp.Body, events: make(chan map[string]any, 64)}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err == nil {
				c.events <- ev
			}
		}
		close(c.events)
	}()
	t.Cleanup(func() { c.body.Close() })
	return c
}

func (c *sseClient) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case ev, ok := <-c.events:
		if !ok {
			t.Fatal("sse: stream closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("sse: timed out waiting for an event")
		return nil
	}
}

// waitFor filters out events that don't match, for scenarios (idle reaper,
// a follow-up while busy, …) where unrelated events may interleave.
func (c *sseClient) waitFor(t *testing.T, match func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				t.Fatal("sse: stream closed")
			}
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("sse: timed out waiting for a matching event")
			return nil
		}
	}
}

func createSession(t *testing.T, base, profile, project string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"profile": profile, "project": project})
	resp, err := http.Post(base+"/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /sessions: status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func doJSON(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func getMessages(t *testing.T, base, id string) []Message {
	t.Helper()
	resp, err := http.Get(base + "/sessions/" + id + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET messages: status %d: %s", resp.StatusCode, b)
	}
	var msgs []Message
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		t.Fatal(err)
	}
	return msgs
}

// --- scenarios --------------------------------------------------------

func TestServeCreatePromptSSEOrder(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sse := subscribeSSE(t, ts.URL)

	id := createSession(t, ts.URL, "general", "")
	ev := sse.next(t)
	if ev["type"] != "session.created" || ev["session"] != id {
		t.Fatalf("1st event = %+v, want session.created for %s", ev, id)
	}

	resp := doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/prompt", map[string]string{"text": "hello"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST prompt: status %d", resp.StatusCode)
	}

	// session.activity and turn events interleave; deltas are coalesced,
	// so the text may arrive in one event or several.
	var order []string
	text := ""
	for len(order) == 0 || order[len(order)-1] != "session.idle" {
		ev := sse.next(t)
		typ, _ := ev["type"].(string)
		switch typ {
		case "session.busy", "message.done", "session.idle":
			order = append(order, typ)
		case "message.delta":
			if len(order) == 0 || order[len(order)-1] != "message.delta" {
				order = append(order, typ)
			}
			text += ev["text"].(string)
		}
	}
	want := []string{"session.busy", "message.delta", "message.done", "session.idle"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("event order = %v, want %v", order, want)
	}
	if text != "Hello world" {
		t.Fatalf("streamed text = %q, want %q", text, "Hello world")
	}
}

func TestServeMessagesLive(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	id := createSession(t, ts.URL, "general", "")
	msgs := getMessages(t, ts.URL, id)
	want := []Message{{Role: "user", Text: "hi"}, {Role: "assistant", Text: "hello"}}
	if len(msgs) != len(want) {
		t.Fatalf("messages = %+v, want %+v", msgs, want)
	}
	for i := range want {
		if msgs[i] != want[i] {
			t.Errorf("message %d = %+v, want %+v", i, msgs[i], want[i])
		}
	}
}

func TestServeFollowUpWhileBusy(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sse := subscribeSSE(t, ts.URL)
	id := createSession(t, ts.URL, "general", "")
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.created" })

	resp := doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/prompt", map[string]string{"text": "hang"})
	resp.Body.Close()
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.busy" && ev["session"] == id })

	resp = doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/prompt", map[string]string{"text": "second"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST follow-up prompt: status %d", resp.StatusCode)
	}

	ev := sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "message.delta" })
	if ev["text"] != "followup-ok" {
		t.Fatalf("follow-up delta text = %q, want %q (server did not set streamingBehavior:followUp)", ev["text"], "followup-ok")
	}
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.idle" })
}

func TestServeAbort(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sse := subscribeSSE(t, ts.URL)
	id := createSession(t, ts.URL, "general", "")
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.created" })

	// Abort with nothing running: a failed pi command response, mapped to
	// session.error (§8) and a 502 to the caller.
	resp := doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/abort", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("POST abort (idle): status %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
	ev := sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.error" })
	if ev["error"] != "nothing to abort" {
		t.Fatalf("session.error = %+v, want error %q", ev, "nothing to abort")
	}

	// Now abort a running turn.
	resp = doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/prompt", map[string]string{"text": "hang"})
	resp.Body.Close()
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.busy" })

	resp = doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/abort", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST abort (busy): status %d", resp.StatusCode)
	}
	ev = sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "message.delta" })
	if ev["text"] != "aborted" {
		t.Fatalf("abort delta text = %q, want %q", ev["text"], "aborted")
	}
}

func TestServeDeleteSessionExited(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sse := subscribeSSE(t, ts.URL)
	id := createSession(t, ts.URL, "general", "")
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.created" })

	resp := doJSON(t, http.MethodDelete, ts.URL+"/sessions/"+id, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE: status %d", resp.StatusCode)
	}

	ev := sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.exited" && ev["session"] == id })
	if code, _ := ev["code"].(float64); code != 0 {
		t.Fatalf("session.exited code = %v, want 0", ev["code"])
	}
}

func TestServeIdleReaper(t *testing.T) {
	srv := newTestServer(t)
	srv.IdleTimeout = 50 * time.Millisecond
	srv.ReapInterval = 10 * time.Millisecond
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	stopReaper := srv.startReaper()
	t.Cleanup(stopReaper)

	sse := subscribeSSE(t, ts.URL)
	id := createSession(t, ts.URL, "general", "")

	ev := sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.exited" && ev["session"] == id })
	if code, _ := ev["code"].(float64); code != 0 {
		t.Fatalf("reaped session.exited code = %v, want 0", ev["code"])
	}
}

func TestServeSessionListIncludesSidecarOnlyLive(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	id := createSession(t, ts.URL, "general", "")

	resp, err := http.Get(ts.URL + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []sessionOut
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	var found *sessionOut
	for i := range list {
		if list[i].ID == id {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatalf("session %s not in GET /sessions: %+v", id, list)
	}
	if !found.Live {
		t.Errorf("session %s: live = false, want true (has a sidecar but no .jsonl yet)", id)
	}
	if found.Busy {
		t.Errorf("session %s: busy = true, want false", id)
	}
	if found.Profile != "general" {
		t.Errorf("session %s: profile = %q, want general", id, found.Profile)
	}
}

func TestServeExtensionUIDialogAnswered(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sse := subscribeSSE(t, ts.URL)
	id := createSession(t, ts.URL, "general", "")
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.created" })

	resp := doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/prompt", map[string]string{"text": "dialog"})
	resp.Body.Close()

	ev := sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "dialog.open" })
	d, _ := ev["dialog"].(map[string]any)
	if d == nil || d["id"] != "ui1" || d["method"] != "confirm" {
		t.Fatalf("dialog.open = %+v", ev)
	}
	resp = doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/dialog/ui1", map[string]any{"confirmed": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST dialog: status %d", resp.StatusCode)
	}
	ev = sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "message.delta" })
	if ev["text"] != "dialog-ok" {
		t.Fatalf("dialog delta text = %q, want %q", ev["text"], "dialog-ok")
	}
}

func TestServeErrorMapping(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sse := subscribeSSE(t, ts.URL)
	id := createSession(t, ts.URL, "general", "")
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.created" })

	resp := doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/prompt", map[string]string{"text": "fail"})
	resp.Body.Close()

	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "message.done" })
	ev := sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.error" })
	if ev["error"] != "boom" {
		t.Fatalf("session.error = %+v, want error %q", ev, "boom")
	}
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.idle" })
}

func TestLoopbackListenerRefusesNonLoopback(t *testing.T) {
	_, err := LoopbackListener{Addr: "0.0.0.0:0"}.Listen()
	if err == nil {
		t.Fatal("Listen on 0.0.0.0: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "D-PI-14") {
		t.Errorf("error = %q, want it to name D-PI-14", err.Error())
	}
}

// richTurn emits one turn exercising the level-2 mapping: thinking, text,
// a tool call with its execution, a queue change and usage.
func richTurn(write func(any)) {
	upd := func(ev map[string]any) {
		write(map[string]any{"type": "message_update", "assistantMessageEvent": ev})
	}
	write(map[string]any{"type": "turn_start"})
	write(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
	upd(map[string]any{"type": "thinking_start", "contentIndex": 0})
	upd(map[string]any{"type": "thinking_delta", "contentIndex": 0, "delta": "let me "})
	upd(map[string]any{"type": "thinking_delta", "contentIndex": 0, "delta": "think"})
	upd(map[string]any{"type": "thinking_end", "contentIndex": 0, "content": "let me think"})
	upd(map[string]any{"type": "toolcall_start", "contentIndex": 1, "id": "c1", "toolName": "read"})
	upd(map[string]any{"type": "toolcall_end", "contentIndex": 1, "toolCall": map[string]any{"id": "c1", "name": "read", "arguments": map[string]any{"path": "a.go"}}})
	write(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "toolUse",
		"provider": "p", "model": "m", "usage": map[string]any{"input": 100, "output": 20, "totalTokens": 120, "cost": map[string]any{"total": 0.5}}}})
	write(map[string]any{"type": "tool_execution_start", "toolCallId": "c1", "toolName": "read", "args": map[string]any{"path": "a.go"}})
	write(map[string]any{"type": "tool_execution_end", "toolCallId": "c1", "toolName": "read",
		"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "package a"}}}, "isError": false})
	write(map[string]any{"type": "queue_update", "steering": []any{}, "followUp": []any{"later"}})
	write(map[string]any{"type": "turn_end"})
	write(map[string]any{"type": "agent_end", "messages": []any{}, "willRetry": false})
	write(map[string]any{"type": "agent_settled"})
}

// fakeEntries is a three-entry session: user, assistant with a tool call,
// and the tool result.
func fakeEntries() []any {
	return []any{
		map[string]any{"type": "message", "id": "e1", "parentId": nil, "timestamp": "2026-09-28T10:00:00Z",
			"message": map[string]any{"role": "user", "content": "read a.go", "timestamp": 1}},
		map[string]any{"type": "message", "id": "e2", "parentId": "e1", "timestamp": "2026-09-28T10:00:01Z",
			"message": map[string]any{"role": "assistant", "provider": "p", "model": "m", "stopReason": "toolUse", "timestamp": 2,
				"usage":   map[string]any{"input": 100, "output": 20, "totalTokens": 120, "cost": map[string]any{"total": 0.5}},
				"content": []any{map[string]any{"type": "toolCall", "id": "c1", "name": "read", "arguments": map[string]any{"path": "a.go"}}}}},
		map[string]any{"type": "message", "id": "e3", "parentId": "e2", "timestamp": "2026-09-28T10:00:02Z",
			"message": map[string]any{"role": "toolResult", "toolCallId": "c1", "toolName": "read", "timestamp": 3,
				"content": []any{map[string]any{"type": "text", "text": "package a"}}, "isError": false}},
	}
}

func TestServeRichEvents(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sse := subscribeSSE(t, ts.URL)
	id := createSession(t, ts.URL, "general", "")
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.created" })

	resp := doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/prompt", map[string]string{"text": "rich"})
	resp.Body.Close()

	ev := sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "block.start" && ev["block"] == "thinking" })
	ev = sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "thinking.delta" })
	if ev["text"] != "let me think" {
		t.Errorf("coalesced thinking.delta = %q, want %q", ev["text"], "let me think")
	}
	ev = sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "block.end" && ev["block"] == "tool" })
	if tool, _ := ev["tool"].(map[string]any); tool == nil || tool["summary"] != "a.go" {
		t.Errorf("block.end tool = %+v, want summary a.go", ev["tool"])
	}
	ev = sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "message.done" })
	if u, _ := ev["usage"].(map[string]any); u == nil || u["cost"] != 0.5 {
		t.Errorf("message.done usage = %+v, want cost 0.5", ev["usage"])
	}
	ev = sse.waitFor(t, func(ev map[string]any) bool {
		return ev["type"] == "session.activity" && ev["activity"] == "read: a.go"
	})
	ev = sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "tool.end" })
	if r, _ := ev["result"].(map[string]any); r == nil || r["text"] != "package a" {
		t.Errorf("tool.end result = %+v", ev["result"])
	}
	ev = sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "queue" })
	if fu, _ := ev["followUp"].([]any); len(fu) != 1 {
		t.Errorf("queue followUp = %+v", ev["followUp"])
	}
	// session.stats is computed after turn_end in its own goroutine, so it
	// may arrive on either side of session.idle.
	sawStats, sawIdle := false, false
	for !sawStats || !sawIdle {
		ev = sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.stats" || ev["type"] == "session.idle" })
		if ev["type"] == "session.idle" {
			sawIdle = true
			continue
		}
		sawStats = true
		if st, _ := ev["stats"].(map[string]any); st == nil || st["cost"] != 0.5 || st["toolCalls"] != float64(1) {
			t.Errorf("session.stats = %+v", ev["stats"])
		}
	}

	resp, err := http.Get(ts.URL + "/sessions/" + id + "/timeline")
	if err != nil {
		t.Fatal(err)
	}
	var tl Timeline
	_ = json.NewDecoder(resp.Body).Decode(&tl)
	resp.Body.Close()
	if len(tl.Items) != 2 || tl.Items[1].Blocks[0].Result == nil || tl.Items[1].Blocks[0].Result.Text != "package a" {
		t.Fatalf("timeline = %+v", tl)
	}

	resp, err = http.Get(ts.URL + "/sessions/" + id + "/state")
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st["live"] != true || st["thinkingLevel"] != "medium" {
		t.Fatalf("state = %+v", st)
	}
	if m, _ := st["model"].(map[string]any); m == nil || m["id"] != "m" {
		t.Errorf("state model = %+v", st["model"])
	}
}

func TestServeDialogTimeout(t *testing.T) {
	srv := newTestServer(t)
	srv.DialogTimeout = 100 * time.Millisecond
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sse := subscribeSSE(t, ts.URL)
	id := createSession(t, ts.URL, "general", "")
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.created" })

	resp := doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/prompt", map[string]string{"text": "dialog-timeout"})
	resp.Body.Close()
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "dialog.open" })
	ev := sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "dialog.close" })
	if ev["reason"] != "timeout" {
		t.Fatalf("dialog.close = %+v, want reason timeout", ev)
	}
	ev = sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "message.delta" })
	if ev["text"] != "dialog-timeout-ok" {
		t.Fatalf("delta = %q, want dialog-timeout-ok", ev["text"])
	}
}
