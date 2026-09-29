package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Integration of `phi agent serve` with the prefs, scheduler, usage and
// coding-monitor components (plan §5.1, §5.5–§5.8): the engine defaults a
// new session starts with, the scheduler's host, the coding-session watcher,
// and the endpoints that expose them.

// prefs reads the user's engine prefs. When the server is not reading prefs
// (tests), or the file is unreadable, the defaults apply.
func (s *Server) prefs() Prefs {
	if !s.ReadPrefs {
		return DefaultPrefs()
	}
	p, err := LoadPrefs()
	if err != nil {
		s.logf("warn", "", "serve", "prefs: %v (using defaults)", err)
		return DefaultPrefs()
	}
	return p
}

// profileDefaults is the prefs model and thinking level for new sessions of
// profile ("" = pi's own default).
func (s *Server) profileDefaults(profile Profile) (model, thinking string) {
	p := s.prefs()
	return p.ModelFor(profile), p.ThinkingFor(profile)
}

// defaultProfile is the profile of a new session that names none.
func (s *Server) defaultProfile() string {
	if d := s.prefs().DefaultProfile; d != "" {
		return d
	}
	return string(General)
}

// scheduledIndex maps session id → schedule job id for every recorded run,
// so the session list can mark chats a job created.
func (s *Server) scheduledIndex() map[string]string {
	out := map[string]string{}
	f, err := LoadSchedule()
	if err != nil {
		return out
	}
	for _, r := range f.Runs {
		if r.Session != "" {
			out[r.Session] = r.Job
		}
	}
	return out
}

// --- scheduler host ----------------------------------------------------

// scheduleHost runs scheduled jobs through the server. A job that continues
// an existing chat is capped on the cost of this run only, so the session's
// cost when the run starts is kept as its baseline.
type scheduleHost struct {
	s         *Server
	mu        sync.Mutex
	baselines map[string]float64
}

func (h *scheduleHost) Enabled() (bool, float64) {
	p := h.s.prefs()
	return p.Scheduler.Enabled, p.Scheduler.DailyCap
}

func (h *scheduleHost) StartRun(j Job) (string, error) {
	profile, err := ParseProfile(j.Profile)
	if err != nil || !isChatProfile(profile) {
		return "", fmt.Errorf("job %s: profile must be general or academic", j.ID)
	}
	var id string
	baseline := 0.0
	if strings.HasPrefix(j.Target, "session:") {
		id = strings.TrimPrefix(j.Target, "session:")
		if tl, entries, err := h.s.diskTimeline(id); err == nil {
			baseline = ComputeStats(entries, tl).Cost
		}
		ls, _, _, err := h.s.ensureLive(id, j.Model, j.Thinking)
		if err != nil {
			return "", err
		}
		h.setBaseline(id, baseline)
		if err := h.s.sendPrompt(ls, j.Prompt, "followUp", nil); err != nil {
			return "", err
		}
		return id, nil
	}
	id, err = h.s.createSession(profile, j.Project, j.Model, j.Thinking, j.Title)
	if err != nil {
		return "", err
	}
	h.setBaseline(id, 0)
	ls := h.s.liveSession(id)
	if ls == nil {
		return "", errors.New("session exited before its first prompt")
	}
	if err := h.s.sendPrompt(ls, j.Prompt, "followUp", nil); err != nil {
		return "", err
	}
	return id, nil
}

func (h *scheduleHost) Abort(sessionID string) error {
	ls := h.s.liveSession(sessionID)
	if ls == nil {
		return nil
	}
	_, _ = h.s.clearQueue(ls)
	_, err := h.s.sendCommand(ls, map[string]any{"type": "abort"})
	return err
}

func (h *scheduleHost) setBaseline(id string, v float64) {
	h.mu.Lock()
	h.baselines[id] = v
	h.mu.Unlock()
}

func (h *scheduleHost) runCost(id string, total float64) float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return total - h.baselines[id]
}

// initScheduler builds the scheduler and wires it to the server's events;
// Serve starts its tick. The endpoints work without the tick (tests).
func (s *Server) initScheduler() {
	host := &scheduleHost{s: s, baselines: map[string]float64{}}
	sched := NewScheduler(host)
	sched.OnChange = func() { s.hub.publish(sseEvent{"type": "schedule.changed"}) }
	sched.OnFired = func(jobID, session string) {
		s.hub.publish(sseEvent{"type": "schedule.fired", "job": jobID, "session": session})
		s.logf("info", session, "schedule", "job %s started", jobID)
	}
	s.sched = sched
	s.onStats = func(ls *liveSession, st Stats) {
		if sched.IsScheduled(ls.id) != "" {
			sched.ObserveCost(ls.id, host.runCost(ls.id, st.Cost))
		}
	}
	s.onSettled = func(ls *liveSession, failed bool) {
		if sched.IsScheduled(ls.id) == "" {
			return
		}
		cost := 0.0
		if st, err := s.liveStats(ls); err == nil {
			cost = host.runCost(ls.id, st.Cost)
		}
		sched.ObserveIdle(ls.id, failed, cost)
	}
}

func (s *Server) startScheduler() (stop func()) { return s.sched.Start(scheduleTick) }

const scheduleTick = 30 * time.Second

// --- coding watcher ----------------------------------------------------------

