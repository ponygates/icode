package tui

import (
	"strings"
	"testing"
)

// TestInlineMarkupNoLeak ensures inline Markdown markers (backticks, bold,
// italic) never leak into rendered output in either colour mode. Regression
// for "CLI 版对话界面 Markdown 格式不支持" — the line-mode (non-TTY) path
// previously dumped raw Markdown syntax into the conversation.
func TestInlineMarkupNoLeak(t *testing.T) {
	for _, color := range []bool{false, true} {
		tt := &TUI{color: color}
		lines := tt.renderMarkdown("用 `rm -rf` 删除，**加粗**，*斜体*", "  ", "  ", 60)
		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "`rm") || strings.Contains(joined, "**加粗") {
			t.Errorf("color=%v markup leaked: %q", color, joined)
		}
		if !strings.Contains(joined, "rm -rf") || !strings.Contains(joined, "加粗") {
			t.Errorf("color=%v content missing: %q", color, joined)
		}
	}
}

// TestPrintAssistantRendersMarkdown covers the line-mode path: a non-streamed
// assistant message must be Markdown-rendered (headings, code fences) instead
// of printed as raw text.
func TestPrintAssistantRendersMarkdown(t *testing.T) {
	tt := &TUI{color: false, writer: &strings.Builder{}}
	tt.printAssistant("## 标题\n\n```go\nfmt.Println(1)\n```")
	out := tt.writer.(*strings.Builder).String()
	if !strings.Contains(out, "标题") || !strings.Contains(out, "fmt.Println") {
		t.Errorf("assistant markdown not rendered in line mode: %q", out)
	}
	if strings.Contains(out, "## ") {
		t.Errorf("heading marker leaked: %q", out)
	}
}
