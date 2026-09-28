package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"phi/internal/build"
)

// `phi agent serve` — the one agent API (binding contract §8, D-PI-13). One
// `pi --mode rpc` child per live session, spawned through the containment
// (BuildLaunch, launch.go); a loopback-only HTTP+SSE front end; sessions
// idle for 15 minutes are closed (their transcript stays — session.go
// respawns on the next prompt, resuming from the .jsonl).
//
// Only the two chat profiles (ChatProfiles: general, academic) are served;
// coding and inline have no panel chat (§1).

// defaultIdleTimeout / defaultReapInterval are §8's "idle for 15 minutes is
// closed". Server fields, not constants, so tests can shrink them.
const (
	defaultIdleTimeout  = 15 * time.Minute
	defaultReapInterval = time.Minute
)

// Listener produces the net.Listener(s) a Server accepts connections on.
// This is an abstraction, not a single implementation, so that a second,
// authenticated tailnet listener (D-PI-14) can be added later without
// reshaping Server — it is explicitly out of scope here (§10) and
// LoopbackListener is the only implementation.
type Listener interface {
	Listen() (net.Listener, error)
}

// LoopbackListener binds Addr after refusing anything that is not loopback
// (§8: "Listens on 127.0.0.1:4199 only (loopback, no auth)").
type LoopbackListener struct {
	Addr string
}

// Listen implements Listener.
func (l LoopbackListener) Listen() (net.Listener, error) {
	if err := requireLoopback(l.Addr); err != nil {
		return nil, fmt.Errorf("phi agent serve: --listen %q: remote access (D-PI-14) is not implemented, address must be loopback: %w", l.Addr, err)
	}
	return net.Listen("tcp", l.Addr)
}

// liveSession is one registry entry: a running piProc plus the bookkeeping
// the HTTP layer and the idle reaper need. profile/project never change
// after spawn (a resume keeps the session's original id, and with it its
// sidecar's profile/project).
type liveSession struct {
	id      string
	profile Profile
	project string
	proc    *piProc

	mu           sync.Mutex
	busy         bool
	lastActivity time.Time
}

func (ls *liveSession) touch() {
	ls.mu.Lock()
	ls.lastActivity = time.Now()
	ls.mu.Unlock()
}

func (ls *liveSession) setBusy(busy bool) {
	ls.mu.Lock()
	ls.busy = busy
	ls.lastActivity = time.Now()
	ls.mu.Unlock()
}

func (ls *liveSession) isBusy() bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.busy
}

func (ls *liveSession) idleSince(cutoff time.Time) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return !ls.busy && ls.lastActivity.Before(cutoff)
}

// Server is `phi agent serve`'s HTTP+SSE front end and session registry.
// Every exported field may be set right after NewServer, before Serve or
// Handler is used, so tests can inject a fake launcher and shrink the idle
// reaper's timers.
type Server struct {
	Model *Model

	// Launch builds the argv for one pi session (defaults to BuildLaunch).
	// Tests substitute a fake pi that re-execs the test binary.
	Launch func(LaunchSpec) ([]string, error)

	// IdleTimeout / ReapInterval: §8's 15-minute idle close, and how often
	// the reaper checks for it. Zero means the package default.
	IdleTimeout  time.Duration
	ReapInterval time.Duration

	// Stderr receives each live pi's stderr, one line at a time, prefixed
	// with "[<session-id>] " (§: stderr is diagnostics only). Defaults to
	// os.Stderr.
	Stderr io.Writer

	mu       sync.Mutex
	sessions map[string]*liveSession

	hub *sseHub
}

// NewServer returns a Server ready to have its fields tuned (if needed) and
// then Serve or Handler called.
func NewServer(m *Model) *Server {
	return &Server{
		Model:        m,
		Launch:       BuildLaunch,
		IdleTimeout:  defaultIdleTimeout,
		ReapInterval: defaultReapInterval,
		Stderr:       os.Stderr,
		sessions:     make(map[string]*liveSession),
		hub:          newSSEHub(),
	}
}

func (s *Server) idleTimeout() time.Duration {
	if s.IdleTimeout > 0 {
		return s.IdleTimeout
	}
	return defaultIdleTimeout
}

func (s *Server) reapInterval() time.Duration {
	if s.ReapInterval > 0 {
		return s.ReapInterval
	}
	return defaultReapInterval
}

// --- registry -----------------------------------------------------------

func (s *Server) liveSession(id string) *liveSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *Server) putSession(ls *liveSession) {
	s.mu.Lock()
	s.sessions[ls.id] = ls
	s.mu.Unlock()
}

