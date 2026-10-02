package tui

import (
	"strings"
	"testing"
)

// This file pins the fix for the "long CJK paragraph truncated at both edges"
// bug (reported on Windows Terminal): wrapANSI/wrapPrefixed used to wrap ALL
// segments against the first-line prefix width, so every continuation line
// carrying the wider continuation indent overran the terminal by
// (contW-prefixW) cells. The implicit hard wrap then desynchronised every
// following row — visible as "right edge truncated, left edge of the next
// lines missing". The invariant under test: NO produced line may exceed the
// requested width in true display cells (CJK=2, emoji=2, VS16=0).

// lineWidth is the terminal's own yardstick: strip styling, measure cells.
func lineWidth(s string) int {
	return runeWidthStr(stripANSI(s))
}

// TestWrapANSIContinuationFitsWidth exercises the two extreme prefix/cont
// shapes: an empty first-line prefix with a wide continuation ("  ") and the
// user prompt ("❯ " vs "    ") — the old code overran both by 2 cells per
// line. (Assistant messages now align at "  "/"  ", but the unequal-width
// capability itself must stay correct for user prompts.)
func TestWrapANSIContinuationFitsWidth(t *testing.T) {
	tui := &TUI{color: true, width: 80}
	long := strings.Repeat("这是一段用于回归测试的中文长文本段落，验证续行缩进宽度。", 10)

	for _, width := range []int{40, 60, 80, 120} {
		cases := []struct {
			name   string
			prefix string
			cont   string
		}{
			{"assistant-empty-prefix", "", "  "},
			{"user-prompt", tui.paint("orange", "❯ "), "    "},
		}
		for _, tc := range cases {
			lines := tui.wrapANSI(tc.prefix, tc.cont, long, width)
			if len(lines) < 3 {
				t.Fatalf("%s@%d: expected multiple wrapped lines, got %d", tc.name, width, len(lines))
			}
			for i, ln := range lines {
				if w := lineWidth(ln); w > width {
					t.Errorf("%s@%d line %d overruns: display width %d > %d: %q",
						tc.name, width, i, w, width, ln)
				}
			}
			if !strings.HasPrefix(lines[1], tc.cont) {
				t.Errorf("%s@%d: continuation line %q does not start with cont %q",
					tc.name, width, lines[1], tc.cont)
			}
		}
	}
}

// TestWrapPrefixedContinuationFitsWidth is the plain-text twin of the test
// above (line-mode chat, error lines, tool output excerpts).
func TestWrapPrefixedContinuationFitsWidth(t *testing.T) {
	long := strings.Repeat("工具输出的长中文内容需要按终端宽度安全折行，", 10)
	cases := []struct {
		name   string
		prefix string
		cont   string
	}{
		{"tool-output", "    ⎿ ", "      "},
		{"error-line", "\x1b[31m× \x1b[0m  ", "    "},
		{"narrow-cont", "  ❯   ", "  "}, // cont narrower than prefix: must not underuse width either
	}
	for _, width := range []int{40, 60, 80} {
		for _, tc := range cases {
			lines := wrapPrefixed(tc.prefix, tc.cont, long, width)
			if len(lines) < 3 {
				t.Fatalf("%s@%d: expected multiple wrapped lines, got %d", tc.name, width, len(lines))
			}
			for i, ln := range lines {
				if w := lineWidth(ln); w > width {
					t.Errorf("%s@%d line %d overruns: display width %d > %d: %q",
						tc.name, width, i, w, width, ln)
				}
			}
		}
	}
}

// TestWrapTextVS16EmojiWidth pins the VS16 handling: "⚠️" (U+26A0 U+FE0F)
// renders as a 2-cell emoji on modern terminals, so wrapping must count it
// as 2 — counting it as 1 made emoji-carrying lines overrun by one cell each.
func TestWrapTextVS16EmojiWidth(t *testing.T) {
	if got := runeWidthStr("⚠️"); got != 2 {
		t.Fatalf("runeWidthStr(⚠️) = %d, want 2", got)
	}
	const width = 10
	lines := wrapText(strings.Repeat("⚠️", 8), width) // 8 × 2 cells = 16 cells → must wrap
	if len(lines) < 2 {
		t.Fatalf("expected wrapping of emoji run, got %d line(s)", len(lines))
	}
	for i, ln := range lines {
		if w := runeWidthStr(ln); w > width {
			t.Errorf("emoji line %d overruns: %d > %d: %q", i, w, width, ln)
		}
	}
}

// TestRenderMarkdownAssistantCJKFitsWidth is the end-to-end shape of the
// original report: a long assistant-style CJK paragraph wrapped through the
// markdown renderer with an empty prefix against a "  " continuation — the
// extreme unequal case that used to produce continuation lines 2 cells wider
// than the terminal. (The live assistant path now passes "  "/"  "; this
// keeps the extreme case pinned.)
func TestRenderMarkdownAssistantCJKFitsWidth(t *testing.T) {
	tui := &TUI{color: true, width: 60}
	para := strings.Repeat("人工智能正在改变保险行业的展业方式，代理人需要掌握数字化工具来服务客户。", 8)
	lines := tui.renderMarkdown(para, "", "  ", tui.width)
	if len(lines) < 3 {
		t.Fatalf("expected multiple wrapped lines, got %d", len(lines))
	}
	for i, ln := range lines {
		if w := lineWidth(ln); w > tui.width {
			t.Errorf("rendered line %d overruns: display width %d > %d: %q",
				i, w, tui.width, ln)
		}
	}
}
