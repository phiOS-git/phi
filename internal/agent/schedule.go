package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Scheduled agent runs (docs/agent-panel-plan.md §5.7): jobs that fire a
// chat-profile prompt on a timer, persisted at
// $XDG_DATA_HOME/phi-agent/schedule.json, independent of any project or
// session file. `phi agent serve` owns the only Scheduler that ticks; the
// CLI verbs open a second, non-ticking one (Host == nil) purely to edit the
// same file.

// maxRuns is the cap on ScheduleFile.Runs (§5.7: "last 200 across jobs").
const maxRuns = 200

// When is a job's recurrence rule. Kind selects which fields apply:
// "once" (At), "daily" (Time, Weekdays) or "interval" (Minutes).
type When struct {
	Kind     string     `json:"kind"`
	At       *time.Time `json:"at,omitempty"`
	Time     string     `json:"time,omitempty"`     // "HH:MM"
	Weekdays []int      `json:"weekdays,omitempty"` // 1=Mon…7=Sun; empty = every day
	Minutes  int        `json:"minutes,omitempty"`
}

// RunRecord is one fired (or skipped) occurrence of a Job, kept both as
// Job.Last and in ScheduleFile.Runs.
type RunRecord struct {
	Job     string    `json:"job"`
	At      time.Time `json:"at"`
	Status  string    `json:"status"` // ok | error | capped | skipped | running
	Session string    `json:"session,omitempty"`
	Cost    float64   `json:"cost"`
	Error   string    `json:"error,omitempty"`
}

// Job is one scheduled agent run.
type Job struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Prompt  string `json:"prompt"`
	Profile string `json:"profile"`
	Project string `json:"project"`

	Model    string `json:"model,omitempty"`
	Thinking string `json:"thinking,omitempty"`

	When    When    `json:"when"`
	Target  string  `json:"target"` // "new" | "session:<id>"
	MaxCost float64 `json:"maxCost"`
	Enabled bool    `json:"enabled"`

	Next *time.Time `json:"next"` // computed, null when nothing is due
	Last *RunRecord `json:"last"`
}

// ScheduleFile is the whole on-disk schedule.json.
type ScheduleFile struct {
	Jobs []Job       `json:"jobs"`
	Runs []RunRecord `json:"runs"`
}

// SchedulePath is $XDG_DATA_HOME/phi-agent/schedule.json.
func SchedulePath() (string, error) {
	root, err := DataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "schedule.json"), nil
}

// LoadSchedule reads schedule.json. A missing file is not an error: it
// reads as an empty schedule with non-nil slices, so callers and their
// `--json` output never have to special-case a null jobs/runs array.
func LoadSchedule() (ScheduleFile, error) {
	path, err := SchedulePath()
	if err != nil {
		return ScheduleFile{}, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ScheduleFile{Jobs: []Job{}, Runs: []RunRecord{}}, nil
	}
	if err != nil {
		return ScheduleFile{}, err
	}
	var f ScheduleFile
	if err := json.Unmarshal(b, &f); err != nil {
		return ScheduleFile{}, fmt.Errorf("schedule.json: %w", err)
	}
	if f.Jobs == nil {
		f.Jobs = []Job{}
	}
	if f.Runs == nil {
		f.Runs = []RunRecord{}
	}
	return f, nil
}

// SaveSchedule writes schedule.json atomically (temp file + rename, so a
// reader never observes a truncated file) at 0600: prompts and titles are
// the user's own text, not meant for other accounts on the machine.
func SaveSchedule(f ScheduleFile) error {
	path, err := SchedulePath()
	if err != nil {
		return err
	}
	if f.Jobs == nil {
		f.Jobs = []Job{}
	}
	if f.Runs == nil {
		f.Runs = []RunRecord{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// parseHHMM parses a "HH:MM" time-of-day, the one format When.Time accepts.
func parseHHMM(s string) (hour, min int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid time %q: want HH:MM", s)
	}
	return t.Hour(), t.Minute(), nil
}

// isoWeekday converts time.Weekday (Sunday=0) to the 1=Mon…7=Sun numbering
// When.Weekdays uses, matching how the panel and the plan (§5.7) count days.
func isoWeekday(w time.Weekday) int {
	if w == time.Sunday {
		return 7
	}
	return int(w)
}

// weekdayAllowed reports whether wd (1..7) is in allowed, treating an empty
// list as "every day" (§5.7).
func weekdayAllowed(allowed []int, wd int) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == wd {
			return true
		}
	}
	return false
}

