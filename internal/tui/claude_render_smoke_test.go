package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestClaudeStyleRender verifies the new split-pane layout (left explorer │
// right conversation), the compact header, the bottom status bar, and the
// sliding thinking bar all render without panicking and contain the expected
// structural markers.
func TestClaudeStyleRender(t *testing.T) {
	tui := New(Config{Model: "deepseek-v4-flash", Provider: "deepseek", Lang: "zh-CN", Theme: "dark"})

	var buf bytes.Buffer
	tui.writer = &buf
	tui.rawMode = true
	tui.color = true
	tui.width = 120
	tui.height = 40

	// Populate explorer + conversation state.
	tui.dirEntries = []string{"main.go", "tui.go", "go.mod", "README.md"}
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

	// Case 1: idle (not streaming) — should render conversation + panes.
	tui.render()
	out := buf.String()
	if !strings.Contains(out, "│") {
		t.Fatalf("expected vertical frame divider '│' in output:\n%s", out)
	}
	if !strings.Contains(out, "◆ iCode") {
		t.Fatalf("expected compact header '◆ iCode' in output:\n%s", out)
	}
	if !strings.Contains(out, "ctx") {
		t.Fatalf("expected context %%-meter ('ctx') in status bar:\n%s", out)
	}
	if !strings.Contains(out, "$0.0123") {
		t.Fatalf("expected cost in status/explorer:\n%s", out)
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
