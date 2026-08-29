package scheduler

import (
	"strings"
	"testing"
	"time"
)

func TestParseRRule(t *testing.T) {
	r, err := parseRRule("FREQ=DAILY;INTERVAL=2;BYHOUR=9;BYMINUTE=30")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.freq != "daily" || r.interval != 2 || r.byHour[0] != 9 || r.byMinute[0] != 30 {
		t.Errorf("unexpected rrule: %+v", r)
	}
	if _, err := parseRRule("FREQ=YEARLY"); err == nil {
		t.Errorf("expected error for unsupported FREQ")
	}
	if _, err := parseRRule("INTERVAL=5"); err == nil {
		t.Errorf("expected error for missing FREQ")
	}
}

func TestRRuleSecondly(t *testing.T) {
	r, _ := parseRRule("FREQ=SECONDLY;INTERVAL=30")
	from := time.Date(2026, 8, 29, 12, 0, 0, 500, time.Local)
	next := r.next(from)
	if next.Second() != 30 {
		t.Errorf("next = %v, want second 30", next)
	}
	if !next.After(from) {
		t.Errorf("next must be after from")
	}
}

func TestRRuleDaily(t *testing.T) {
	r, _ := parseRRule("FREQ=DAILY;BYHOUR=9;BYMINUTE=0")
	// Before 09:00 → same day 09:00.
	from := time.Date(2026, 8, 29, 8, 30, 0, 0, time.Local)
	next := r.next(from)
	if next.Day() != 29 || next.Hour() != 9 {
		t.Errorf("next = %v, want 08-29 09:00", next)
	}
	// After 09:00 → next day 09:00.
	from2 := time.Date(2026, 8, 29, 12, 0, 0, 0, time.Local)
	next2 := r.next(from2)
	if next2.Day() != 30 || next2.Hour() != 9 {
		t.Errorf("next2 = %v, want 08-30 09:00", next2)
	}
}

func TestRRuleWeeklyByDay(t *testing.T) {
	r, _ := parseRRule("FREQ=WEEKLY;BYDAY=MO,TH;BYHOUR=9;BYMINUTE=30")
	// Friday 2026-08-28 is not MO/TH; next is Monday 08-31.
	from := time.Date(2026, 8, 28, 10, 0, 0, 0, time.Local)
	next := r.next(from)
	if next.Weekday() != time.Monday || next.Hour() != 9 || next.Minute() != 30 {
		t.Errorf("next = %v, want Monday 09:30", next)
	}
}

func TestRRuleNextRunAfter(t *testing.T) {
	from := time.Date(2026, 8, 29, 0, 0, 0, 0, time.Local)
	next, err := nextRunAfter("rrule:FREQ=SECONDLY;INTERVAL=15", from, "00:00")
	if err != nil {
		t.Fatalf("nextRunAfter: %v", err)
	}
	if next.Sub(from) != 15*time.Second {
		t.Errorf("next = %v, want +15s", next)
	}
}

func TestDescribeRRule(t *testing.T) {
	r, _ := parseRRule("FREQ=WEEKLY;BYDAY=MO,FR;BYHOUR=9")
	d := describeRRule(r)
	if !strings.Contains(d, "周") || !strings.Contains(d, "周一") || !strings.Contains(d, "周五") || !strings.Contains(d, "9 点") {
		t.Errorf("describe = %q", d)
	}
}
