// Package scheduler implements WorkBuddy-style scheduled automations: tasks
// that run a prompt through the conversation engine on a schedule (interval or
// daily-at-time), with persistent task definitions and a run history.
//
// Schedule formats (kept deliberately simple, no cron dependency):
//   - "every:30m"  — run every 30 minutes (supports s/m/h/d units)
//   - "every:6h"   — run every 6 hours
//   - "daily:09:00" — run once per day at 09:00 local time
//   - "idle"        — run during the configured off-peak window (智谱 Idle
//     Tasks parity): heavy/non-urgent work deferred to low-traffic hours.
package scheduler

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// Task is a persisted automation definition.
type Task struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prompt    string    `json:"prompt"`
	Schedule  string    `json:"schedule"` // "every:30m" | "daily:09:00" | "idle"
	Enabled   bool      `json:"enabled"`
	LastRun   time.Time `json:"last_run"`
	NextRun   time.Time `json:"next_run"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RunRecord is one execution result.
type RunRecord struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"` // running | ok | error
	Output     string    `json:"output"` // truncated (max 4000 chars)
	Error      string    `json:"error,omitempty"`
}

// Store is the persistence interface the scheduler needs. Implemented by the
// SQLite-backed db.Store (with its own tables) so the scheduler stays UI-free.
type Store interface {
	LoadAutomations() ([]Task, error)
	SaveAutomation(t Task) error
	DeleteAutomation(id string) error
	AppendAutomationRun(r RunRecord) error
	ListAutomationRuns(taskID string, limit int) ([]RunRecord, error)
}

// Engine is the subset of the conversation engine the scheduler needs.
type Engine interface {
	Send(ctx context.Context, sessionID, content string, attachments ...[]types.Attachment) (<-chan types.StreamEvent, error)
}

// SessionFactory creates a fresh session for a scheduled run and returns its
// ID. Implemented against types.SessionStore by the app layer.
type SessionFactory func() (string, error)

// Scheduler runs due automations in the background.
type Scheduler struct {
	mu      sync.Mutex
	tasks   map[string]*Task
	store   Store
	engine  Engine
	newSess SessionFactory
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// idleStart / idleEnd define the off-peak window ("HH:MM", 24h) in which
	// "idle" tasks run. Default 00:00–06:00. idleStart > idleEnd wraps midnight.
	idleStart string
	idleEnd   string
}

const maxOutput = 4000

var durRe = regexp.MustCompile(`^every:(\d+)\s*([smhd])$`)
var dailyRe = regexp.MustCompile(`^daily:(\d{1,2}):(\d{2})$`)

// New creates a scheduler (not yet started). Load persisted tasks lazily.
// newSess creates the throwaway session each run executes in; pass nil to
// disable execution (list-only mode).
func New(store Store, engine Engine, newSess SessionFactory) *Scheduler {
	return &Scheduler{
		store: store, engine: engine, newSess: newSess, tasks: make(map[string]*Task),
		idleStart: "00:00", idleEnd: "06:00",
	}
}

// SetIdleWindow configures the off-peak window for "idle" tasks ("HH:MM").
// idleStart > idleEnd expresses a window that wraps midnight.
func (s *Scheduler) SetIdleWindow(start, end string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if start != "" {
		s.idleStart = start
	}
	if end != "" {
		s.idleEnd = end
	}
}

// inIdleWindow reports whether now falls inside the off-peak window.
func inIdleWindow(now time.Time, start, end string) bool {
	if start == "" || end == "" {
		return false
	}
	cur := now.Format("15:04")
	if start <= end {
		return cur >= start && cur <= end
	}
	// Wraps midnight.
	return cur >= start || cur <= end
}

// nextIdleStart returns the next time the idle window begins (>= from).
func nextIdleStart(from time.Time, start string) time.Time {
	var hh, mm int
	if _, err := fmt.Sscanf(start, "%d:%d", &hh, &mm); err != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		hh, mm = 0, 0
	}
	next := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, from.Location())
	if !next.After(from) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// Start begins the background tick loop. Loads persisted tasks first.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return // already running
	}
	inner, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	if s.store != nil {
		if ts, err := s.store.LoadAutomations(); err == nil {
			for i := range ts {
				s.tasks[ts[i].ID] = &ts[i]
			}
		} else {
			log.Printf("[scheduler] load automations: %v", err)
		}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-inner.Done():
				return
			case now := <-tick.C:
				s.runDue(now)
			}
		}
	}()
}

