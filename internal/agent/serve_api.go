package agent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Session endpoints of the level-2 API (plan §5.2–§5.3, §5.8). serve.go
// owns the registry, the pi process lifecycle and SSE; this file is the
// request handling on top of it.

// sessionOut is one GET /sessions row: TranscriptMeta plus live state,
// without its Path (host-internal, never in the API).
type sessionOut struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Profile    string    `json:"profile"`
	Project    string    `json:"project"`
	Pinned     bool      `json:"pinned"`
	Updated    time.Time `json:"updated"`
	Live       bool      `json:"live"`
	Busy       bool      `json:"busy"`
	Activity   string    `json:"activity"`
	NeedsInput bool      `json:"needsInput"`
	Failed     bool      `json:"failed"`
	Scheduled  string    `json:"scheduled"`
}

// sessionRow builds the list row of one transcript, merging the live
// registry entry when there is one.
func (s *Server) sessionRow(m TranscriptMeta) sessionOut {
	out := sessionOut{ID: m.ID, Title: m.Title, Profile: m.Profile, Project: m.Project,
		Pinned: m.Pinned, Updated: m.Updated}
	if ls := s.liveSession(m.ID); ls != nil {
		ls.mu.Lock()
		out.Live = true
		out.Busy = ls.busy
		out.Activity = ls.turn.activity
		out.NeedsInput = len(ls.turn.dialogs) > 0
		out.Failed = ls.turn.failed
		ls.mu.Unlock()
	}
	return out
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	unfiled := r.URL.Query().Get("unfiled") == "1"

	// ListTranscripts unions *.phi.json and *.jsonl, so a session created
	// by POST /sessions whose pi child has not written a transcript yet is
	// already listed.
	metas, err := s.Model.ListTranscripts(project, unfiled)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	out := []sessionOut{}
	sched := s.scheduledIndex()
	for _, m := range metas {
		profile, perr := ParseProfile(m.Profile)
		if perr != nil || !isChatProfile(profile) {
			continue // coding/inline sessions have no panel chat
		}
		row := s.sessionRow(m)
		row.Scheduled = sched[m.ID]
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// spawnArgs are pi's model and thinking flags for a new process: the
// request's own choice, else the prefs default for the profile.
func (s *Server) spawnArgs(profile Profile, model, thinking string) []string {
	defModel, defThinking := s.profileDefaults(profile)
	if model == "" {
		model = defModel
	}
	if thinking == "" {
		thinking = defThinking
	}
	var args []string
	if model != "" {
		args = append(args, "--model", model)
	}
	if thinking != "" {
		args = append(args, "--thinking", thinking)
	}
	return args
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile  string `json:"profile"`
		Project  string `json:"project"`
		Model    string `json:"model"`
		Thinking string `json:"thinking"`
		Title    string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	if body.Profile == "" {
		body.Profile = s.defaultProfile()
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
	if err := checkModelRef(body.Model); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.createSession(profile, body.Project, body.Model, body.Thinking, body.Title)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// createSession writes the sidecar, spawns the pi child and announces the
// session. Shared by POST /sessions and the scheduler.
func (s *Server) createSession(profile Profile, project, model, thinking, title string) (string, error) {
	dir, err := s.Model.SessionsDir(project)
	if err != nil {
		return "", err
	}
	id := NewSessionID()
	if err := WriteSidecar(dir, Sidecar{
		ID: id, Profile: string(profile), Project: project, Title: title, Created: time.Now().UTC(),
	}); err != nil {
		return "", err
	}
	if _, err := s.spawnSession(id, profile, project, id, "", s.spawnArgs(profile, model, thinking)); err != nil {
		return "", err
	}
	s.hub.publish(sseEvent{"type": "session.created", "session": id, "profile": string(profile), "project": project})
	return id, nil
}

// ensureLive returns the live session for id, resuming it from its
// transcript when it is not running. model/thinking apply only to a
// process started here.
func (s *Server) ensureLive(id, model, thinking string) (*liveSession, bool, int, error) {
	if ls := s.liveSession(id); ls != nil {
		return ls, false, 0, nil
	}
	dir, jsonlPath, meta, err := s.Model.FindTranscript(id)
	if err != nil {
		return nil, false, http.StatusNotFound, err
	}
	profile, perr := ParseProfile(meta.Profile)
	if perr != nil || !isChatProfile(profile) {
		return nil, false, http.StatusBadRequest, fmt.Errorf("session %q is not a chat profile", id)
	}
	project := s.Model.projectForSessionsDir(dir)
	sessionID, resumeFile := "", ""
	if jsonlPath != "" {
		resumeFile = jsonlPath
	} else {
		sessionID = id
	}
	ls, err := s.spawnSession(id, profile, project, sessionID, resumeFile, s.spawnArgs(profile, model, thinking))
	if err != nil {
		return nil, false, http.StatusInternalServerError, err
	}
	return ls, true, 0, nil
}

// sendPrompt delivers one message: while the session is busy it is queued
// (steer or follow-up); otherwise it starts a turn.
func (s *Server) sendPrompt(ls *liveSession, text, mode string, images []map[string]any) error {
	cmd := map[string]any{"type": "prompt", "message": text}
	if len(images) > 0 {
		cmd["images"] = images
	}
	if ls.isBusy() {
		switch mode {
		case "steer":
			cmd["streamingBehavior"] = "steer"
		default:
			cmd["streamingBehavior"] = "followUp"
		}
	}
	_, err := s.sendCommand(ls, cmd)
	return err
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Text     string   `json:"text"`
		Images   []string `json:"images"`
		Mode     string   `json:"mode"`
		Model    string   `json:"model"`
		Thinking string   `json:"thinking"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Text) == "" && len(body.Images) == 0 {
		writeJSONError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	switch body.Mode {
	case "", "auto", "steer", "followUp":
	default:
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("mode must be auto, steer or followUp"))
		return
	}
	if err := checkModelRef(body.Model); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	images, err := loadPromptImages(body.Images)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}

	ls, spawned, status, err := s.ensureLive(id, body.Model, body.Thinking)
	if err != nil {
		writeJSONError(w, status, err)
		return
	}
	if !spawned {
		if body.Model != "" {
			if _, err := s.setModel(ls, body.Model); err != nil {
				writeJSONError(w, http.StatusBadGateway, err)
				return
			}
		}
		if body.Thinking != "" {
			if _, err := s.sendCommand(ls, map[string]any{"type": "set_thinking_level", "level": body.Thinking}); err != nil {
				writeJSONError(w, http.StatusBadGateway, err)
				return
			}
		}
	}
	if err := s.sendPrompt(ls, body.Text, body.Mode, images); err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{})
}

// maxPromptImage bounds one attached image: providers reject far smaller
// ones already, and the whole file is held in memory to base64 it.
const maxPromptImage = 8 << 20

var imageMIME = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif",
}

// loadPromptImages reads the absolute image paths a client attached. phi
// reads them outside the containment on the user's explicit choice; the
// agent only ever receives the bytes.
func loadPromptImages(paths []string) ([]map[string]any, error) {
	var out []map[string]any
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("image path must be absolute: %q", p)
		}
		mime, ok := imageMIME[strings.ToLower(filepath.Ext(p))]
		if !ok {
			return nil, fmt.Errorf("unsupported image type: %q (png, jpeg, webp, gif)", filepath.Base(p))
		}
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if fi.Size() > maxPromptImage {
			return nil, fmt.Errorf("image %q is larger than 8 MiB", filepath.Base(p))
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(b), "mimeType": mime})
	}
	return out, nil
}

func checkModelRef(ref string) error {
	if ref == "" {
		return nil
	}
	i := strings.Index(ref, "/")
	if i <= 0 || i == len(ref)-1 {
		return fmt.Errorf("model must be provider/id, got %q", ref)
	}
	return nil
}

func (s *Server) setModel(ls *liveSession, ref string) (json.RawMessage, error) {
	i := strings.Index(ref, "/")
	if i <= 0 {
		return nil, fmt.Errorf("model must be provider/id, got %q", ref)
	}
	resp, err := s.sendCommand(ls, map[string]any{"type": "set_model", "provider": ref[:i], "modelId": ref[i+1:]})
	return resp.Data, err
}

// clearQueue empties pi's steering and follow-up queues and returns their
// text, so a client can put it back into its composer.
func (s *Server) clearQueue(ls *liveSession) (map[string][]string, error) {
	resp, err := s.sendCommand(ls, map[string]any{"type": "clear_queue"})
	out := map[string][]string{"steering": {}, "followUp": {}}
	if err != nil {
		return out, err
	}
	var d struct {
		Steering []string `json:"steering"`
		FollowUp []string `json:"followUp"`
	}
	if json.Unmarshal(resp.Data, &d) == nil {
		out["steering"] = nonNil(d.Steering)
		out["followUp"] = nonNil(d.FollowUp)
	}
	return out, nil
}

func (s *Server) handleAbort(w http.ResponseWriter, r *http.Request) {
	ls := s.liveSession(r.PathValue("id"))
	if ls == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Errorf("session %q is not live", r.PathValue("id")))
		return
	}
	// Clear first: pi's abort would otherwise go on to deliver the queue.
	queued, _ := s.clearQueue(ls)
	if _, err := s.sendCommand(ls, map[string]any{"type": "abort"}); err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, queued)
}

func (s *Server) handleQueueClear(w http.ResponseWriter, r *http.Request) {
	ls := s.liveSession(r.PathValue("id"))
	if ls == nil {
		writeJSONError(w, http.StatusConflict, errors.New("session is not live"))
		return
	}
	queued, err := s.clearQueue(ls)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, queued)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if ls := s.liveSession(id); ls != nil {
		s.closeSession(ls)
	}
	if r.URL.Query().Get("purge") == "1" {
		if err := s.Model.DeleteChat(id); err != nil {
			writeJSONError(w, http.StatusNotFound, err)
			return
		}
		s.hub.publish(sseEvent{"type": "session.title", "session": id, "title": ""})
		s.logf("info", id, "serve", "session deleted")
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// --- timeline and state ------------------------------------------------

// liveTimeline builds a live session's timeline from pi's get_entries (the
// same entry shapes as the JSONL, so the same normaliser) plus the
// in-progress message.
func (s *Server) liveTimeline(ls *liveSession) (Timeline, []json.RawMessage, error) {
	resp, err := s.sendCommand(ls, map[string]any{"type": "get_entries"})
	if err != nil {
		return Timeline{}, nil, err
	}
	var d struct {
		Entries []json.RawMessage `json:"entries"`
		LeafID  *string           `json:"leafId"`
	}
	if err := json.Unmarshal(resp.Data, &d); err != nil {
		return Timeline{}, nil, err
	}
	leaf := ""
	if d.LeafID != nil {
		leaf = *d.LeafID
	}
	tl := BuildTimeline(d.Entries, leaf)
	ls.mu.Lock()
	if p := ls.turn.pending; p != nil && len(p.Blocks) > 0 {
		cp := PendingTurn{StartedAt: p.StartedAt, Blocks: append([]Block(nil), p.Blocks...)}
		tl.Pending = &cp
	}
	ls.mu.Unlock()
	return tl, d.Entries, nil
}

// diskTimeline reads a session's transcript; a session pi has not written
// yet has an empty timeline.
func (s *Server) diskTimeline(id string) (Timeline, []json.RawMessage, error) {
	_, jsonlPath, _, err := s.Model.FindTranscript(id)
	if err != nil {
		return Timeline{}, nil, err
	}
	if jsonlPath == "" {
		return Timeline{Items: []Item{}}, nil, nil
	}
	return LoadTimelineFile(jsonlPath)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var tl Timeline
	var err error
	if ls := s.liveSession(id); ls != nil {
		tl, _, err = s.liveTimeline(ls)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err)
			return
		}
	} else {
		tl, _, err = s.diskTimeline(id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, tl)
}

// liveStats combines pi's own session statistics (exact tokens, cost and
// context fill) with the counts and files the timeline gives.
func (s *Server) liveStats(ls *liveSession) (Stats, error) {
	tl, entries, err := s.liveTimeline(ls)
	if err != nil {
		return Stats{}, err
	}
	st := ComputeStats(entries, tl)
	resp, err := s.sendCommand(ls, map[string]any{"type": "get_session_stats"})
	if err != nil {
		return st, nil
	}
	var d struct {
		Tokens       *Tokens  `json:"tokens"`
		Cost         *float64 `json:"cost"`
		ContextUsage *struct {
			Tokens        *int64   `json:"tokens"`
			ContextWindow *int64   `json:"contextWindow"`
			Percent       *float64 `json:"percent"`
		} `json:"contextUsage"`
	}
	if json.Unmarshal(resp.Data, &d) == nil {
		if d.Tokens != nil {
			st.Tokens = *d.Tokens
		}
		if d.Cost != nil {
			st.Cost = *d.Cost
		}
		if d.ContextUsage != nil {
			st.Context = ContextUsage{Tokens: d.ContextUsage.Tokens, Window: d.ContextUsage.ContextWindow, Percent: d.ContextUsage.Percent}
		}
	}
	return st, nil
}

// publishStats sends session.stats after a turn, and lets the scheduler
// enforce a run's cost cap.
func (s *Server) publishStats(ls *liveSession) {
	st, err := s.liveStats(ls)
	if err != nil {
		return
	}
	s.hub.publish(sseEvent{"type": "session.stats", "session": ls.id, "stats": st})
	if s.onStats != nil {
		s.onStats(ls, st)
	}
}

func modelInfoFrom(raw json.RawMessage) *ModelInfo {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m ModelInfo
	if json.Unmarshal(raw, &m) != nil || m.ID == "" {
		return nil
	}
	return &m
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ls := s.liveSession(id)
	if ls == nil {
		tl, entries, err := s.diskTimeline(id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err)
			return
		}
		var model any
		if last := lastAssistantItem(tl); last != nil && last.Model != "" {
			model = ModelInfo{Provider: last.Provider, ID: last.Model}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "live": false, "busy": false, "activity": "", "compacting": false, "retry": nil,
			"model": model, "thinkingLevel": "", "thinkingLevels": []string{},
			"queue":   map[string][]string{"steering": {}, "followUp": {}},
			"dialogs": []Dialog{}, "status": map[string]string{}, "widgets": map[string][]string{},
			"stats": ComputeStats(entries, tl),
		})
		return
	}

	out := map[string]any{"id": id, "live": true}
	if resp, err := s.sendCommand(ls, map[string]any{"type": "get_state"}); err == nil {
		var d struct {
			Model         json.RawMessage `json:"model"`
			ThinkingLevel string          `json:"thinkingLevel"`
		}
		if json.Unmarshal(resp.Data, &d) == nil {
			out["model"] = modelInfoFrom(d.Model)
			out["thinkingLevel"] = d.ThinkingLevel
		}
	}
	levels := []string{}
	if resp, err := s.sendCommand(ls, map[string]any{"type": "get_available_thinking_levels"}); err == nil {
		var d struct {
			Levels []string `json:"levels"`
		}
		if json.Unmarshal(resp.Data, &d) == nil && d.Levels != nil {
			levels = d.Levels
		}
	}
	out["thinkingLevels"] = levels
	if st, err := s.liveStats(ls); err == nil {
		out["stats"] = st
	}

	ls.mu.Lock()
	out["busy"] = ls.busy
	out["activity"] = ls.turn.activity
	out["compacting"] = ls.turn.compacting
	out["retry"] = ls.turn.retry
	out["queue"] = map[string][]string{"steering": nonNil(ls.turn.queueSteer), "followUp": nonNil(ls.turn.queueFollow)}
	dialogs := []Dialog{}
	for _, d := range ls.turn.dialogs {
		dialogs = append(dialogs, d.Dialog)
	}
	out["dialogs"] = dialogs
	status := map[string]string{}
	for k, v := range ls.turn.status {
		status[k] = v
	}
	out["status"] = status
	widgets := map[string][]string{}
	for k, v := range ls.turn.widgets {
		widgets[k] = v
	}
	out["widgets"] = widgets
	ls.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// --- controls --------------------------------------------------------------

func (s *Server) requireLive(w http.ResponseWriter, id string) *liveSession {
	ls := s.liveSession(id)
	if ls == nil {
		writeJSONError(w, http.StatusConflict, fmt.Errorf("session %q is not live", id))
	}
	return ls
}

func (s *Server) handleSetModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || checkModelRef(body.Model) != nil || body.Model == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("body must be {\"model\":\"provider/id\"}"))
		return
	}
	ls := s.requireLive(w, r.PathValue("id"))
	if ls == nil {
		return
	}
	data, err := s.setModel(ls, body.Model)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, modelInfoFrom(data))
}

func (s *Server) handleSetThinking(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Level == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("body must be {\"level\":\"…\"}"))
		return
	}
	ls := s.requireLive(w, r.PathValue("id"))
	if ls == nil {
		return
	}
	if _, err := s.sendCommand(ls, map[string]any{"type": "set_thinking_level", "level": body.Level}); err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// compactTimeout: compaction is one extra model call over the whole
// context, which can take far longer than an ordinary command.
const compactTimeout = 5 * time.Minute

func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Instructions string `json:"instructions"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ls := s.requireLive(w, r.PathValue("id"))
	if ls == nil {
		return
	}
	cmd := map[string]any{"type": "compact"}
	if body.Instructions != "" {
		cmd["customInstructions"] = body.Instructions
	}
	ls.touch()
	resp, err := ls.proc.send(cmd, compactTimeout)
	if err == nil && !resp.Success {
		err = errors.New(orString(resp.Error, "compaction failed"))
	}
	if err != nil {
		s.logf("error", ls.id, "serve", "compact: %v", err)
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	var d struct {
		TokensBefore         int64 `json:"tokensBefore"`
		EstimatedTokensAfter int64 `json:"estimatedTokensAfter"`
	}
	_ = json.Unmarshal(resp.Data, &d)
	writeJSON(w, http.StatusOK, map[string]any{"tokensBefore": d.TokensBefore, "tokensAfter": d.EstimatedTokensAfter})
}

func (s *Server) handleDialog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value     *string `json:"value"`
		Confirmed *bool   `json:"confirmed"`
		Cancelled bool    `json:"cancelled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	ls := s.requireLive(w, r.PathValue("id"))
	if ls == nil {
		return
	}
	answer := map[string]any{}
	switch {
	case body.Cancelled:
		answer["cancelled"] = true
	case body.Confirmed != nil:
		answer["confirmed"] = *body.Confirmed
	case body.Value != nil:
		answer["value"] = *body.Value
	default:
		writeJSONError(w, http.StatusBadRequest, errors.New("body needs value, confirmed or cancelled"))
		return
	}
	if !s.resolveDialog(ls, r.PathValue("dialog"), answer, "answered") {
		writeJSONError(w, http.StatusNotFound, errors.New("no such pending dialog"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	ls := s.liveSession(r.PathValue("id"))
	out := []map[string]string{}
	if ls == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	resp, err := s.sendCommand(ls, map[string]any{"type": "get_commands"})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	var d struct {
		Commands []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Source      string `json:"source"`
		} `json:"commands"`
	}
	if json.Unmarshal(resp.Data, &d) == nil {
		for _, c := range d.Commands {
			out = append(out, map[string]string{"name": c.Name, "description": c.Description, "source": c.Source})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	meta, msgs, err := s.Model.LoadTranscript(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, TranscriptMarkdown(meta, msgs))
}

// --- models ------------------------------------------------------------------

// modelsCacheTTL: the model list only changes when models.json is edited,
// which also needs a service restart to reach running sessions.
const modelsCacheTTL = 10 * time.Minute

type modelsCacheEntry struct {
	models []ModelInfo
	at     time.Time
}

func parseModelList(data json.RawMessage) []ModelInfo {
	var d struct {
		Models []json.RawMessage `json:"models"`
	}
	out := []ModelInfo{}
	if json.Unmarshal(data, &d) != nil {
		return out
	}
	for _, raw := range d.Models {
		if m := modelInfoFrom(raw); m != nil {
			out = append(out, *m)
		}
	}
	return out
}

// availableModels asks pi which models a profile can use: a live session of
// that profile answers directly; otherwise a short-lived contained pi with
// no session is started, asked, and closed.
func (s *Server) availableModels(profile Profile) ([]ModelInfo, error) {
	s.modelsMu.Lock()
	if e, ok := s.modelsCache[profile]; ok && time.Since(e.at) < modelsCacheTTL {
		s.modelsMu.Unlock()
		return e.models, nil
	}
	s.modelsMu.Unlock()

	var data json.RawMessage
	var live *liveSession
	s.mu.Lock()
	for _, ls := range s.sessions {
		if ls.profile == profile {
			live = ls
			break
		}
	}
	s.mu.Unlock()
	if live != nil {
		resp, err := s.sendCommand(live, map[string]any{"type": "get_available_models"})
		if err != nil {
			return nil, err
		}
		data = resp.Data
	} else {
		argv, err := s.Launch(LaunchSpec{Profile: profile, Mode: ModeRPC, ExtraArgs: []string{"--no-session"}})
		if err != nil {
			return nil, err
		}
		proc, err := startPiProc(argv, func(line string) { s.logf(piStderrLevel(line), "", "pi", "%s", line) }, nil, nil)
		if err != nil {
			return nil, err
		}
		resp, err := proc.send(map[string]any{"type": "get_available_models"}, 30*time.Second)
		go proc.close()
		if err != nil {
			return nil, err
		}
		if !resp.Success {
			return nil, errors.New(orString(resp.Error, "get_available_models failed"))
		}
		data = resp.Data
	}
	models := parseModelList(data)
	s.modelsMu.Lock()
	s.modelsCache[profile] = modelsCacheEntry{models: models, at: time.Now()}
	s.modelsMu.Unlock()
	return models, nil
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("profile")
	if name == "" {
		name = string(General)
	}
	profile, err := ParseProfile(name)
	if err != nil || profile == Inline {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("profile must be general, academic or coding"))
		return
	}
	models, err := s.availableModels(profile)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	def, _ := s.profileDefaults(profile)
	writeJSON(w, http.StatusOK, map[string]any{"models": models, "default": def})
}

// --- logs --------------------------------------------------------------------

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	level := q.Get("level")
	if _, ok := logLevelRank[level]; !ok {
		level = "debug"
	}
	entries, next := s.logs.since(since, level, q.Get("session"), limit)
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "next": next})
}
