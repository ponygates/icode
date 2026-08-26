package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Debug helper: eyeball the markdown renderer output for a representative
// sample. Run with: go test ./internal/tui/ -run TestDumpMD -v
func TestDumpMD(t *testing.T) {
	tui := New(Config{Model: "m", Provider: "p", Lang: "zh-CN", Theme: "dark"})
	tui.color = true
	md := "# 一级标题\n## 二级标题\n\n普通段落，含 **粗体**、*斜体*、`行内代码` 和 [链接](https://example.com)。\n\n- 列表项一\n- 列表项二\n  - 嵌套项 A\n  - 嵌套项 B\n1. 有序一\n2. 有序二\n\n> 引用行一\n> 引用行二\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\") // 注释\n}\n```\n\n| 列A | 列B |\n|---|---|\n| 1 | 2 |\n\n---\n\n- [ ] 待办未完成\n- [x] 待办已完成\n"
	lines := tui.renderMarkdown(md, "", "  ", 80)
	for _, ln := range lines {
		fmt.Println("|" + strings.TrimRight(ln, " ") + "|")
	}
}