// Stop halts the tick loop.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// runDue executes every enabled task whose NextRun has passed. "idle" tasks
// additionally require being inside the off-peak window; outside it they are
// re-queued to the next window start.
func (s *Scheduler) runDue(now time.Time) {
	s.mu.Lock()
	var due []*Task
	for _, t := range s.tasks {
		if !t.Enabled || t.NextRun.IsZero() || now.Before(t.NextRun) {
			continue
		}
		if t.Schedule == "idle" && !inIdleWindow(now, s.idleStart, s.idleEnd) {
			t.NextRun = nextIdleStart(now, s.idleStart)
			continue
		}
		due = append(due, t)
	}
	s.mu.Unlock()
	for _, t := range due {
		s.runTask(t.ID)
	}
}

// List returns all tasks sorted by name.
func (s *Scheduler) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a task by ID (copy).
func (s *Scheduler) Get(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

// Create registers a new task. Returns the created task.
func (s *Scheduler) Create(name, prompt, schedule string) (Task, error) {
	if strings.TrimSpace(name) == "" {
		return Task{}, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return Task{}, fmt.Errorf("prompt is required")
	}
	next, err := nextRunAfter(schedule, time.Now(), s.idleStart)
	if err != nil {
		return Task{}, err
	}
	now := time.Now()
	t := Task{
		ID:        fmt.Sprintf("auto-%d", now.UnixNano()),
		Name:      strings.TrimSpace(name),
		Prompt:    strings.TrimSpace(prompt),
		Schedule:  strings.TrimSpace(schedule),
		Enabled:   true,
		NextRun:   next,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.tasks[t.ID] = &t
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveAutomation(t); err != nil {
			log.Printf("[scheduler] persist create %s: %v", t.ID, err)
		}
	}
	return t, nil
}

// Update patches a task (name/prompt/schedule/enabled). Recomputes NextRun
// when the schedule or enabled state changed to true.
func (s *Scheduler) Update(id string, patch Task) (Task, error) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return Task{}, fmt.Errorf("task %q not found", id)
	}
	origSchedule, origEnabled := t.Schedule, t.Enabled
	if patch.Name != "" {
		t.Name = patch.Name
	}
	if patch.Prompt != "" {
		t.Prompt = patch.Prompt
	}
	if patch.Schedule != "" {
		if next, err := nextRunAfter(patch.Schedule, time.Now(), s.idleStart); err != nil {
			s.mu.Unlock()
			return Task{}, err
		} else {
			t.Schedule = patch.Schedule
			t.NextRun = next
		}
	}
	if patch.Enabled {
		t.Enabled = true
	}
	if patch.Enabled == false {
		t.Enabled = false
	}
	if patch.Enabled && !origEnabled {
		if next, err := nextRunAfter(t.Schedule, time.Now(), s.idleStart); err == nil {
			t.NextRun = next
		}
	}
	if t.Enabled && t.Schedule == origSchedule && t.Schedule == "" {
		// keep as-is
	}
	t.UpdatedAt = time.Now()
	out := *t
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveAutomation(out); err != nil {
			log.Printf("[scheduler] persist update %s: %v", id, err)
		}
	}
	return out, nil
}

// Delete removes a task.
func (s *Scheduler) Delete(id string) error {
	s.mu.Lock()
	_, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("task %q not found", id)
	}
	delete(s.tasks, id)
	s.mu.Unlock()
	if s.store != nil {
		return s.store.DeleteAutomation(id)
	}
	return nil
}

// RunNow executes a task immediately regardless of schedule.
func (s *Scheduler) RunNow(id string) (RunRecord, error) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return RunRecord{}, fmt.Errorf("task %q not found", id)
	}
	taskCopy := *t
	s.mu.Unlock()
	return s.runTask(taskCopy.ID), nil
}

// History returns recent run records for a task.
func (s *Scheduler) History(taskID string, limit int) ([]RunRecord, error) {
	if s.store == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.ListAutomationRuns(taskID, limit)
}

