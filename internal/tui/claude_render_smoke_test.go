package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/core/searchreplace"
)

// TestClaudeStyleRender verifies the minimal opencode-style layout: the compact
// header (iCode + model), the bottom status bar (model · ctx · cost), the
// single-line prompt, and the sliding thinking bar all render without
// panicking and contain the expected structural markers.
func TestClaudeStyleRender(t *testing.T) {
	tui := New(Config{Model: "deepseek-v4-flash", Provider: "deepseek", Lang: "zh-CN", Theme: "dark"})

	var buf bytes.Buffer
	tui.writer = &buf
	tui.rawMode = true
	tui.color = true
	tui.width = 120
	tui.height = 40
	// Pin the status bar on: this test verifies the layout, not the /statusline
	// toggle (which persists to the real config and would otherwise flip the
	// default-visible assumption).
	tui.statusVisible = true

	// Populate conversation + status state.
	tui.model = "deepseek-v4-flash"
	tui.provider = "deepseek"
	tui.cost = "$0.0123"
	tui.contextTokens = 120000
	tui.contextWindow = 1048576
	tui.messages = []Message{
		{Role: RoleUser, Content: "帮我写一个快速排序"},
		{Role: RoleAssistant, Content: "好的，下面是用 Go 实现的快速排序：\n\n```go\nfunc quicksort(a []int) {}\n```"},
		{Role: RoleTool, Tool: "bash", ToolArgs: "go build ./..."},
	}

	// Case 1: idle (not streaming) — should render conversation + status + box.
	tui.render()
	out := buf.String()
	// opencode-style header dot (●) leads the model name in the header AND the
	// model marker on the task bar below the prompt.
	if !strings.Contains(out, "●") {
		t.Fatalf("expected opencode-style model dot '●' in output:\n%s", out)
	}
	if !strings.Contains(out, "▓") || !strings.Contains(out, "░") {
		t.Fatalf("expected visual context bar (▓/░) in status bar:\n%s", out)
	}
	if !strings.Contains(out, "$0.0123") {
		t.Fatalf("expected cost in status bar:\n%s", out)
	}
	if !strings.Contains(out, "┌") || !strings.Contains(out, "└") {
		t.Fatalf("expected code-block fence (┌/└) in output:\n%s", out)
	}
	if !strings.Contains(out, "·") {
		t.Fatalf("expected task-bar dot separators '·' in output:\n%s", out)
	}
	// Minimal design: the model sits next to the wordmark in the header, and
	// context usage (120000/1048576 ≈ 11%) lives on the status bar below the
	// prompt — no duplicated header strip.
	if !strings.Contains(out, "deepseek-v4-flash") {
		t.Fatalf("expected model in header/status output:\n%s", out)
	}
	if !strings.Contains(out, "11%") {
		t.Fatalf("expected context %%-meter ('11%%') in status bar:\n%s", out)
	}

	// Case 2: streaming with no tokens yet — should show the sliding thinking bar.
	buf.Reset()
	tui.mu.Lock()
	tui.streaming = true
	tui.streamBuf.Reset()
	tui.turnStart = time.Now()
	tui.mu.Unlock()
	tui.render()
	out2 := buf.String()
	if !strings.Contains(out2, "[") || !strings.Contains(out2, "]") {
		t.Fatalf("expected thinking bar brackets in streaming output:\n%s", out2)
	}
	if !strings.Contains(out2, "生成中") {
		t.Fatalf("expected '生成中' thinking label:\n%s", out2)
	}
}

// TestWelcomeScreen verifies the Claude Code-style startup banner: the
// two-column box with "Welcome back!" on the left and tips on the right.
func TestWelcomeScreen(t *testing.T) {
	tui := New(Config{Model: "deepseek-v4-flash", Provider: "deepseek", Lang: "zh-CN", Theme: "dark"})
	var buf bytes.Buffer
	tui.writer = &buf
	tui.rawMode = true
	tui.color = true
	tui.width = 120
	tui.height = 40
	tui.model = "deepseek-v4-flash"
	tui.provider = "deepseek"
	tui.welcomeVisible = true
	tui.messages = nil

	// Case 1: banner should be visible on a fresh session.
	tui.render()
	out := buf.String()
	if !strings.Contains(out, "Welcome back!") {
		t.Fatalf("expected 'Welcome back!' in welcome screen:\n%s", out)
	}
	if !strings.Contains(out, "多模型 AI 编程助手") {
		t.Fatalf("expected LOGO tagline '多模型 AI 编程助手' in welcome screen:\n%s", out)
	}
	// Two-column layout: tips on the right, info on the left
	if !strings.Contains(out, "Tips for getting started") {
		t.Fatalf("expected tips section in welcome screen:\n%s", out)
	}
	if !strings.Contains(out, "What's new") {
		t.Fatalf("expected 'what's new' section in welcome screen:\n%s", out)
	}
	if !strings.Contains(out, "deepseek-v4-flash") {
		t.Fatalf("expected model name in welcome screen:\n%s", out)
	}

	// Case 2: dismiss should hide the banner.
	buf.Reset()
	if !tui.dismissWelcome() {
		t.Fatalf("dismissWelcome should have returned true when banner was visible")
	}
	tui.render()
	out2 := buf.String()
	if strings.Contains(out2, "Welcome back!") {
		t.Fatalf("expected welcome panel to be hidden after dismiss:\n%s", out2)
	}
}

// TestDiffBoxOverlay verifies the staged-edits review overlay renders each
// edit's file path plus its colour-coded unified diff, highlights the current
// row with ▶, and never exceeds the available body height.
func TestDiffBoxOverlay(t *testing.T) {
	tui := New(Config{Model: "deepseek-v4-flash", Provider: "deepseek", Lang: "zh-CN", Theme: "dark"})
	tui.diffBoxOpen = true
	tui.diffIdx = 0
	tui.diffEdits = []searchreplace.StagedEdit{
		{
			FilePath: "internal/core/tool/tools.go",
			Valid:    true,
			Diff:     "--- a/internal/core/tool/tools.go\n+++ b/internal/core/tool/tools.go\n@@ -1,5 +1,6 @@\n-func old() {}\n+func new() {}",
		},
		{
			FilePath: "cmd/commands.go",
			Valid:    false,
			Reason:   "search text not found",
			Diff:     "",
		},
	}

	lines := tui.diffBoxOverlay(80, 20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "tools.go") {
		t.Fatalf("expected first file path in diff overlay:\n%s", joined)
	}
	if !strings.Contains(joined, "▶") {
		t.Fatalf("expected ▶ highlight marker on the selected row:\n%s", joined)
	}
	if !strings.Contains(joined, "-func old") || !strings.Contains(joined, "+func new") {
		t.Fatalf("expected unified diff +/- lines in overlay:\n%s", joined)
	}
	if !strings.Contains(joined, "无效") {
		t.Fatalf("expected invalid-edit note in overlay:\n%s", joined)
	}
	if len(lines) > 20 {
		t.Fatalf("diff overlay exceeds body height: %d lines (max 20)", len(lines))
	}
}