// ValidateJob checks the fields a client can set (never id/next/last).
func ValidateJob(j Job) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(j.Title)); n < 1 || n > 80 {
		return fmt.Errorf("title must be 1-80 characters")
	}
	if strings.TrimSpace(j.Prompt) == "" {
		return fmt.Errorf("prompt must not be empty")
	}
	if j.Profile != string(General) && j.Profile != string(Academic) {
		return fmt.Errorf("profile must be %q or %q (schedule targets a panel chat)", General, Academic)
	}
	if j.Project != "" {
		if err := checkSegment(j.Project); err != nil {
			return err
		}
	}
	switch j.When.Kind {
	case "once":
		if j.When.At == nil {
			return fmt.Errorf("once: at is required")
		}
	case "daily":
		if _, _, err := parseHHMM(j.When.Time); err != nil {
			return err
		}
		for _, wd := range j.When.Weekdays {
			if wd < 1 || wd > 7 {
				return fmt.Errorf("daily: weekday %d out of range 1-7", wd)
			}
		}
	case "interval":
		if j.When.Minutes < 5 || j.When.Minutes > 10080 {
			return fmt.Errorf("interval: minutes must be 5-10080")
		}
	default:
		return fmt.Errorf("unknown schedule kind %q (want once, daily or interval)", j.When.Kind)
	}
	if j.Target != "new" {
		if !strings.HasPrefix(j.Target, "session:") || strings.TrimPrefix(j.Target, "session:") == "" {
			return fmt.Errorf(`target must be "new" or "session:<id>"`)
		}
	}
	if j.MaxCost < 0 {
		return fmt.Errorf("maxCost must be >= 0")
	}
	return nil
}

// NextRun computes a job's next occurrence strictly after `after`, in
// after's location. Returns nil when the job has nothing left to schedule
// (a "once" job whose At has passed, or an unparseable rule).
func NextRun(j Job, after time.Time) *time.Time {
	loc := after.Location()
	switch j.When.Kind {
	case "once":
		if j.When.At == nil {
			return nil
		}
		t := j.When.At.In(loc)
		if !t.After(after) {
			return nil
		}
		return &t

	case "daily":
		hour, min, err := parseHHMM(j.When.Time)
		if err != nil {
			return nil
		}
		// time.Date normalises an out-of-range day (e.g. day 31 of a
		// 30-day month) itself, and folds a DST transition into the
		// wall-clock hour/min the same way the user's clock would — no
		// manual offset arithmetic needed.
		for offset := 0; offset <= 7; offset++ {
			cand := time.Date(after.Year(), after.Month(), after.Day()+offset, hour, min, 0, 0, loc)
			if !cand.After(after) {
				continue
			}
			if weekdayAllowed(j.When.Weekdays, isoWeekday(cand.Weekday())) {
				return &cand
			}
		}
		return nil

	case "interval":
		if j.When.Minutes <= 0 {
			return nil
		}
		step := time.Duration(j.When.Minutes) * time.Minute
		base := after
		if j.Last != nil {
			base = j.Last.At
		}
		next := base.In(loc).Add(step)
		for !next.After(after) {
			next = next.Add(step)
		}
		return &next

	default:
		return nil
	}
}

// NewJobID returns 8 lowercase hex characters (4 random bytes) — short
// enough for a CLI argument, long enough that two jobs never collide.
func NewJobID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read does not fail on any platform phi targets; this
		// is a last-resort fallback, not a path under test.
		return fmt.Sprintf("%08x", uint32(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b)
}

// SpentToday sums Runs.Cost for runs that landed on now's local calendar
// date, the figure the daily cap (§5.7) is checked against.
func SpentToday(f ScheduleFile, now time.Time) float64 {
	y, m, d := now.Date()
	var total float64
	for _, r := range f.Runs {
		ry, rm, rd := r.At.In(now.Location()).Date()
		if ry == y && rm == m && rd == d {
			total += r.Cost
		}
	}
	return total
}

// --- runner ------------------------------------------------------------

// ScheduleHost is how the Scheduler reaches the running server: the global
// on/off + daily cap prefs, and starting/aborting a session. Kept as an
// interface (rather than a concrete *Server) so schedule.go stays free of
// HTTP and pi-process concerns; serve.go implements it.
type ScheduleHost interface {
	Enabled() (enabled bool, dailyCap float64)
	StartRun(j Job) (sessionID string, err error)
	Abort(sessionID string) error
}

// Scheduler runs the schedule: a periodic Tick fires due jobs through Host,
// and ObserveCost/ObserveIdle (fed by the session's own usage/idle events)
// close them out. Create/Update/Delete/List work with Host == nil, so the
// CLI verbs can edit schedule.json without a live serve process.
type Scheduler struct {
	Host ScheduleHost
	Now  func() time.Time // defaults to time.Now; overridden by tests

	// OnChange fires after every save (a job fired, a run recorded, a CRUD
	// edit) so the panel's SSE feed can push a fresh /schedule. OnFired
	// fires only when Tick or RunNow actually starts a session, letting
	// the caller wire the new session into its own event stream.
	OnChange func()
	OnFired  func(jobID, session string)

	mu      sync.Mutex
	running map[string]string // sessionID -> jobID, for sessions this Scheduler started
}