// runTask executes one task through the engine and records the result.
// Returns the record (also persisted) so callers can surface it.
func (s *Scheduler) runTask(id string) RunRecord {
	s.mu.Lock()
	t, ok := s.tasks[id]
	s.mu.Unlock()
	if !ok {
		return RunRecord{TaskID: id, Status: "error", Error: "task not found"}
	}
	rec := RunRecord{
		ID:        fmt.Sprintf("run-%d", time.Now().UnixNano()),
		TaskID:    id,
		StartedAt: time.Now(),
		Status:    "running",
	}
	s.persistRun(rec)

	// Run the prompt through the engine in a fresh session so automations
	// never touch the user's active chat history.
	sessID := ""
	if s.newSess != nil {
		if s2, err := s.newSess(); err == nil {
			sessID = s2
		}
	}
	if sessID == "" {
		sessID = fmt.Sprintf("auto-sess-%d", time.Now().UnixNano())
	}

	var out strings.Builder
	runErr := ""
	if s.engine != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		ch, err := s.engine.Send(ctx, sessID, t.Prompt)
		if err != nil {
			runErr = err.Error()
		} else {
			for ev := range ch {
				if ev.Type == types.EventText {
					out.WriteString(ev.Content)
					if out.Len() > maxOutput*2 {
						cancel()
						break
					}
				} else if ev.Type == types.EventError {
					if ev.Content != "" {
						runErr = ev.Content
					}
				}
			}
		}
		cancel()
	} else {
		runErr = "engine unavailable"
	}

	text := out.String()
	if len(text) > maxOutput {
		text = text[:maxOutput] + "\n…(截断)"
	}
	rec.Output = text
	rec.Error = runErr
	rec.FinishedAt = time.Now()
	if runErr != "" {
		rec.Status = "error"
	} else {
		rec.Status = "ok"
	}

	// Update task LastRun + recompute NextRun.
	s.mu.Lock()
	if cur, ok := s.tasks[id]; ok {
		cur.LastRun = rec.StartedAt
		if cur.Enabled {
			if next, err := nextRunAfter(cur.Schedule, rec.StartedAt, s.idleStart); err == nil {
				cur.NextRun = next
			}
		}
		cur.UpdatedAt = time.Now()
		if s.store != nil {
			_ = s.store.SaveAutomation(*cur)
		}
	}
	s.mu.Unlock()

	s.persistRun(rec)
	log.Printf("[scheduler] task %s (%s) → %s in %s", id, t.Name, rec.Status, time.Since(rec.StartedAt).Round(time.Millisecond))
	return rec
}

func (s *Scheduler) persistRun(r RunRecord) {
	if s.store == nil {
		return
	}
	if err := s.store.AppendAutomationRun(r); err != nil {
		log.Printf("[scheduler] persist run: %v", err)
	}
}

// nextRunAfter computes the next fire time from a schedule string. idleStart
// is the off-peak window start ("HH:MM") used for "idle" schedules.
func nextRunAfter(schedule string, from time.Time, idleStart string) (time.Time, error) {
	sched := strings.TrimSpace(strings.ToLower(schedule))
	if sched == "idle" {
		return nextIdleStart(from, idleStart), nil
	}
	if m := durRe.FindStringSubmatch(sched); m != nil {
		var unit time.Duration
		switch m[2] {
		case "s":
			unit = time.Second
		case "m":
			unit = time.Minute
		case "h":
			unit = time.Hour
		case "d":
			unit = 24 * time.Hour
		}
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		return from.Add(time.Duration(n) * unit), nil
	}
	if m := dailyRe.FindStringSubmatch(sched); m != nil {
		var hh, mm int
		fmt.Sscanf(m[1], "%d", &hh)
		fmt.Sscanf(m[2], "%d", &mm)
		if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
			return time.Time{}, fmt.Errorf("invalid daily time %q (use daily:HH:MM)", schedule)
		}
		next := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, from.Location())
		if !next.After(from) {
			next = next.Add(24 * time.Hour)
		}
		return next, nil
	}
	return time.Time{}, fmt.Errorf("invalid schedule %q (use \"every:30m\", \"daily:09:00\" or \"idle\")", schedule)
}

// DescribeSchedule returns a human-readable rendering of a schedule string.
func DescribeSchedule(schedule string) string {
	sched := strings.ToLower(strings.TrimSpace(schedule))
	if sched == "idle" {
		return "闲时（低峰窗口）"
	}
	if m := durRe.FindStringSubmatch(sched); m != nil {
		return "每 " + m[1] + " " + map[string]string{"s": "秒", "m": "分钟", "h": "小时", "d": "天"}[m[2]]
	}
	if m := dailyRe.FindStringSubmatch(sched); m != nil {
		return "每天 " + m[1] + ":" + m[2]
	}
	return schedule
}
