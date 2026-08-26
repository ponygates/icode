package tui

import (
	"strings"
	"testing"
)

// renderPlain strips ANSI so assertions can match on visible text only.
func renderPlain(t *TUI, md string, width int) string {
	lines := t.renderMarkdown(md, "", "  ", width)
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(stripANSILn(ln))
		b.WriteByte('\n')
	}
	return b.String()
}

func stripANSILn(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' || r == ']' || r == '\\' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

const mdSample = "# 大标题\n## 小节\n正文 **加粗** `代码`。\n\n- 项目一\n- 项目二\n  - 嵌套项\n\n```go\nfmt.Println(\"x\")\n```\n\n| 列A | 列B |\n|---|---|\n| 1 | 2 |\n"

// Structural contract of the markdown renderer — guards the visual hierarchy
// added in v0.42.6: tiered headings, nested-list indent, code language tag,
// and non-dim table body cells.
func TestRenderMarkdownStructure(t *testing.T) {
	tui := New(Config{Model: "m", Provider: "p", Lang: "zh-CN", Theme: "dark"})
	out := renderPlain(tui, mdSample, 80)

	if !strings.Contains(out, "大标题") {
		t.Errorf("h1 text missing:\n%s", out)
	}
	if !strings.Contains(out, "▍小节") {
		t.Errorf("h2 should carry the ▍ side-marker:\n%s", out)
	}
	if strings.Count(out, "──") == 0 {
		t.Error("expected heading/hr rules")
	}

	// Nested list item is indented under its parent.
	if !strings.Contains(out, "\n    - 嵌套项") && !strings.Contains(out, "\n    - 嵌套项") {
		t.Errorf("nested list not indented:\n%s", out)
	}

	// Code fence carries the language tag.
	if !strings.Contains(out, "┌─ go ") {
		t.Errorf("code block missing go tag:\n%s", out)
	}

	// Table header present with body values following.
	if !strings.Contains(out, "列A") || !strings.Contains(out, "1") || !strings.Contains(out, "2") {
		t.Errorf("table content missing:\n%s", out)
	}
}

// ANSI colouring contract (checked on the coloured output, before stripping):
// h1 cyan-bold; h2 yellow; nested marker keeps its colour after indentation.
func TestRenderMarkdownColours(t *testing.T) {
	tui := New(Config{Model: "m", Provider: "p", Lang: "zh-CN", Theme: "dark"})
	tui.color = true
	md := "# T\n## S\n- a\n  - b\n"
	coloured := ""
	for _, ln := range tui.renderMarkdown(md, "", "", 60) {
		coloured += ln + "\n"
	}
	cyanBold := tui.c("cyan") + "\x1b[1m"
	if !strings.Contains(coloured, cyanBold+"T") {
		t.Errorf("h1 not cyan+bold:\n%q", coloured)
	}
	yellowBold := tui.c("yellow") + "\x1b[1m▍S"
	if !strings.Contains(coloured, yellowBold) {
		t.Errorf("h2 not yellow with marker:\n%q", coloured)
	}
	nested := "    " + tui.c("yellow") + "-"
	if !strings.Contains(coloured, nested) {
		t.Errorf("nested bullet not indented+coloured:\n%q", coloured)
	}
}

// Regression: bold spanning two source lines inside one paragraph must pair
// up (paragraph folding) — the model wraps long bold phrases constantly.
func TestRenderMarkdownCrossLineBold(t *testing.T) {
	tui := New(Config{Model: "m", Provider: "p", Lang: "zh-CN", Theme: "dark"})
	out := renderPlain(tui, "**第一行要点：\n第二行继续说明** 完毕", 80)
	if strings.Contains(out, "**") {
		t.Errorf("literal ** leaked:\n%s", out)
	}
	if !strings.Contains(out, "第一行要点") || !strings.Contains(out, "第二行继续说明") {
		t.Errorf("content lost:\n%s", out)
	}
}

// Regression: table cells go through inline rendering.
func TestRenderMarkdownTableInline(t *testing.T) {
	tui := New(Config{Model: "m", Provider: "p", Lang: "zh-CN", Theme: "dark"})
	out := renderPlain(tui, "| A | B |\n|---|---|\n| **重点** | 普通 |\n", 60)
	if strings.Contains(out, "**") {
		t.Errorf("literal ** in table cell:\n%s", out)
	}
	if !strings.Contains(out, "重点") || !strings.Contains(out, "普通") {
		t.Errorf("table content missing:\n%s", out)
	}
}
