package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/core/checkpoint"
)

// TestReplayOverlayRendering verifies the timeline overlay: title, entry rows
// (newest-first step numbers), highlight marker, and the internal scroll
// window keeping the highlighted row visible.
func TestReplayOverlayRendering(t *testing.T) {
	tu := &TUI{theme: "dark", lang: "zh-CN", color: true}
	base := time.Date(2026, 9, 17, 10, 0, 0, 0, time.Local)
	var entries []checkpoint.Entry
	for i := 0; i < 12; i++ {
		entries = append(entries, checkpoint.Entry{
			Hash:    strings.Repeat("h", 8),
			Message: "step message",
			When:    base.Add(time.Duration(i) * time.Minute),
		})
	}
	tu.replayList = entries
	tu.replayIdx = 0
	tu.replayTop = 0

	lines := tu.replayOverlay(100, 14)
	joined := strings.Join(lines, "\n")

	// Title + newest-first numbering: index 0 renders as step #11 (n-1-i).
	if !strings.Contains(joined, "检查点时间轴") {
		t.Fatalf("title missing:\n%s", joined)
	}
	if !strings.Contains(joined, "#11") {
		t.Fatalf("newest entry should be step #11:\n%s", joined)
	}

	// Highlight scrolls the internal window when the cursor moves deep.
	tu.replayIdx = 11 // oldest entry (step #0)
	lines = tu.replayOverlay(100, 14)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "▶") {
		t.Fatalf("highlight marker missing:\n%s", joined)
	}
	if !strings.Contains(joined, "#0 ") {
		t.Fatalf("oldest entry (step #0) should be visible after scroll:\n%s", joined)
	}
	if len(lines) > 14 {
		t.Fatalf("overlay must not exceed bodyH: got %d rows:\n%s", len(lines), joined)
	}
}

// TestReplayOverlayShortList fits without the more-rows indicator.
func TestReplayOverlayShortList(t *testing.T) {
	tu := &TUI{theme: "dark", lang: "zh-CN"}
	tu.replayList = []checkpoint.Entry{
		{Hash: "a", Message: "first", When: time.Now()},
		{Hash: "b", Message: "second", When: time.Now()},
	}
	tu.replayIdx = 0
	lines := tu.replayOverlay(100, 14)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "first") || !strings.Contains(joined, "second") {
		t.Fatalf("entries missing in short list:\n%s", joined)
	}
	if !strings.Contains(joined, "Enter: 单步 diff") {
		t.Fatalf("shortcut footer missing:\n%s", joined)
	}
}

// TestTruncRunes checks the CJK-safe truncation helper.
func TestTruncRunes(t *testing.T) {
	if got := truncRunes("hello", 10); got != "hello" {
		t.Fatalf("short string should pass through: %q", got)
	}
	if got := truncRunes("长长长长长", 3); got != "长长长…" {
		t.Fatalf("rune-based truncation broken: %q", got)
	}
	if got := truncRunes("  padded  ", 10); got != "padded" {
		t.Fatalf("should trim spaces: %q", got)
	}
}
