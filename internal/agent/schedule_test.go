package agent

import (
	"fmt"
	"testing"
	"time"
)

// --- NextRun -------------------------------------------------------------

func TestNextRunDailyAcrossMidnightAndWeekday(t *testing.T) {
	after := time.Date(2026, 9, 28, 23, 30, 0, 0, time.UTC)

	// The 00:15 slot today has already passed; the next occurrence is
	// tomorrow, crossing midnight.
	j := Job{When: When{Kind: "daily", Time: "00:15"}}
	got := NextRun(j, after)
	want := time.Date(2026, 9, 29, 0, 15, 0, 0, time.UTC)
	if got == nil || !got.Equal(want) {
		t.Errorf("across midnight: got %v, want %v", got, want)
	}

	// Restrict to a weekday two days out: today's and tomorrow's weekdays
	// must be skipped even though 09:00 has not passed on either of them.
	targetDay := after.AddDate(0, 0, 2)
	targetWD := isoWeekday(targetDay.Weekday())
	j2 := Job{When: When{Kind: "daily", Time: "09:00", Weekdays: []int{targetWD}}}
	got2 := NextRun(j2, after)
	want2 := time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(), 9, 0, 0, 0, time.UTC)
	if got2 == nil || !got2.Equal(want2) {
		t.Errorf("weekday filter: got %v, want %v", got2, want2)
	}
}

func TestNextRunInterval(t *testing.T) {
	after := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

	// No Last yet: the base is `after` itself.
	j := Job{When: When{Kind: "interval", Minutes: 60}}
	got := NextRun(j, after)
	want := after.Add(60 * time.Minute)
	if got == nil || !got.Equal(want) {
		t.Errorf("no Last: got %v, want %v", got, want)
	}

	// Last is far enough in the past that one interval still lands before
	// `after`; NextRun must advance by whole intervals, not just one.
	last := after.Add(-95 * time.Minute)
	j.Last = &RunRecord{At: last}
	got2 := NextRun(j, after)
	want2 := after.Add(25 * time.Minute) // last +60m is -35m (still <= after); +60m again is +25m
	if got2 == nil || !got2.Equal(want2) {
		t.Errorf("with Last: got %v, want %v", got2, want2)
	}
}

func TestNextRunOnce(t *testing.T) {
	after := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

	future := after.Add(time.Hour)
	j := Job{When: When{Kind: "once", At: &future}}
	got := NextRun(j, after)
	if got == nil || !got.Equal(future) {
		t.Errorf("future once: got %v, want %v", got, future)
	}

	past := after.Add(-time.Hour)
	j2 := Job{When: When{Kind: "once", At: &past}}
	if got := NextRun(j2, after); got != nil {
		t.Errorf("past once: got %v, want nil", got)
	}
}

// --- ValidateJob -----------------------------------------------------------

func validJob() Job {
	return Job{
		Title:   "digest",
		Prompt:  "summarise today",
		Profile: string(General),
		When:    When{Kind: "interval", Minutes: 60},
		Target:  "new",
	}
}

func TestValidateJobFailures(t *testing.T) {
	cases := map[string]func(*Job){
		"empty title":       func(j *Job) { j.Title = "" },
		"blank title":       func(j *Job) { j.Title = "   " },
		"empty prompt":      func(j *Job) { j.Prompt = "" },
		"bad profile":       func(j *Job) { j.Profile = "coding" },
		"path project":      func(j *Job) { j.Project = "a/b" },
		"dotdot project":    func(j *Job) { j.Project = ".." },
		"once no at":        func(j *Job) { j.When = When{Kind: "once"} },
		"daily bad time":    func(j *Job) { j.When = When{Kind: "daily", Time: "9am"} },
		"daily bad weekday": func(j *Job) { j.When = When{Kind: "daily", Time: "09:00", Weekdays: []int{0}} },
		"interval too low":  func(j *Job) { j.When = When{Kind: "interval", Minutes: 1} },
		"interval too high": func(j *Job) { j.When = When{Kind: "interval", Minutes: 10081} },
		"unknown kind":      func(j *Job) { j.When = When{Kind: "weekly"} },
		"bad target":        func(j *Job) { j.Target = "resume" },
		"empty session id":  func(j *Job) { j.Target = "session:" },
		"negative maxCost":  func(j *Job) { j.MaxCost = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			j := validJob()
			mutate(&j)
			if err := ValidateJob(j); err == nil {
				t.Errorf("%s: want error, got nil", name)
			}
		})
	}
}

