package slashui

import (
	"strings"
	"testing"
)

func TestBuildShareHTML(t *testing.T) {
	msgs := []shareMsg{
		{Role: "user", Content: "帮我看下这段 `code` **加粗**\n- 列表项"},
		{Role: "assistant", Content: "# 标题\n\n```go\nfmt.Println(\"hi\")\n```"},
		{Role: "tool", Content: "tool output"},
	}
	page := buildShareHTML("测试会话", "deepseek-v4", msgs)

	for _, want := range []string{
		"<title>测试会话 — iCode 会话</title>",
		`<div class="msg user">`,
		`<div class="msg assistant">`,
		`<div class="msg tool">`,
		"模型: deepseek-v4",
		"String.fromCharCode(96)", // markdown renderer inlined
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Content must be HTML-escaped in the data-md attribute.
	if !strings.Contains(page, "data-md=") {
		t.Error("data-md attributes missing")
	}
	// No literal backtick-fence regex that would break Go raw strings.
	if strings.Contains(page, "/^```") {
		t.Error("raw ``` leaked into JS")
	}
}
