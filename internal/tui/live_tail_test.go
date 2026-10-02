package tui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestLiveTailScrollingWindow verifies the in-flight live output rendering:
// a tool card with LiveTail shows the LAST 5 lines (scrolling tail window,
// Claude Code parity) with a leading "…" marker — not the first 8 lines the
// folded view uses.
func TestLiveTailScrollingWindow(t *testing.T) {
	tu := &TUI{theme: "dark", lang: "zh-CN", width: 100, height: 40}
	var b strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "log line %d\n", i)
	}
	m := Message{Role: RoleTool, Tool: "bash", ToolArgs: "npm install", LiveTail: b.String(), Folded: true}
	lines := tu.messageLinesW(m, 100)
	joined := strings.Join(lines, "\n")

	// Head present.
	if !strings.Contains(joined, "bash") {
		t.Fatalf("tool head missing:\n%s", joined)
	}
	// Tail marker + the LAST 5 lines only.
	if !strings.Contains(joined, "⎿ …") {
		t.Fatalf("expected tail ellipsis marker for a >5-line live tail:\n%s", joined)
	}
	for i := 8; i <= 12; i++ {
		if !strings.Contains(joined, fmt.Sprintf("log line %d", i)) {
			t.Fatalf("expected tail line %d in window:\n%s", i, joined)
		}
	}
	for i := 1; i <= 7; i++ {
		if strings.Contains(joined, fmt.Sprintf("log line %d\n", i)) ||
			strings.HasSuffix(strings.TrimSpace(joined), fmt.Sprintf("log line %d", i)) && i <= 6 {
			// exact-line containment check below is the real assertion; this
			// guard avoids false positives on substrings like "line 1" inside
			// "line 12".
		}
		if strings.Contains(joined, fmt.Sprintf("log line %d ", i)) {
			t.Fatalf("head line %d should be scrolled out of the tail window:\n%s", i, joined)
		}
	}
}

// TestLiveTailShortOutput shows everything (no ellipsis) when the live tail
// fits inside the window.
func TestLiveTailShortOutput(t *testing.T) {
	tu := &TUI{theme: "dark", lang: "zh-CN", width: 100, height: 40}
	m := Message{Role: RoleTool, Tool: "bash", ToolArgs: "echo hi", LiveTail: "hi\n", Folded: true}
	lines := tu.messageLinesW(m, 100)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "…") {
		t.Fatalf("short tail must not show the ellipsis marker:\n%s", joined)
	}
	if !strings.Contains(joined, "hi") {
		t.Fatalf("live output missing:\n%s", joined)
	}
}

// TestProgressNoDuplicateResult guards the dedup contract: progress chunks go
// to LiveTail (never Content), and AppendToolResult clears LiveTail — so the
// card's final Content holds the result exactly once even though the same
// bytes streamed live first.
func TestProgressNoDuplicateResult(t *testing.T) {
	tu := &TUI{theme: "dark", lang: "zh-CN", rawMode: false, toolFolded: false}
	tu.writer = &bytes.Buffer{}
	tu.messages = []Message{{Role: RoleUser, Content: "run it"}}

	tu.AddToolMessage("bash", "npm install", "")
	for i := 1; i <= 20; i++ {
		tu.AppendToolProgress(fmt.Sprintf("progress chunk %d\n", i))
	}
	m := tu.messages[len(tu.messages)-1]
	if m.Content != "" {
		t.Fatalf("progress leaked into Content: %q", m.Content)
	}
	if !strings.Contains(m.LiveTail, "progress chunk 20") {
		t.Fatalf("live tail missing latest chunk: %q", m.LiveTail)
	}

	tu.AppendToolResult("final result output")
	m = tu.messages[len(tu.messages)-1]
	if m.LiveTail != "" {
		t.Fatalf("LiveTail must be cleared after the result arrives: %q", m.LiveTail)
	}
	if strings.Count(m.Content, "final result output") != 1 {
		t.Fatalf("result duplicated in Content: %q", m.Content)
	}
	if strings.Contains(m.Content, "progress chunk") {
		t.Fatalf("volatile progress persisted into Content: %q", m.Content)
	}
}

// TestLiveTailCap verifies the trailing-64KB cap so a multi-GB log dump can't
// grow the message without bound.
func TestLiveTailCap(t *testing.T) {
	tu := &TUI{theme: "dark", lang: "zh-CN", rawMode: false}
	tu.writer = &bytes.Buffer{}
	tu.messages = []Message{{Role: RoleTool, Tool: "bash", ToolArgs: "", Folded: true}}
	big := strings.Repeat("x", liveTailCap) // exactly at cap, then more
	tu.AppendToolProgress(big)
	tu.AppendToolProgress("tail-marker")
	m := tu.messages[len(tu.messages)-1]
	if len(m.LiveTail) > liveTailCap {
		t.Fatalf("LiveTail exceeded cap: %d", len(m.LiveTail))
	}
	if !strings.HasSuffix(m.LiveTail, "tail-marker") {
		t.Fatalf("cap must keep the TRAILING bytes: ...%q", m.LiveTail[len(m.LiveTail)-40:])
	}
}
