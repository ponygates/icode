package tui

import (
	"bytes"
	"regexp"
	"testing"
)

// sgrRe matches SGR (Select Graphic Rendition) escape sequences — color/style
// codes like "\x1b[1m" or "\x1b[0m". Terminal control codes such as "\x1b[2J"
// (clear) or "\x1b[?25h" (show cursor) are NOT SGR and are expected even when
// color is disabled.
var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func hasSGR(b []byte) bool {
	return sgrRe.Match(b)
}

// TestRenderRawNoPanic drives the raw-mode render path (with a buffer as the
// writer, no real terminal needed) to catch panics that would manifest as a
// "flash close" when the user runs the CLI in a real TTY and a message streams.
func TestRenderRawNoPanic(t *testing.T) {
	for _, w := range []int{20, 40, 80, 120} {
		tui := &TUI{
			mode:      ModeAgent,
			model:     "deepseek-v4-flash",
			provider:  "deepseek",
			lang:      "zh-CN",
			theme:     "dark",
			rawMode:   true,
			color:     true,
			width:     w,
			height:    30,
			streamDone: make(chan struct{}, 1),
		}
		tui.writer = &bytes.Buffer{}
		tui.messages = []Message{
			{Role: RoleUser, Content: "帮我写个函数"},
			{Role: RoleThinking, Content: "我需要先理解需求，再决定实现方式。"},
			{Role: RoleAssistant, Content: "# 标题\n这是 **粗体** 和 `代码` 以及 *斜体*。\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n- 列表项一\n- 列表项二\n\n> 引用一行很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长\n\n[链接](https://example.com)\n\n普通段落带中文测试宽度是否计算正确。"},
			{Role: RoleTool, Tool: "bash", ToolArgs: "ls -la /tmp", Content: "file1\nfile2\nfile3"},
			{Role: RoleError, Content: "出错了：权限不足"},
			{Role: RoleSystem, Content: "会话已清空。"},
		}
		// Initial render must not panic.
		tui.render()

		// Simulated streaming of a partial (unclosed) code fence + markdown.
		tui.streaming = true
		tui.streamBuf.Reset()
		tui.streamBuf.WriteString("正在生成 ```python\nprint('hello'\n未完成的内容 **粗体")
		tui.render()

		// Permissive prompt path.
		tui.permPending = true
		tui.permPrompt = "是否允许执行 rm -rf /tmp/test？"
		tui.render()
		tui.permPending = false

		tui.EndStream()
	}
}

// TestRenderNoColor ensures the colorless (color=false) path also renders
// without panic and without emitting escape codes.
func TestRenderNoColor(t *testing.T) {
	tui := &TUI{
		mode:       ModePlan,
		model:      "gpt-4o",
		provider:   "openai",
		lang:       "en",
		theme:      "light",
		rawMode:    true,
		color:      false,
		width:      80,
		height:     24,
		streamDone: make(chan struct{}, 1),
	}
	var buf bytes.Buffer
	tui.writer = &buf
	tui.messages = []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "**bold** and `code`"},
	}
	tui.render()
	if hasSGR(buf.Bytes()) {
		t.Errorf("color disabled but SGR color codes present: %q", buf.String())
	}
}