// NewScheduler returns a Scheduler; h may be nil (CLI use, §5.7).
func NewScheduler(h ScheduleHost) *Scheduler {
	return &Scheduler{Host: h, Now: time.Now}
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Start runs Tick(true) once immediately (startup catch-up), then Tick(false)
// every interval, until stop is called.
func (s *Scheduler) Start(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		_ = s.Tick(true)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_ = s.Tick(false)
			}
		}
	}()
	return func() { close(done) }
}

// recordRun sets job.Last and appends rec to f.Runs, trimming to maxRuns.
// Used for a due job's first outcome (Tick's fire, or a skip that never
// became a session).
func recordRun(f *ScheduleFile, job *Job, rec RunRecord) {
	job.Last = &rec
	f.Runs = append(f.Runs, rec)
	if len(f.Runs) > maxRuns {
		f.Runs = append([]RunRecord(nil), f.Runs[len(f.Runs)-maxRuns:]...)
	}
}

// updateRunBySession replaces the Runs entry Tick recorded as "running" for
// rec.Session with its final outcome, instead of appending a duplicate. A
// session ObserveCost/ObserveIdle hears about but that has no such entry
// (schedule.json was edited or lost underneath the running session) falls
// back to recordRun so the outcome is not silently dropped.
func updateRunBySession(f *ScheduleFile, job *Job, rec RunRecord) {
	for i := len(f.Runs) - 1; i >= 0; i-- {
		if f.Runs[i].Session == rec.Session {
			f.Runs[i] = rec
			job.Last = &rec
			return
		}
	}
	recordRun(f, job, rec)
}

func findJob(f *ScheduleFile, id string) *Job {
	for i := range f.Jobs {
		if f.Jobs[i].ID == id {
			return &f.Jobs[i]
		}
	}
	return nil
}

func hasJobID(f ScheduleFile, id string) bool {
	return findJob(&f, id) != nil
}

// Tick evaluates every enabled, due job once. startup marks the one call
// Start makes right after the process comes up, which changes how a job
// that was due while nothing was running is handled (§5.7: fire once if
// less than an hour late, otherwise record it skipped and move on).
func (s *Scheduler) Tick(startup bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	f, err := LoadSchedule()
	if err != nil {
		return err
	}

	var globalEnabled bool
	var dailyCap float64
	if s.Host != nil {
		globalEnabled, dailyCap = s.Host.Enabled()
	}

	changed := false
	for i := range f.Jobs {
		job := &f.Jobs[i]
		if !job.Enabled || job.Next == nil || job.Next.After(now) {
			continue
		}
		due := *job.Next
		changed = true

		switch {
		case startup && now.Sub(due) > time.Hour:
			recordRun(&f, job, RunRecord{Job: job.ID, At: now, Status: "skipped", Error: "missed while the engine was stopped"})

		case !globalEnabled:
			// Scheduler is off: leave no run trace, just reschedule so
			// this job does not keep coming up due on every tick.

		case dailyCap > 0 && SpentToday(f, now)+job.MaxCost > dailyCap:
			recordRun(&f, job, RunRecord{Job: job.ID, At: now, Status: "skipped", Error: "daily cap reached"})

		case s.Host == nil:
			recordRun(&f, job, RunRecord{Job: job.ID, At: now, Status: "error", Error: "no scheduler host configured"})

		default:
			session, startErr := s.Host.StartRun(*job)
			if startErr != nil {
				recordRun(&f, job, RunRecord{Job: job.ID, At: now, Status: "error", Error: startErr.Error()})
				break
			}
			if s.running == nil {
				s.running = map[string]string{}
			}
			s.running[session] = job.ID
			recordRun(&f, job, RunRecord{Job: job.ID, At: now, Status: "running", Session: session})
			if s.OnFired != nil {
				s.OnFired(job.ID, session)
			}
		}

		job.Next = NextRun(*job, now)
		if job.When.Kind == "once" {
			job.Next = nil
			job.Enabled = false
		}
	}

	if !changed {
		return nil
	}
	if err := SaveSchedule(f); err != nil {
		return err
	}
	if s.OnChange != nil {
		s.OnChange()
	}
	return nil
}