func TestValidateJobAccepts(t *testing.T) {
	j := validJob()
	if err := ValidateJob(j); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	j.Target = "session:abc123"
	if err := ValidateJob(j); err != nil {
		t.Fatalf("valid resume target rejected: %v", err)
	}
	j.Profile = string(Academic)
	if err := ValidateJob(j); err != nil {
		t.Fatalf("academic profile rejected: %v", err)
	}
}

// --- Scheduler runner ------------------------------------------------------

// fakeHost is a ScheduleHost recording what the Scheduler asked it to do,
// for tests that never touch a real pi process.
type fakeHost struct {
	enabled  bool
	dailyCap float64
	startErr error

	started []Job
	aborted []string
	nextID  int
}

func (h *fakeHost) Enabled() (bool, float64) { return h.enabled, h.dailyCap }

func (h *fakeHost) StartRun(j Job) (string, error) {
	if h.startErr != nil {
		return "", h.startErr
	}
	h.started = append(h.started, j)
	h.nextID++
	return fmt.Sprintf("sess-%d", h.nextID), nil
}

func (h *fakeHost) Abort(sessionID string) error {
	h.aborted = append(h.aborted, sessionID)
	return nil
}

// seedSchedule sets up XDG_DATA_HOME and writes f as the starting
// schedule.json.
func seedSchedule(t *testing.T, f ScheduleFile) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := SaveSchedule(f); err != nil {
		t.Fatal(err)
	}
}

// dueJob returns an enabled interval job whose Next is exactly `at` — due
// the instant Tick runs at `at`.
func dueJob(id string, at time.Time) Job {
	next := at
	return Job{
		ID: id, Title: "t", Prompt: "p", Profile: string(General),
		When: When{Kind: "interval", Minutes: 60}, Target: "new",
		Enabled: true, Next: &next,
	}
}

func TestTickFiresDueJobAndAdvances(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	seedSchedule(t, ScheduleFile{Jobs: []Job{dueJob("j1", now)}})

	host := &fakeHost{enabled: true}
	s := NewScheduler(host)
	s.Now = func() time.Time { return now }
	changed := false
	s.OnChange = func() { changed = true }

	if err := s.Tick(false); err != nil {
		t.Fatal(err)
	}
	if len(host.started) != 1 || host.started[0].ID != "j1" {
		t.Fatalf("StartRun calls = %v, want one for j1", host.started)
	}
	if !changed {
		t.Error("OnChange not called")
	}
	if got := s.IsScheduled("sess-1"); got != "j1" {
		t.Errorf("IsScheduled(sess-1) = %q, want j1", got)
	}

	f, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	job := findJob(&f, "j1")
	if job == nil {
		t.Fatal("job j1 missing after tick")
	}
	if job.Last == nil || job.Last.Status != "running" || job.Last.Session != "sess-1" {
		t.Errorf("job.Last = %+v, want running/sess-1", job.Last)
	}
	if job.Next == nil || !job.Next.After(now) {
		t.Errorf("job.Next = %v, want a time after %v", job.Next, now)
	}
	if len(f.Runs) != 1 || f.Runs[0].Status != "running" {
		t.Errorf("Runs = %+v, want one running entry", f.Runs)
	}
}

