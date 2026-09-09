package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockOpencode is a minimal stand-in for `opencode serve`: enough of
// /global/health, POST /session, POST /session/:id/message and
// DELETE /session/:id for `phi agent ask`.
func mockOpencode(t *testing.T) (*httptest.Server, *mockState) {
	t.Helper()
	st := &mockState{sessions: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"healthy":true,"version":"test"}`)
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.created++
		id := "ses_test"
		st.sessions[id] = true
		json.NewEncoder(w).Encode(map[string]any{"id": id, "title": "inline (ephemeral)"})
	})
	mux.HandleFunc("/session/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/session/")
		switch {
		case r.Method == http.MethodDelete:
			st.mu.Lock()
			delete(st.sessions, strings.TrimSuffix(id, "/"))
			st.deleted++
			st.mu.Unlock()
			io.WriteString(w, "true")
		case strings.HasSuffix(id, "/message"):
			var body struct {
				Agent string `json:"agent"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			st.mu.Lock()
			st.lastAgent = body.Agent
			st.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"info": map[string]any{"role": "assistant"},
				"parts": []map[string]any{
					{"type": "text", "text": "answer to: " + body.Parts[0].Text},
				},
			})
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, st
}

type mockState struct {
	mu        sync.Mutex
	sessions  map[string]bool
	created   int
	deleted   int
	lastAgent string
}

func TestAskCreatesUsesAndDeletesSession(t *testing.T) {
	srv, st := mockOpencode(t)

	var out strings.Builder
	err := Ask(context.Background(), AskConfig{BaseURL: srv.URL, Personality: "technical"}, "what is 2+2", &out)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "answer to: what is 2+2" {
		t.Errorf("reply = %q", got)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.created != 1 || st.deleted != 1 {
		t.Errorf("created=%d deleted=%d, want 1/1 — the session must not linger (§10.2, V-15)", st.created, st.deleted)
	}
	if len(st.sessions) != 0 {
		t.Errorf("session left behind: %v", st.sessions)
	}
	if st.lastAgent != "technical" {
		t.Errorf("agent = %q, want technical", st.lastAgent)
	}
}

func TestAskFailsClosedWhenServiceDown(t *testing.T) {
	// A port nothing listens on.
	err := Ask(context.Background(), AskConfig{BaseURL: "http://127.0.0.1:1"}, "hi", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("want a clear 'not reachable' error, got %v", err)
	}
}

func TestAskDeletesSessionOnMessageError(t *testing.T) {
	st := &mockState{sessions: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "{}") })
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.created++
		st.sessions["ses_x"] = true
		st.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": "ses_x"})
	})
	mux.HandleFunc("/session/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			st.mu.Lock()
			delete(st.sessions, "ses_x")
			st.deleted++
			st.mu.Unlock()
			io.WriteString(w, "true")
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := Ask(context.Background(), AskConfig{BaseURL: srv.URL}, "hi", io.Discard)
	if err == nil {
		t.Fatal("expected an error from the failing message endpoint")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.deleted != 1 {
		t.Errorf("session not cleaned up after error: deleted=%d", st.deleted)
	}
}
