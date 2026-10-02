package tui

import (
	"strings"
	"testing"
)

// This file pins the CJK line-start prohibition (行首禁则): a wrapped line
// must never begin with sentence-final punctuation (，。、；：？！…— and
// closing brackets/quotes). The naive per-rune break stranded such marks at
// the start of the next line ("标点符号出现在行首"); the wrap loops now hand
// the previous visible character down so the next line starts with「字+标点」,
// leaving 1-2 cells of right-edge slack on the previous line instead.

// prohibitedAtLineStart reports whether any produced line begins with a
// rune that CJK typesetting forbids at line start.
func prohibitedAtLineStart(lines []string) (int, rune, bool) {
	for i, ln := range lines {
		rs := []rune(ln)
		if len(rs) > 0 && isLineStartProhibited(rs[0]) {
			return i, rs[0], true
		}
	}
	return 0, 0, false
}

// TestWrapTextLineStartProhibition: at width 20 the ten CJK chars fill the
// line exactly, so the naive break lands right before the full-width comma —
// which must be pulled down together with the preceding 十.
func TestWrapTextLineStartProhibition(t *testing.T) {
	const width = 20
	lines := wrapText(strings.Repeat("一二三四五六七八九十，", 6), width)
	if len(lines) < 3 {
		t.Fatalf("expected multiple wrapped lines, got %d: %q", len(lines), lines)
	}
	for i, ln := range lines {
		if w := runeWidthStr(ln); w > width {
			t.Errorf("line %d overruns: %d > %d: %q", i, w, width, ln)
		}
	}
	if i, r, bad := prohibitedAtLineStart(lines); bad {
		t.Errorf("line %d starts with prohibited %q: %q", i, r, lines[i])
	}
	if !strings.HasPrefix(lines[1], "十，") {
		t.Errorf("line 2 should start with 十，got %q", lines[1])
	}
}

// TestWrapANSILineStartProhibition is the styled twin: the same paragraph
// wrapped through the ANSI-preserving path must keep every style sequence
// intact AND keep sentence-final marks off line starts.
func TestWrapANSILineStartProhibition(t *testing.T) {
	tui := &TUI{color: true, width: 20}
	styled := tui.paint("green", strings.Repeat("一二三四五六七八九十，", 6))
	lines := tui.wrapANSI("", "  ", styled, 20)
	if len(lines) < 3 {
		t.Fatalf("expected multiple wrapped lines, got %d: %q", len(lines), lines)
	}
	for i, ln := range lines {
		if w := lineWidth(ln); w > 20 {
			t.Errorf("line %d overruns: %d > 20: %q", i, w, ln)
		}
		if i, r, bad := prohibitedAtLineStart([]string{stripANSI(ln)}); bad {
			t.Errorf("line %d starts with prohibited %q: %q", i, r, ln)
		}
	}
	if !strings.HasPrefix(stripANSI(lines[1]), "  十，") {
		t.Errorf("line 2 should start with cont + 十，got %q", lines[1])
	}
}

// TestRenderMarkdownAssistantAlignment pins the hanging-indent fix: raw-mode
// assistant messages used to render with prefix "" and cont "  ", so every
// wrapped CJK paragraph showed line 0 flush left and all continuation lines
// shifted right by 2 cells (visible as「第二行开始往右空一格」). The render
// path must now use equal prefix/cont, matching streaming (stream.go) and
// the plain-text path (wrapPrefixed "  "/"  ").
func TestRenderMarkdownAssistantAlignment(t *testing.T) {
	tui := &TUI{color: true, width: 60}
	para := strings.Repeat("人工智能正在改变保险行业的展业方式，代理人需要掌握数字化工具来服务客户。", 4)
	lines := tui.renderMarkdown(para, "  ", "  ", tui.width)
	if len(lines) < 2 {
		t.Fatalf("expected wrapped lines, got %d: %q", len(lines), lines)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "  ") {
			t.Errorf("line %d lacks the uniform 2-space indent: %q", i, ln)
		}
		if w := lineWidth(ln); w > tui.width {
			t.Errorf("line %d overruns: %d > %d: %q", i, w, tui.width, ln)
		}
	}
}

// TestMessageLinesWAssistantAlignment runs the real messageLinesW entry
// point (raw mode) to prove the fix end-to-end: the assistant paragraph's
// first line and its continuation lines share the same 2-space left edge.
func TestMessageLinesWAssistantAlignment(t *testing.T) {
	tui := &TUI{color: true, width: 60, rawMode: true}
	long := strings.Repeat("保险规划需要根据家庭生命周期动态调整，", 10)
	lines := tui.messageLinesW(Message{Role: RoleAssistant, Content: long}, tui.width)
	if len(lines) < 3 {
		t.Fatalf("expected wrapped lines, got %d: %q", len(lines), lines)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "  ") {
			t.Errorf("assistant line %d lacks the 2-space indent: %q", i, ln)
		}
		if w := lineWidth(ln); w > tui.width {
			t.Errorf("assistant line %d overruns: %d > %d: %q", i, w, tui.width, ln)
		}
	}
}
