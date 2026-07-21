package tui

import (
	"bytes"
	"strings"
	"testing"
)

// renderAt renders a fresh welcome screen at the given size and returns the
// plain-text output (ANSI stripped) for assertions.
func renderAt(w, h int) string {
	tui := New(Config{Model: "deepseek-v4-flash", Provider: "deepseek", Lang: "zh-CN", Theme: "dark"})
	var buf bytes.Buffer
	tui.writer = &buf
	tui.rawMode = true
	tui.color = false // no ANSI, so assertions match raw glyphs
	tui.width = w
	tui.height = h
	tui.model = "deepseek-v4-flash"
	tui.provider = "deepseek"
	tui.welcomeVisible = true
	tui.messages = nil
	tui.render()
	return buf.String()
}

// TestWelcomeAdaptive guards the height-adaptive welcome screen: the Claude
// Code-style two-column box must render whole or not at all — it must never be
// sliced in half on a short terminal. It also verifies the full banner shows
// on a roomy terminal and degrades gracefully when cramped.
func TestWelcomeAdaptive(t *testing.T) {
	welcomeTop := "iCode v"        // first row of the Claude Code welcome box
	welcomeTip := "Tips for getting started" // right-column header

	// Roomy terminal: the whole welcome box must be present.
	roomy := renderAt(120, 40)
	if !strings.Contains(roomy, welcomeTop) {
		t.Fatalf("expected welcome box on a roomy terminal:\n%s", roomy)
	}
	if !strings.Contains(roomy, welcomeTip) {
		t.Fatalf("expected tips section on a roomy terminal:\n%s", roomy)
	}
	if !strings.Contains(roomy, "Welcome back!") {
		t.Fatalf("expected 'Welcome back!' on a roomy terminal:\n%s", roomy)
	}

	// The banner must be anchored near the top (not vertically centred): on a
	// 40-row terminal the first welcome row must sit within the first ~12 lines,
	// so it can never be pushed above the visible window.
	for i, ln := range strings.Split(roomy, "\n") {
		if strings.Contains(ln, welcomeTop) {
			if i > 12 {
				t.Fatalf("welcome anchored too low (row %d) instead of near top:\n%s", i, roomy)
			}
			break
		}
	}

	// Cramped terminals of several heights: never a partial welcome. Whenever
	// the tip section appears, the top row must appear too.
	for _, sz := range []struct{ w, h int }{{120, 18}, {100, 16}, {90, 14}, {80, 12}, {80, 10}} {
		out := renderAt(sz.w, sz.h)
		if strings.Contains(out, welcomeTip) && !strings.Contains(out, welcomeTop) {
			t.Fatalf("welcome sliced in half at %dx%d (tip without top):\n%s", sz.w, sz.h, out)
		}
	}
}

// runeIndex / runeLastIndex find a rune's position by *rune* (cell) index, not
// byte offset, so multi-byte box-drawing glyphs (│ ╮ ╯) are measured correctly.
func runeIndex(s string, target rune) int {
	rs := []rune(s)
	for i, r := range rs {
		if r == target {
			return i
		}
	}
	return -1
}
func runeLastIndex(s string, target rune) int {
	rs := []rune(s)
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == target {
			return i
		}
	}
	return -1
}

// TestWelcomeBoxRightBorderAligned guards the Claude Code-style two-column
// welcome box: the right `│` border must sit at the same column on every row,
// and must match the `╮` / `╯` corners of the top/bottom bar. This regresses the
// bug where the right column was padded to leftW but NOT to rightW, so rows with
// shorter right content pulled the right border inward — leaving it misaligned.
func TestWelcomeBoxRightBorderAligned(t *testing.T) {
	tui := New(Config{Model: "deepseek-v4-flash", Provider: "deepseek", Lang: "zh-CN", Theme: "dark"})
	tui.color = false
	// welcomeBox returns the bare lines (paint() is a no-op with color=false),
	// so we can assert per-row column alignment directly without parsing the
	// full-screen frame's cursor-escape encoding.
	box := tui.welcomeBox(120)
	if len(box) < 3 {
		t.Fatalf("welcome box too small: %v", box)
	}
	top := box[0]
	bot := box[len(box)-1]
	topRight := runeIndex(top, '╮')
	botRight := runeIndex(bot, '╯')
	if topRight < 0 || botRight < 0 {
		t.Fatalf("missing corners: top=%q bot=%q", top, bot)
	}
	if topRight != botRight {
		t.Fatalf("top/bottom right corners misaligned: top col %d, bot col %d", topRight, botRight)
	}
	for i, ln := range box[1 : len(box)-1] {
		c := runeLastIndex(ln, '│')
		if c < 0 {
			t.Fatalf("row %d missing right border: %q", i, ln)
		}
		if c != topRight {
			t.Fatalf("row %d right border at col %d, expected %d (matches corners):\n  %q", i, c, topRight, ln)
		}
	}
}