// startCodingWatcher polls active coding sessions every codingPoll while at
// least one event client is connected, and announces each change. Coding
// sessions run pi's TUI in a terminal, so their JSONL is the only live signal.
func (s *Server) startCodingWatcher() (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(codingPoll)
		defer t.Stop()
		prev := map[string]CodingSnap{}
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if s.hub.clientCount() == 0 {
					continue
				}
				rows, err := ActiveCodingRows(time.Now())
				if err != nil {
					continue
				}
				next := SnapshotCoding(rows)
				for _, id := range ChangedCoding(prev, next) {
					state := "ended"
					if sn, ok := next[id]; ok {
						state = sn.State
					}
					s.hub.publish(sseEvent{"type": "coding.changed", "id": id, "state": state})
				}
				prev = next
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

const codingPoll = 2 * time.Second

func (h *sseHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// --- routes --------------------------------------------------------------

func (s *Server) extendRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /overview", s.handleOverview)
	mux.HandleFunc("GET /usage", s.handleUsage)
	mux.HandleFunc("GET /coding", s.handleCoding)
	mux.HandleFunc("GET /coding/{id}/timeline", s.handleCodingTimeline)
	mux.HandleFunc("GET /schedule", s.handleScheduleList)
	mux.HandleFunc("POST /schedule", s.handleScheduleCreate)
	mux.HandleFunc("PUT /schedule/{id}", s.handleScheduleUpdate)
	mux.HandleFunc("DELETE /schedule/{id}", s.handleScheduleDelete)
	mux.HandleFunc("POST /schedule/{id}/run", s.handleScheduleRun)
	mux.HandleFunc("GET /broker/requests", s.handleBrokerRequests)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	// Only the live registry's sessions are resolved: the overview is
	// polled, and listing every transcript here would parse them all.
	live := []sessionOut{}
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	sched := s.scheduledIndex()
	for _, id := range ids {
		dir, jsonlPath, sc, err := s.Model.FindTranscript(id)
		if err != nil {
			continue
		}
		row := s.sessionRow(TranscriptMeta{ID: id, Title: resolveTitle(sc, jsonlPath), Profile: sc.Profile,
			Project: s.Model.projectForSessionsDir(dir), Pinned: sc.Pinned,
			Updated: transcriptUpdated(jsonlPath, sc, sidecarPath(dir, id))})
		row.Scheduled = sched[id]
		live = append(live, row)
	}
	sort.Slice(live, func(i, k int) bool { return live[i].Updated.After(live[k].Updated) })
	coding, err := ActiveCodingRows(now)
	if err != nil || coding == nil {
		coding = []CodingRow{}
	}
	dialogs := 0
	s.mu.Lock()
	for _, ls := range s.sessions {
		ls.mu.Lock()
		dialogs += len(ls.turn.dialogs)
		ls.mu.Unlock()
	}
	s.mu.Unlock()

	today := Usage{}
	if u, err := ComputeUsage(s.Model, 1, now); err == nil {
		today = u.Today
	}

	p := s.prefs()
	var next any
	if f, err := LoadSchedule(); err == nil {
		var best *Job
		for i := range f.Jobs {
			j := &f.Jobs[i]
			if j.Enabled && j.Next != nil && (best == nil || j.Next.Before(*best.Next)) {
				best = j
			}
		}
		if best != nil {
			next = map[string]any{"job": best.ID, "title": best.Title, "at": best.Next}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"api": APILevel, "version": buildVersion(), "startedAt": s.startedAt,
		"live": live, "coding": coding, "dialogs": dialogs, "usageToday": today,
		"scheduler": map[string]any{"enabled": p.Scheduler.Enabled, "next": next},
		"errors":    s.logs.lastErrors(10),
	})
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 365 {
		days = 30
	}
	u, err := ComputeUsage(s.Model, days, time.Now())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleCoding(w http.ResponseWriter, r *http.Request) {
	rows, err := CodingRows(time.Now())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	if rows == nil {
		rows = []CodingRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleCodingTimeline(w http.ResponseWriter, r *http.Request) {
	tl, err := CodingTimeline(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, tl)
}

func (s *Server) handleScheduleList(w http.ResponseWriter, r *http.Request) {
	f, err := s.sched.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	jobs := f.Jobs
	sort.SliceStable(jobs, func(i, k int) bool { return jobs[i].Title < jobs[k].Title })
	p := s.prefs()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": p.Scheduler.Enabled, "dailyCap": p.Scheduler.DailyCap,
		"spentToday": SpentToday(f, time.Now()), "jobs": jobs,
	})
}

func decodeJob(r *http.Request) (Job, error) {
	var j Job
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		return Job{}, err
	}
	return j, nil
}

func (s *Server) handleScheduleCreate(w http.ResponseWriter, r *http.Request) {
	j, err := decodeJob(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.sched.Create(j)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleScheduleUpdate(w http.ResponseWriter, r *http.Request) {
	j, err := decodeJob(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.sched.Update(r.PathValue("id"), j)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleScheduleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.sched.Delete(r.PathValue("id")); err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleScheduleRun(w http.ResponseWriter, r *http.Request) {
	session, err := s.sched.RunNow(r.PathValue("id"))
	if err != nil {
		s.logf("error", "", "schedule", "run %s: %v", r.PathValue("id"), err)
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleBrokerRequests(w http.ResponseWriter, r *http.Request) {
	inst, err := ParseInstance(orString(r.URL.Query().Get("instance"), "a1"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	reqs, err := RecentBrokerRequests(inst, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, reqs)
}