func (s *Server) removeSession(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// isChatProfile is defined in tui.go, shared by `phi agent tui` and this API.

// spawnSession launches a pi RPC child for a session, registers it, and
// wires its events into the SSE hub. Exactly one of sessionID (a brand new
// session) or resumeFile (a host .jsonl to resume, §6) is set.
func (s *Server) spawnSession(id string, profile Profile, project, sessionID, resumeFile string) (*liveSession, error) {
	spec := LaunchSpec{Profile: profile, Project: project, Mode: ModeRPC, SessionID: sessionID, ResumeFile: resumeFile}
	argv, err := s.Launch(spec)
	if err != nil {
		return nil, err
	}

	ls := &liveSession{id: id, profile: profile, project: project, lastActivity: time.Now()}

	stderrSink := func(line string) {
		fmt.Fprintf(s.Stderr, "[%s] %s\n", id, line)
	}
	proc, err := startPiProc(argv, stderrSink, s.piEventHandler(ls), s.piExitHandler(ls))
	if err != nil {
		return nil, err
	}
	ls.proc = proc
	s.putSession(ls)
	return ls, nil
}

// piExitHandler removes the session from the registry and reports its exit
// over SSE (§8: "process exit → session.exited {code} (and remove from
// registry)").
func (s *Server) piExitHandler(ls *liveSession) func(error) {
	return func(err error) {
		s.removeSession(ls.id)
		code := 0
		var ee *exec.ExitError
		switch {
		case err == nil:
			code = 0
		case errors.As(err, &ee):
			code = ee.ExitCode()
		default:
			code = -1
		}
		s.hub.publish(sseEvent{"type": "session.exited", "session": ls.id, "code": code})
	}
}

// piEventHandler maps one pi stdout record (json.md, rpc-extension-ui.md)
// onto §8's SSE events.
func (s *Server) piEventHandler(ls *liveSession) EventFunc {
	return func(kind string, raw json.RawMessage) {
		ls.touch()
		switch kind {
		case "agent_start":
			ls.setBusy(true)
			s.hub.publish(sseEvent{"type": "session.busy", "session": ls.id})

		case "message_update":
			var ev struct {
				AssistantMessageEvent struct {
					Type  string `json:"type"`
					Delta string `json:"delta"`
				} `json:"assistantMessageEvent"`
			}
			if err := json.Unmarshal(raw, &ev); err == nil && ev.AssistantMessageEvent.Type == "text_delta" {
				s.hub.publish(sseEvent{"type": "message.delta", "session": ls.id, "text": ev.AssistantMessageEvent.Delta})
			}

		case "message_end":
			var ev struct {
				Message struct {
					Role         string `json:"role"`
					StopReason   string `json:"stopReason"`
					ErrorMessage string `json:"errorMessage"`
				} `json:"message"`
			}
			if err := json.Unmarshal(raw, &ev); err != nil || ev.Message.Role != "assistant" {
				return
			}
			s.hub.publish(sseEvent{"type": "message.done", "session": ls.id})
			if ev.Message.StopReason == "error" {
				errText := ev.Message.ErrorMessage
				if errText == "" {
					errText = "error"
				}
				s.hub.publish(sseEvent{"type": "session.error", "session": ls.id, "error": errText})
			}

		case "agent_settled":
			ls.setBusy(false)
			s.hub.publish(sseEvent{"type": "session.idle", "session": ls.id})

		case "extension_ui_request":
			s.replyExtensionUI(ls, raw)
		}
	}
}

// replyExtensionUI answers every dialog method (select, confirm, input,
// editor) with a cancelled response, and ignores fire-and-forget methods
// (notify, setStatus, setWidget, setTitle, set_editor_text) — §8's task,
// rpc-extension-ui.md's request/response shapes.
func (s *Server) replyExtensionUI(ls *liveSession, raw json.RawMessage) {
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(raw, &req); err != nil || req.ID == "" {
		return
	}
	switch req.Method {
	case "select", "confirm", "input", "editor":
		_ = ls.proc.sendRaw(map[string]any{
			"type":      "extension_ui_response",
			"id":        req.ID,
			"cancelled": true,
		})
	}
}

// sendCommand sends cmd to ls's pi process and reports a failure (transport
// or success:false) as session.error (§8: "a failed command response →
// session.error").
func (s *Server) sendCommand(ls *liveSession, cmd map[string]any) (piResponse, error) {
	ls.touch()
	resp, err := ls.proc.send(cmd, 0)
	if err != nil {
		s.hub.publish(sseEvent{"type": "session.error", "session": ls.id, "error": err.Error()})
		return resp, err
	}
	if !resp.Success {
		errText := resp.Error
		if errText == "" {
			errText = "command failed"
		}
		s.hub.publish(sseEvent{"type": "session.error", "session": ls.id, "error": errText})
		return resp, errors.New(errText)
	}
	return resp, nil
}

// closeSession closes one live proc's pi process (transcript stays; §8
// DELETE). The registry entry and the session.exited event follow from the
// process's own exit, handled by piExitHandler.
func (s *Server) closeSession(ls *liveSession) {
	ls.proc.close()
}

// --- idle reaper ----------------------------------------------------------

// startReaper runs the §8 15-minute idle close on a ticker until stop is
// called. Closing is done in its own goroutine per session so one slow
// close() (waiting out piCloseGrace) never delays reaping the rest.
func (s *Server) startReaper() (stop func()) {
	ticker := time.NewTicker(s.reapInterval())
	done := make(chan struct{})
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.reapIdle()
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

func (s *Server) reapIdle() {
	cutoff := time.Now().Add(-s.idleTimeout())
	s.mu.Lock()
	var stale []*liveSession
	for _, ls := range s.sessions {
		if ls.idleSince(cutoff) {
			stale = append(stale, ls)
		}
	}
	s.mu.Unlock()
	for _, ls := range stale {
		go s.closeSession(ls)
	}
}

// --- serving ---------------------------------------------------------------

// Serve binds every listener and serves the HTTP API until ctx is
// cancelled, then shuts down gracefully: stop accepting, close every live
// pi process, and return. Mirrors Broker.Run's shape (broker.go).
func (s *Server) Serve(ctx context.Context, listeners ...Listener) error {
	if len(listeners) == 0 {
		return errors.New("phi agent serve: no listener configured")
	}

	var lns []net.Listener
	for _, l := range listeners {
		ln, err := l.Listen()
		if err != nil {
			for _, opened := range lns {
				_ = opened.Close()
			}
			return err
		}
		lns = append(lns, ln)
	}

	httpSrv := &http.Server{Handler: s.Handler()}

	stopReaper := s.startReaper()
	defer stopReaper()

	errc := make(chan error, len(lns))
	for _, ln := range lns {
		ln := ln
		go func() { errc <- httpSrv.Serve(ln) }()
	}

	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}

	s.closeAllSessions()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) closeAllSessions() {
	s.mu.Lock()
	procs := make([]*liveSession, 0, len(s.sessions))
	for _, ls := range s.sessions {
		procs = append(procs, ls)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, ls := range procs {
		ls := ls
		wg.Add(1)
		go func() { defer wg.Done(); s.closeSession(ls) }()
	}
	wg.Wait()
}

// --- HTTP handlers -----------------------------------------------------

// Handler returns the §8 API as an http.Handler, with no listener bound —
// what tests drive through httptest, and what Serve wraps in an http.Server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /profiles", s.handleProfiles)
	mux.HandleFunc("GET /sessions", s.handleListSessions)
	mux.HandleFunc("POST /sessions", s.handleCreateSession)
	mux.HandleFunc("GET /sessions/{id}/messages", s.handleMessages)
	mux.HandleFunc("POST /sessions/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /sessions/{id}/abort", s.handleAbort)
	mux.HandleFunc("POST /sessions/{id}/title", s.handleTitle)
	mux.HandleFunc("POST /sessions/{id}/pin", s.handlePin)
	mux.HandleFunc("DELETE /sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("GET /events", s.handleEvents)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": build.Version})
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	out := []string{}
	for _, p := range ChatProfiles() {
		out = append(out, string(p))
	}
	writeJSON(w, http.StatusOK, out)
}

