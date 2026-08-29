package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Lightweight RRULE support (WorkBuddy parity subset) for scheduled tasks.
// Schedule format: "rrule:FREQ=SECONDLY|MINUTELY|HOURLY|DAILY|WEEKLY|MONTHLY;
// INTERVAL=n; BYHOUR=9; BYMINUTE=30; BYDAY=MO,WE,FR"
// Examples:
//
//	rrule:FREQ=SECONDLY;INTERVAL=30   — every 30 seconds (second-level precision)
//	rrule:FREQ=DAILY;BYHOUR=9;BYMINUTE=0
//	rrule:FREQ=WEEKLY;BYDAY=MO,TH;BYHOUR=9;BYMINUTE=30
type rrule struct {
	freq     string // secondly | minutely | hourly | daily | weekly | monthly
	interval int    // default 1
	byHour   []int
	byMinute []int
	byDay    map[time.Weekday]bool
}

var weekdayMap = map[string]time.Weekday{
	"MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday, "SU": time.Sunday,
}

// parseRRule parses an RRULE string (after the "rrule:" prefix).
func parseRRule(s string) (*rrule, error) {
	r := &rrule{interval: 1, byDay: map[time.Weekday]bool{}}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(strings.ToUpper(part))
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("rrule: bad token %q", part)
		}
		key, val := kv[0], kv[1]
		switch key {
		case "FREQ":
			switch strings.ToLower(val) {
			case "secondly", "minutely", "hourly", "daily", "weekly", "monthly":
				r.freq = strings.ToLower(val)
			default:
				return nil, fmt.Errorf("rrule: unsupported FREQ %q", val)
			}
		case "INTERVAL":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("rrule: bad INTERVAL %q", val)
			}
			r.interval = n
		case "BYHOUR":
			for _, h := range strings.Split(val, ",") {
				n, err := strconv.Atoi(strings.TrimSpace(h))
				if err != nil || n < 0 || n > 23 {
					return nil, fmt.Errorf("rrule: bad BYHOUR %q", val)
				}
				r.byHour = append(r.byHour, n)
			}
		case "BYMINUTE":
			for _, m := range strings.Split(val, ",") {
				n, err := strconv.Atoi(strings.TrimSpace(m))
				if err != nil || n < 0 || n > 59 {
					return nil, fmt.Errorf("rrule: bad BYMINUTE %q", val)
				}
				r.byMinute = append(r.byMinute, n)
			}
		case "BYDAY":
			for _, d := range strings.Split(val, ",") {
				d = strings.TrimSpace(d)
				if wd, ok := weekdayMap[d]; ok {
					r.byDay[wd] = true
				}
			}
		default:
			// Ignore unknown parts so future RRULE keys don't break parsing.
		}
	}
	if r.freq == "" {
		return nil, fmt.Errorf("rrule: missing FREQ")
	}
	return r, nil
}

// next returns the next occurrence at or after from.
func (r *rrule) next(from time.Time) time.Time {
	switch r.freq {
	case "secondly":
		return addAligned(from, time.Second, r.interval)
	case "minutely":
		return addAligned(from, time.Minute, r.interval)
	case "hourly":
		return addAligned(from, time.Hour, r.interval)
	case "daily":
		return r.nextDaily(from)
	case "weekly":
		return r.nextWeekly(from)
	case "monthly":
		return r.nextMonthly(from)
	}
	return from.Add(time.Hour)
}

// addAligned snaps to the unit boundary then steps interval units forward,
// guaranteeing a time strictly after from.
func addAligned(from time.Time, unit time.Duration, interval int) time.Time {
	base := from.Truncate(unit)
	next := base.Add(time.Duration(interval) * unit)
	if !next.After(from) {
		next = next.Add(time.Duration(interval) * unit)
	}
	return next
}

func (r *rrule) hour0() int {
	if len(r.byHour) > 0 {
		return r.byHour[0]
	}
	return 0
}

func (r *rrule) min0() int {
	if len(r.byMinute) > 0 {
		return r.byMinute[0]
	}
	return 0
}

// hhmm reports whether the time-of-day matches BYHOUR/BYMINUTE (empty = any).
func (r *rrule) hhmm(d time.Time) bool {
	if len(r.byHour) > 0 {
		ok := false
		for _, h := range r.byHour {
			if d.Hour() == h {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(r.byMinute) > 0 {
		for _, m := range r.byMinute {
			if d.Minute() == m {
				return true
			}
		}
		return false
	}
	return true
}

func (r *rrule) nextDaily(from time.Time) time.Time {
	for d := from; ; d = d.Add(24 * time.Hour) {
		cand := time.Date(d.Year(), d.Month(), d.Day(), r.hour0(), r.min0(), 0, 0, from.Location())
		if !cand.Before(from) && r.hhmm(cand) {
			return cand
		}
	}
}

func (r *rrule) nextWeekly(from time.Time) time.Time {
	days := r.byDay
	if len(days) == 0 {
		days = map[time.Weekday]bool{from.Weekday(): true}
	}
	for d := from; ; d = d.Add(24 * time.Hour) {
		if !days[d.Weekday()] {
			continue
		}
		cand := time.Date(d.Year(), d.Month(), d.Day(), r.hour0(), r.min0(), 0, 0, from.Location())
		if !cand.Before(from) && r.hhmm(cand) {
			return cand
		}
	}
}

func (r *rrule) nextMonthly(from time.Time) time.Time {
	day := from.Day()
	for d := from; ; d = d.AddDate(0, 1, 0) {
		cand := time.Date(d.Year(), d.Month(), day, r.hour0(), r.min0(), 0, 0, from.Location())
		if !cand.Before(from) && r.hhmm(cand) {
			return cand
		}
	}
}

// describeRRule renders a human-readable description for the task panel.
func describeRRule(r *rrule) string {
	freq := map[string]string{
		"secondly": "秒", "minutely": "分钟", "hourly": "小时",
		"daily": "天", "weekly": "周", "monthly": "月",
	}[r.freq]
	prefix := "每"
	if r.interval > 1 {
		prefix = "每 " + strconv.Itoa(r.interval)
	}
	s := prefix + " " + freq
	if len(r.byHour) > 0 {
		s += " " + strconv.Itoa(r.byHour[0]) + " 点"
		if len(r.byMinute) > 0 {
			s += strconv.Itoa(r.byMinute[0]) + " 分"
		}
	}
	if len(r.byDay) > 0 {
		names := []string{}
		order := []time.Weekday{
			time.Monday, time.Tuesday, time.Wednesday, time.Thursday,
			time.Friday, time.Saturday, time.Sunday,
		}
		zh := map[time.Weekday]string{
			time.Monday: "周一", time.Tuesday: "周二", time.Wednesday: "周三",
			time.Thursday: "周四", time.Friday: "周五", time.Saturday: "周六",
			time.Sunday: "周日",
		}
		for _, wd := range order {
			if r.byDay[wd] {
				names = append(names, zh[wd])
			}
		}
		s += "（" + strings.Join(names, "、") + "）"
	}
	return s
}
