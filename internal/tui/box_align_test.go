package tui

import (
	"testing"
)

// boxCols maps each box-drawing rune in a line to its VISIBLE column (CJK-aware),
// so CJK content no longer fools alignment checks the way raw rune indices do.
func boxCols(line string) map[rune][]int {
	out := map[rune][]int{}
	col := 0
	for _, r := range line {
		if r == '│' || r == '┌' || r == '┐' || r == '└' || r == '┘' ||
			r == '╭' || r == '╮' || r == '╰' || r == '╯' {
			out[r] = append(out[r], col)
		}
		col += runeWidth(r)
	}
	return out
}

// assertBoxAligned checks that every content row's left/right borders line up
// with the top/bottom corners of a single framed box (one ┌┐/╭╮ or └┘/╰╯ pair).
func assertBoxAligned(t *testing.T, name string, lines []string) {
	t.Helper()
	if len(lines) < 3 {
		return
	}
	var topL, topR, botL, botR int
	first := boxCols(lines[0])
	if c := first['┌']; len(c) > 0 {
		topL = c[0]
	} else if c := first['╭']; len(c) > 0 {
		topL = c[0]
	}
	if c := first['┐']; len(c) > 0 {
		topR = c[0]
	} else if c := first['╮']; len(c) > 0 {
		topR = c[0]
	}
	last := boxCols(lines[len(lines)-1])
	if c := last['└']; len(c) > 0 {
		botL = c[0]
	} else if c := last['╰']; len(c) > 0 {
		botL = c[0]
	}
	if c := last['┘']; len(c) > 0 {
		botR = c[0]
	} else if c := last['╯']; len(c) > 0 {
		botR = c[0]
	}

	for i := 1; i < len(lines)-1; i++ {
		cols := boxCols(lines[i])
		l := cols['│']
		if len(l) == 0 {
			t.Fatalf("%s row %d has no left │: %q", name, i, lines[i])
		}
		if l[0] != topL || l[0] != botL {
			t.Fatalf("%s row %d left │ at col %d, expected %d (corners): %q", name, i, l[0], topL, lines[i])
		}
		r := l[len(l)-1]
		if r != topR || r != botR {
			t.Fatalf("%s row %d right │ at col %d, expected %d (corners topR=%d botR=%d): %q",
				name, i, r, topR, topR, botR, lines[i])
		}
	}
}

func TestAllBoxesAligned(t *testing.T) {
	tui := New(Config{Model: "deepseek-v4-flash", Provider: "deepseek", Lang: "zh-CN", Theme: "dark"})
	tui.color = false // strip ANSI so visibleWidth math matches raw glyphs

	// Welcome box across terminal widths (narrow triggers the trim path).
	for _, w := range []int{120, 100, 90, 85, 80} {
		box := tui.welcomeBox(w)
		if box == nil {
			continue
		}
		assertBoxAligned(t, "welcomeBox("+itoa(w)+")", box)
	}

	// Streaming thinking box.
	for _, w := range []int{120, 90, 80, 70} {
		assertBoxAligned(t, "thinkingBox("+itoa(w)+")", tui.thinkingBox(w))
	}

	// "思考" framed block.
	assertBoxAligned(t, "thinkingLines", thinkingLines("分析中，正在调用工具并检查磁盘空间使用情况", 120))

	// Permission box replica (uses visibleWidth for the title so CJK titles size correctly).
	perm := buildPermissionBox("需要执行危险命令", "rm -rf node_modules", 120)
	assertBoxAligned(t, "permissionBox", perm)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// buildPermissionBox mirrors the inline permission box in render().
func buildPermissionBox(title, prompt string, W int) []string {
	boxW := min(visibleWidth(title)+4, W-4)
	if boxW < 40 {
		boxW = 40
	}
	if boxW > W-4 {
		boxW = W - 4
	}
	p := truncVisible(prompt, boxW-2)
	opts := "[1] 允许   [2] 全部允许   [3] 拒绝"
	return []string{
		"  ╭" + repeat("─", boxW) + "╮",
		"  │ " + title + padVisible("", boxW-visibleWidth(title)-2) + " │",
		"  │ " + p + padVisible("", boxW-visibleWidth(p)-2) + " │",
		"  │ " + opts + padVisible("", boxW-visibleWidth(opts)-2) + " │",
		"  ╰" + repeat("─", boxW) + "╯",
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestPrintMessageThinkingBoxHasRightBorder guards the line-mode thinking box
// (printMessage, RoleThinking): the middle row must carry a closing │ that
// lines up with the ┐/┘ corners — previously it was omitted, leaving the two
// vertical lines (left │ and the missing right │) unaligned.
func TestPrintMessageThinkingBoxHasRightBorder(t *testing.T) {
	lines := printMessageThinkingLines("正在分析代码结构并规划修改方案")
	assertBoxAligned(t, "printMessageThinking", lines)
}

// printMessageThinkingLines reproduces the RoleThinking branch of printMessage
// with the fix applied (right │ present, exact-width truncation via fitVis).
func printMessageThinkingLines(content string) []string {
	const inner = 12
	c := fitVis(content, inner)
	return []string{
		"  ┌─ thinking ─┐",
		"  │" + c + "│",
		"  └────────────┘",
	}
}
