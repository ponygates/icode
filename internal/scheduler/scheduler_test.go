package scheduler

import (
	"testing"
	"time"
)

func TestNextRunAfter(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 30, 0, 0, time.Local)
	cases := []struct {
		sched    string
		wantDiff time.Duration
		wantErr  bool
	}{
		{"every:30m", 30 * time.Minute, false},
		{"every:6h", 6 * time.Hour, false},
		{"every:1d", 24 * time.Hour, false},
		{"every:90s", 90 * time.Second, false},
		{"daily:09:00", 22*time.Hour + 30*time.Minute, false}, // today 10:30 → tomorrow 09:00
		{"daily:11:00", 30 * time.Minute, false},              // today 10:30 → today 11:00
		{"bogus", 0, true},
		{"", 0, true},
		{"every:", 0, true},
		{"daily:99:99", 0, true},
	}
	for _, c := range cases {
		got, err := nextRunAfter(c.sched, now, "00:00")
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error, got %v", c.sched, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.sched, err)
			continue
		}
		diff := got.Sub(now)
		if diff.Round(time.Second) != c.wantDiff {
			t.Errorf("%q: diff=%v want=%v", c.sched, diff, c.wantDiff)
		}
	}
}

func TestDescribeSchedule(t *testing.T) {
	cases := map[string]string{
		"every:30m":   "每 30 分钟",
		"every:1d":    "每 1 天",
		"daily:09:00": "每天 09:00",
	}
	for in, want := range cases {
		if got := DescribeSchedule(in); got != want {
			t.Errorf("DescribeSchedule(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSchedulerCRUD(t *testing.T) {
	s := New(nil, nil, nil) // no persistence, no engine — list/create/update/delete only
	_, err := s.Create("", "prompt", "every:1h")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	task, err := s.Create("daily check", "git status", "every:1h")
	if err != nil {
		t.Fatal(err)
	}
	if !task.Enabled || task.NextRun.IsZero() {
		t.Errorf("bad defaults: %+v", task)
	}
	if len(s.List()) != 1 {
		t.Fatal("expected 1 task")
	}
	// Update schedule → recompute NextRun.
	upd, err := s.Update(task.ID, Task{Schedule: "every:30m"})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Schedule != "every:30m" {
		t.Errorf("schedule not updated: %+v", upd)
	}
	// Invalid schedule rejected.
	if _, err := s.Update(task.ID, Task{Schedule: "nope"}); err == nil {
		t.Error("expected error for invalid schedule")
	}
	if err := s.Delete(task.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("expected 0 tasks after delete")
	}
	if _, err := s.RunNow(task.ID); err == nil {
		t.Error("expected error running deleted task")
	}
}

func TestIdleSchedule(t *testing.T) {
	now := time.Date(2026, 8, 14, 10, 30, 0, 0, time.Local)
	// Outside the 00:00–06:00 window → next idle start is tomorrow 00:00.
	next, err := nextRunAfter("idle", now, "00:00")
	if err != nil {
		t.Fatalf("idle schedule err: %v", err)
	}
	want := time.Date(2026, 8, 15, 0, 0, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Fatalf("next idle = %v, want %v", next, want)
	}
	// Inside window (e.g. 03:00) → inIdleWindow true.
	if !inIdleWindow(time.Date(2026, 8, 14, 3, 0, 0, 0, time.Local), "00:00", "06:00") {
		t.Fatal("03:00 should be in 00:00–06:00 window")
	}
	if inIdleWindow(time.Date(2026, 8, 14, 12, 0, 0, 0, time.Local), "00:00", "06:00") {
		t.Fatal("12:00 should be outside 00:00–06:00 window")
	}
	// Midnight wrap: 22:00–06:00, 23:00 should be in window.
	if !inIdleWindow(time.Date(2026, 8, 14, 23, 0, 0, 0, time.Local), "22:00", "06:00") {
		t.Fatal("23:00 should be in 22:00–06:00 wrapped window")
	}
}
