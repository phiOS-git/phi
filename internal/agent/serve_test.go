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
				case "dialog":
					write(map[string]any{"type": "extension_ui_request", "id": "ui1", "method": "confirm", "title": "ok?", "message": "proceed?"})
					reply, _ := next()
					text := "dialog-mismatch"
					if reply != nil {
						rt, _ := reply["type"].(string)
						rid, _ := reply["id"].(string)
						cancelled, _ := reply["cancelled"].(bool)
						if rt == "extension_ui_response" && rid == "ui1" && cancelled {
							text = "dialog-ok"
						}
					}
					finishTurn(text)
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

	wantSeq := []string{"session.busy", "message.delta", "message.delta", "message.done", "session.idle"}
	for _, want := range wantSeq {
		ev := sse.next(t)
		if ev["type"] != want {
			t.Fatalf("event = %+v, want type %q", ev, want)
		}
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

func TestServeExtensionUIDialogCancelled(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sse := subscribeSSE(t, ts.URL)
	id := createSession(t, ts.URL, "general", "")
	sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "session.created" })

	resp := doJSON(t, http.MethodPost, ts.URL+"/sessions/"+id+"/prompt", map[string]string{"text": "dialog"})
	resp.Body.Close()

	ev := sse.waitFor(t, func(ev map[string]any) bool { return ev["type"] == "message.delta" })
	if ev["text"] != "dialog-ok" {
		t.Fatalf("dialog delta text = %q, want %q (server did not auto-reply cancelled:true)", ev["text"], "dialog-ok")
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
