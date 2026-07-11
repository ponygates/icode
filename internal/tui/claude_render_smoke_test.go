package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestClaudeStyleRender verifies the Claude Code-style single-column layout:
// the compact header (✻ iCode), the bottom status bar (model · ctx · cost),
// the bordered input box, and the sliding thinking bar all render without
// panicking and contain the expected structural markers.
func TestClaudeStyleRender(t *testing.T) {
	tui := New(Config{Model: "deepseek-v4-flash", Provider: "deepseek", Lang: "zh-CN", Theme: "dark"})

	var buf bytes.Buffer
	tui.writer = &buf
	tui.rawMode = true
	tui.color = true
	tui.width = 120
	tui.height = 40

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
	if !strings.Contains(out, "✻ iCode") {
		t.Fatalf("expected Claude Code-style header '✻ iCode' in output:\n%s", out)
	}
	if !strings.Contains(out, "ctx") {
		t.Fatalf("expected context %%-meter ('ctx') in status bar:\n%s", out)
	}
	if !strings.Contains(out, "$0.0123") {
		t.Fatalf("expected cost in status bar:\n%s", out)
	}
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╰") {
		t.Fatalf("expected bordered input box (╭/╰) in output:\n%s", out)
	}
	if !strings.Contains(out, "●") {
		t.Fatalf("expected model status dot '●' in output:\n%s", out)
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

// TestWelcomeScreen verifies the Claude Code-style startup banner: the big
// ASCII iCode logo plus the model/dir info, and that dismissWelcome() hides it.
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
	tui.messages = nil // fresh session

	// Case 1: banner should be visible on a fresh session.
	tui.render()
	out := buf.String()
	if !strings.Contains(out, "Welcome to iCode") {
		t.Fatalf("expected 'Welcome to iCode' tagline in welcome screen:\n%s", out)
	}
	// The wordmark uses plain ASCII (no block/box-drawing glyphs) so it
	// renders crisply on Windows conhost and CJK terminals.
	if !strings.Contains(out, "___") || !strings.Contains(out, `/ _ \`) {
		t.Fatalf("expected plain ASCII iCode logo in welcome screen:\n%s", out)
	}
	// Info is shown as indented lines (Claude Code style), not a framed box,
	// to avoid border-alignment artifacts.
	if !strings.Contains(out, "Model:") || !strings.Contains(out, "cwd:") {
		t.Fatalf("expected model/cwd info in welcome screen:\n%s", out)
	}

	// Case 2: dismiss should hide the banner.
	buf.Reset()
	if !tui.dismissWelcome() {
		t.Fatalf("dismissWelcome should have returned true when banner was visible")
	}
	tui.render()
	out2 := buf.String()
	if strings.Contains(out2, `/ _ \`) {
		t.Fatalf("expected welcome logo to be hidden after dismiss:\n%s", out2)
	}
}