func TestTickDisabledAdvancesWithoutFiring(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	seedSchedule(t, ScheduleFile{Jobs: []Job{dueJob("j1", now)}})

	host := &fakeHost{enabled: false}
	s := NewScheduler(host)
	s.Now = func() time.Time { return now }

	if err := s.Tick(false); err != nil {
		t.Fatal(err)
	}
	if len(host.started) != 0 {
		t.Errorf("StartRun called %d times, want 0", len(host.started))
	}
	f, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Runs) != 0 {
		t.Errorf("Runs = %+v, want none", f.Runs)
	}
	job := findJob(&f, "j1")
	if job == nil || job.Next == nil || !job.Next.After(now) {
		t.Errorf("job.Next = %v, want advanced past %v", job.Next, now)
	}
}

func TestTickStartupCatchUp(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

	t.Run("less than an hour late fires", func(t *testing.T) {
		seedSchedule(t, ScheduleFile{Jobs: []Job{dueJob("j1", now.Add(-30*time.Minute))}})
		host := &fakeHost{enabled: true}
		s := NewScheduler(host)
		s.Now = func() time.Time { return now }
		if err := s.Tick(true); err != nil {
			t.Fatal(err)
		}
		if len(host.started) != 1 {
			t.Errorf("StartRun called %d times, want 1", len(host.started))
		}
	})

	t.Run("more than an hour late is skipped", func(t *testing.T) {
		seedSchedule(t, ScheduleFile{Jobs: []Job{dueJob("j1", now.Add(-90*time.Minute))}})
		host := &fakeHost{enabled: true}
		s := NewScheduler(host)
		s.Now = func() time.Time { return now }
		if err := s.Tick(true); err != nil {
			t.Fatal(err)
		}
		if len(host.started) != 0 {
			t.Errorf("StartRun called %d times, want 0", len(host.started))
		}
		f, err := s.List()
		if err != nil {
			t.Fatal(err)
		}
		job := findJob(&f, "j1")
		if job == nil || job.Last == nil || job.Last.Status != "skipped" {
			t.Errorf("job.Last = %+v, want skipped", job.Last)
		}
	})
}

func TestTickDailyCapSkip(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	job := dueJob("j1", now)
	job.MaxCost = 0.5
	seedSchedule(t, ScheduleFile{
		Jobs: []Job{job},
		Runs: []RunRecord{{Job: "other", At: now.Add(-time.Hour), Status: "ok", Cost: 0.9}},
	})

	host := &fakeHost{enabled: true, dailyCap: 1.0}
	s := NewScheduler(host)
	s.Now = func() time.Time { return now }

	if err := s.Tick(false); err != nil {
		t.Fatal(err)
	}
	if len(host.started) != 0 {
		t.Errorf("StartRun called %d times, want 0 (cap reached)", len(host.started))
	}
	f, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	j := findJob(&f, "j1")
	if j == nil || j.Last == nil || j.Last.Status != "skipped" || j.Last.Error != "daily cap reached" {
		t.Errorf("job.Last = %+v, want skipped/daily cap reached", j.Last)
	}
}

func TestOnceJobDisablesItself(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	at := now
	job := Job{
		ID: "j1", Title: "t", Prompt: "p", Profile: string(General),
		When: When{Kind: "once", At: &at}, Target: "new",
		Enabled: true, Next: &at,
	}
	seedSchedule(t, ScheduleFile{Jobs: []Job{job}})

	host := &fakeHost{enabled: true}
	s := NewScheduler(host)
	s.Now = func() time.Time { return now }
	if err := s.Tick(false); err != nil {
		t.Fatal(err)
	}

	f, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	j := findJob(&f, "j1")
	if j == nil {
		t.Fatal("job j1 missing")
	}
	if j.Enabled {
		t.Error("once job still enabled after firing")
	}
	if j.Next != nil {
		t.Errorf("once job Next = %v, want nil", j.Next)
	}
}

