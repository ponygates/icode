package scheduler

import (
	"fmt"
	"time"
)

type Mode string

const (
	ModeAggressiveSave Mode = "aggressive_save"
	ModeFullQuality    Mode = "full_quality"
	ModeBalanced       Mode = "balanced"
)

type PeakSlot struct {
	Start string
	End   string
	Days  []time.Weekday
	Mode  Mode
}

type Scheduler struct {
	Enabled    bool
	Peaks      []PeakSlot
	LowMode    Mode
	NormalMode Mode
	LowStart   string
	LowEnd     string
}

func New(enabled bool) *Scheduler {
	return &Scheduler{
		Enabled:    enabled,
		NormalMode: ModeBalanced,
	}
}

func (s *Scheduler) CurrentMode() Mode {
	if !s.Enabled {
		return s.NormalMode
	}

	now := time.Now()
	currentTime := fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute())
	currentDay := now.Weekday()

	// Check low hours first
	if s.LowStart != "" && s.LowEnd != "" {
		if isTimeBetween(currentTime, s.LowStart, s.LowEnd) {
			return s.LowMode
		}
	}

	// Check peak hours
	for _, peak := range s.Peaks {
		if !dayMatches(currentDay, peak.Days) {
			continue
		}
		if isTimeBetween(currentTime, peak.Start, peak.End) {
			return peak.Mode
		}
	}

	return s.NormalMode
}

func (s *Scheduler) ShouldPruneAggressively() bool {
	return s.CurrentMode() == ModeAggressiveSave
}

func (s *Scheduler) SummaryInterval() int {
	switch s.CurrentMode() {
	case ModeAggressiveSave:
		return 3
	case ModeFullQuality:
		return 10
	default:
		return 5
	}
}

func (s *Scheduler) MaxHistoryRounds() int {
	switch s.CurrentMode() {
	case ModeAggressiveSave:
		return 30
	case ModeFullQuality:
		return 100
	default:
		return 50
	}
}

func isTimeBetween(current, start, end string) bool {
	return current >= start && current < end
}

var dayNames = map[string]time.Weekday{
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
	"sun": time.Sunday,
}

func dayMatches(day time.Weekday, days []string) bool {
	for _, d := range days {
		if wd, ok := dayNames[d]; ok && wd == day {
			return true
		}
	}
	return false
}