// RunNow starts jobID immediately, ignoring both the global on/off and the
// job's own Enabled — the panel's explicit "run now" action (§5.7:
// POST /schedule/{id}/run "runs now, even when disabled").
func (s *Scheduler) RunNow(jobID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Host == nil {
		return "", fmt.Errorf("no scheduler host configured")
	}
	f, err := LoadSchedule()
	if err != nil {
		return "", err
	}
	job := findJob(&f, jobID)
	if job == nil {
		return "", fmt.Errorf("no such job: %q", jobID)
	}

	now := s.now()
	session, err := s.Host.StartRun(*job)
	if err != nil {
		recordRun(&f, job, RunRecord{Job: job.ID, At: now, Status: "error", Error: err.Error()})
		_ = SaveSchedule(f)
		if s.OnChange != nil {
			s.OnChange()
		}
		return "", err
	}
	if s.running == nil {
		s.running = map[string]string{}
	}
	s.running[session] = job.ID
	recordRun(&f, job, RunRecord{Job: job.ID, At: now, Status: "running", Session: session})
	if err := SaveSchedule(f); err != nil {
		return session, err
	}
	if s.OnChange != nil {
		s.OnChange()
	}
	if s.OnFired != nil {
		s.OnFired(job.ID, session)
	}
	return session, nil
}

// ObserveCost is fed a running session's latest cost (session.stats). When
// that session belongs to a scheduled job with a positive MaxCost and cost
// has exceeded it, the run is aborted and recorded "capped".
func (s *Scheduler) ObserveCost(sessionID string, cost float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobID, ok := s.running[sessionID]
	if !ok {
		return
	}
	f, err := LoadSchedule()
	if err != nil {
		return
	}
	job := findJob(&f, jobID)
	if job == nil || job.MaxCost <= 0 || cost <= job.MaxCost {
		return
	}
	if s.Host != nil {
		_ = s.Host.Abort(sessionID)
	}
	delete(s.running, sessionID)
	updateRunBySession(&f, job, RunRecord{Job: jobID, At: s.now(), Status: "capped", Session: sessionID, Cost: cost})
	if err := SaveSchedule(f); err != nil {
		return
	}
	if s.OnChange != nil {
		s.OnChange()
	}
}

// ObserveIdle is fed a session's end-of-turn state. When that session
// belongs to a scheduled job still marked running, its outcome is recorded
// ok/error and it is dropped from running. A session ObserveCost already
// capped (and removed from running) is left alone.
func (s *Scheduler) ObserveIdle(sessionID string, failed bool, cost float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobID, ok := s.running[sessionID]
	if !ok {
		return
	}
	delete(s.running, sessionID)

	f, err := LoadSchedule()
	if err != nil {
		return
	}
	job := findJob(&f, jobID)
	if job == nil {
		return
	}
	status := "ok"
	if failed {
		status = "error"
	}
	updateRunBySession(&f, job, RunRecord{Job: jobID, At: s.now(), Status: status, Session: sessionID, Cost: cost})
	if err := SaveSchedule(f); err != nil {
		return
	}
	if s.OnChange != nil {
		s.OnChange()
	}
}

// IsScheduled reports the job id a running session belongs to, or "" when
// the session was not started by this Scheduler.
func (s *Scheduler) IsScheduled(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[sessionID]
}

// List returns the schedule file as-is (§5.7 GET /schedule reshapes it with
// the global prefs; this is the jobs+runs half).
func (s *Scheduler) List() (ScheduleFile, error) {
	return LoadSchedule()
}

// Create validates j, assigns a fresh id, computes Next and appends it.
func (s *Scheduler) Create(j Job) (Job, error) {
	if err := ValidateJob(j); err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := LoadSchedule()
	if err != nil {
		return Job{}, err
	}
	id := NewJobID()
	for hasJobID(f, id) {
		id = NewJobID()
	}
	j.ID = id
	j.Last = nil
	j.Next = NextRun(j, s.now())
	f.Jobs = append(f.Jobs, j)
	if err := SaveSchedule(f); err != nil {
		return Job{}, err
	}
	if s.OnChange != nil {
		s.OnChange()
	}
	return j, nil
}

// Update validates j and replaces job id with it, keeping its id and Last
// (a client edits the rule, not the history) and recomputing Next.
func (s *Scheduler) Update(id string, j Job) (Job, error) {
	if err := ValidateJob(j); err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := LoadSchedule()
	if err != nil {
		return Job{}, err
	}
	existing := findJob(&f, id)
	if existing == nil {
		return Job{}, fmt.Errorf("no such job: %q", id)
	}
	j.ID = id
	j.Last = existing.Last
	j.Next = NextRun(j, s.now())
	*existing = j
	if err := SaveSchedule(f); err != nil {
		return Job{}, err
	}
	if s.OnChange != nil {
		s.OnChange()
	}
	return j, nil
}

// Delete removes job id.
func (s *Scheduler) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := LoadSchedule()
	if err != nil {
		return err
	}
	idx := -1
	for i := range f.Jobs {
		if f.Jobs[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no such job: %q", id)
	}
	f.Jobs = append(f.Jobs[:idx], f.Jobs[idx+1:]...)
	if err := SaveSchedule(f); err != nil {
		return err
	}
	if s.OnChange != nil {
		s.OnChange()
	}
	return nil
}