func TestObserveCostAbortsAndRecordsCapped(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	job := dueJob("j1", now)
	job.MaxCost = 0.5
	seedSchedule(t, ScheduleFile{Jobs: []Job{job}})

	host := &fakeHost{enabled: true}
	s := NewScheduler(host)
	s.Now = func() time.Time { return now }
	if err := s.Tick(false); err != nil {
		t.Fatal(err)
	}

	s.ObserveCost("sess-1", 0.75)

	if len(host.aborted) != 1 || host.aborted[0] != "sess-1" {
		t.Errorf("aborted = %v, want [sess-1]", host.aborted)
	}
	if got := s.IsScheduled("sess-1"); got != "" {
		t.Errorf("IsScheduled(sess-1) = %q after capping, want \"\"", got)
	}
	f, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Runs) != 1 || f.Runs[0].Status != "capped" || f.Runs[0].Cost != 0.75 {
		t.Errorf("Runs = %+v, want one capped/0.75 entry", f.Runs)
	}
	j := findJob(&f, "j1")
	if j == nil || j.Last == nil || j.Last.Status != "capped" {
		t.Errorf("job.Last = %+v, want capped", j.Last)
	}

	// An unknown session (not tracked as running) must be a no-op.
	s.ObserveCost("sess-does-not-exist", 999)
	if len(host.aborted) != 1 {
		t.Errorf("aborted = %v, want still just [sess-1]", host.aborted)
	}
}

func TestObserveIdleRecordsOk(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	seedSchedule(t, ScheduleFile{Jobs: []Job{dueJob("j1", now)}})

	host := &fakeHost{enabled: true}
	s := NewScheduler(host)
	s.Now = func() time.Time { return now }
	if err := s.Tick(false); err != nil {
		t.Fatal(err)
	}

	s.ObserveIdle("sess-1", false, 0.12)

	if got := s.IsScheduled("sess-1"); got != "" {
		t.Errorf("IsScheduled(sess-1) = %q after idle, want \"\"", got)
	}
	f, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Runs) != 1 || f.Runs[0].Status != "ok" || f.Runs[0].Cost != 0.12 {
		t.Errorf("Runs = %+v, want one ok/0.12 entry", f.Runs)
	}

	// A second idle notification for the same (now unscheduled) session
	// must be a no-op, not a duplicate record.
	s.ObserveIdle("sess-1", true, 5)
	f2, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(f2.Runs) != 1 {
		t.Errorf("Runs after repeat idle = %+v, want still one entry", f2.Runs)
	}
}

// --- CRUD, nil host --------------------------------------------------------

func TestScheduleCRUDNilHost(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := NewScheduler(nil)

	created, err := s.Create(validJob())
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Error("Create did not assign an id")
	}
	if created.Next == nil {
		t.Error("Create did not compute Next")
	}

	f, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Jobs) != 1 {
		t.Fatalf("Jobs = %v, want 1", f.Jobs)
	}

	edited := created
	edited.Title = "renamed"
	updated, err := s.Update(created.ID, edited)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID {
		t.Errorf("Update changed id: %s -> %s", created.ID, updated.ID)
	}
	if updated.Title != "renamed" {
		t.Errorf("updated.Title = %q, want renamed", updated.Title)
	}

	if _, err := s.Update("no-such-id", validJob()); err == nil {
		t.Error("Update on unknown id: want error")
	}

	if err := s.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(created.ID); err == nil {
		t.Error("Delete on already-deleted id: want error")
	}

	f2, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(f2.Jobs) != 0 {
		t.Errorf("Jobs after delete = %v, want none", f2.Jobs)
	}
}

func TestLoadScheduleMissingFileIsEmptyNonNil(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	f, err := LoadSchedule()
	if err != nil {
		t.Fatal(err)
	}
	if f.Jobs == nil || f.Runs == nil {
		t.Errorf("LoadSchedule on a missing file: Jobs=%v Runs=%v, want non-nil empty slices", f.Jobs, f.Runs)
	}
}