// sessionOut is one GET /sessions row (§8): TranscriptMeta plus live/busy,
// without its Path (host-internal, never in the API).
type sessionOut struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Profile string    `json:"profile"`
	Project string    `json:"project"`
	Pinned  bool      `json:"pinned"`
	Updated time.Time `json:"updated"`
	Live    bool      `json:"live"`
	Busy    bool      `json:"busy"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	unfiled := r.URL.Query().Get("unfiled") == "1"

	// ListTranscripts already unions *.phi.json and *.jsonl (sessionIDsInDir,
	// chat.go), so a session whose sidecar was written but whose pi child
	// has not written a transcript yet (a session just created by POST
	// /sessions, before its first prompt) is already included here.
	metas, err := s.Model.ListTranscripts(project, unfiled)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}

	out := []sessionOut{}
	for _, m := range metas {
		profile, perr := ParseProfile(m.Profile)
		if perr != nil || !isChatProfile(profile) {
			continue // coding/inline sessions have no panel chat (§1, §8)
		}
		ls := s.liveSession(m.ID)
		out = append(out, sessionOut{
			ID: m.ID, Title: m.Title, Profile: m.Profile, Project: m.Project,
			Pinned: m.Pinned, Updated: m.Updated,
			Live: ls != nil, Busy: ls != nil && ls.isBusy(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile string `json:"profile"`
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}

	profile, err := ParseProfile(body.Profile)
	if err != nil || !isChatProfile(profile) {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("profile must be one of %v", ChatProfiles()))
		return
	}
	if body.Project != "" && !s.Model.HasProject(body.Project) {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("no such project: %q", body.Project))
		return
	}

	dir, err := s.Model.SessionsDir(body.Project)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	id := NewSessionID()
	if err := WriteSidecar(dir, Sidecar{
		ID: id, Profile: string(profile), Project: body.Project, Created: time.Now().UTC(),
	}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}

	if _, err := s.spawnSession(id, profile, body.Project, id, ""); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}

	s.hub.publish(sseEvent{"type": "session.created", "session": id, "profile": string(profile), "project": body.Project})
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if ls := s.liveSession(id); ls != nil {
		resp, err := s.sendCommand(ls, map[string]any{"type": "get_messages"})
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err)
			return
		}
		var data struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			writeJSONError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, NormalizeAgentMessages(data.Messages))
		return
	}

	_, jsonlPath, _, err := s.Model.FindTranscript(id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	if jsonlPath == "" {
		writeJSON(w, http.StatusOK, []Message{})
		return
	}
	msgs, err := TranscriptMessages(jsonlPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	if msgs == nil {
		msgs = []Message{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}

	ls := s.liveSession(id)
	if ls == nil {
		dir, jsonlPath, meta, err := s.Model.FindTranscript(id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err)
			return
		}
		profile, perr := ParseProfile(meta.Profile)
		if perr != nil || !isChatProfile(profile) {
			writeJSONError(w, http.StatusBadRequest, fmt.Errorf("session %q is not a chat profile", id))
			return
		}
		project := s.Model.projectForSessionsDir(dir)
		sessionID, resumeFile := "", ""
		if jsonlPath != "" {
			resumeFile = jsonlPath
		} else {
			sessionID = id
		}
		ls, err = s.spawnSession(id, profile, project, sessionID, resumeFile)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
	}

	cmd := map[string]any{"type": "prompt", "message": body.Text}
	if ls.isBusy() {
		cmd["streamingBehavior"] = "followUp"
	}
	if _, err := s.sendCommand(ls, cmd); err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{})
}

func (s *Server) handleAbort(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ls := s.liveSession(id)
	if ls == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Errorf("session %q is not live", id))
		return
	}
	if _, err := s.sendCommand(ls, map[string]any{"type": "abort"}); err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleTitle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Model.SetChatTitle(id, body.Title); err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	if ls := s.liveSession(id); ls != nil {
		// A live rename that fails is already reported as session.error by
		// sendCommand; the sidecar (the source of truth for GET /sessions)
		// is already updated, so it is not fatal to this request.
		_, _ = s.sendCommand(ls, map[string]any{"type": "set_session_name", "name": body.Title})
	}
	s.hub.publish(sseEvent{"type": "session.title", "session": id, "title": body.Title})
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Pinned bool `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Model.SetChatPinned(id, body.Pinned); err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if ls := s.liveSession(id); ls != nil {
		s.closeSession(ls)
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// --- SSE ---------------------------------------------------------------

// sseEvent is one §8 SSE payload; a plain map keeps every event's field set
// exactly as documented without a struct per event type.
type sseEvent map[string]any

// sseHub fans events out to every subscribed /events client. Publish never
// blocks on a slow client: each subscriber has a small buffered channel,
// and a client that does not drain it is dropped rather than stalling the
// others.
type sseHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newSSEHub() *sseHub {
	return &sseHub{clients: make(map[chan []byte]struct{})}
}

func (h *sseHub) subscribe() chan []byte {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *sseHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *sseHub) publish(ev sseEvent) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- b:
		default:
			// Slow client: drop it rather than block the rest of the fan-out.
			delete(h.clients, ch)
			close(ch)
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case b, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
