package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/permission"
)

// renderPermBox paints one approval box at the given severity and returns
// the frame output, so the colour tests share a single construction path.
func renderPermBox(t *testing.T, severity string) string {
	t.Helper()
	tu := &TUI{
		mode:       ModeAgent,
		model:      "test",
		provider:   "test",
		lang:       "zh-CN",
		theme:      "dark",
		rawMode:    true,
		color:      true,
		width:      80,
		height:     30,
		streamDone: make(chan struct{}, 1),
	}
	tu.writer = &bytes.Buffer{}
	tu.permPending = true
	tu.permPrompt = "是否允许执行？"
	tu.permSeverity = severity
	tu.render()
	return tu.writer.(*bytes.Buffer).String()
}

// TestRenderPermSeverityHigh: a high-risk ask (rm -rf, git push --force)
// must switch the box to red and carry the ⚠ prefix — the visual difference
// is the feature, so assert both.
func TestRenderPermSeverityHigh(t *testing.T) {
	out := renderPermBox(t, permission.SeverityHigh)
	if !strings.Contains(out, "\x1b[31m") {
		t.Fatalf("high severity must paint the border red (SGR 31)")
	}
	if !strings.Contains(out, "⚠") {
		t.Fatalf("high severity title must carry the ⚠ prefix")
	}
}

// TestRenderPermSeverityMedium: the default stays the familiar yellow.
func TestRenderPermSeverityMedium(t *testing.T) {
	out := renderPermBox(t, permission.SeverityMedium)
	if !strings.Contains(out, "\x1b[33m") {
		t.Fatalf("medium severity must paint the border yellow (SGR 33)")
	}
	if strings.Contains(out, "⚠") {
		t.Fatalf("medium severity must not carry the ⚠ prefix")
	}
}

// TestRenderPermSeverityLow: read-tier confirms render calm cyan.
func TestRenderPermSeverityLow(t *testing.T) {
	out := renderPermBox(t, permission.SeverityLow)
	if !strings.Contains(out, "\x1b[36m") {
		t.Fatalf("low severity must paint the border cyan (SGR 36)")
	}
}
