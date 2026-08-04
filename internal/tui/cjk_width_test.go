package tui

import "testing"

// TestRuneWidthCJKAndEmoji verifies full-width handling for Han, emoji,
// combining marks, variation selectors, and supplementary-plane CJK so chat
// text wraps correctly instead of overrunning the terminal columns.
func TestRuneWidthCJKAndEmoji(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		want int
	}{
		{"ascii", 'A', 1},
		{"han", '中', 2},
		{"han-b", '本', 2},
		{"kana", 'あ', 2},
		{"cjk-ext-b", '\U00020BB7', 2}, // 𠮷 (supplementary plane)
		{"emoji-face", '\U0001F600', 2},
		{"emoji-rocket", '\U0001F680', 2},
		{"regional-indicator", '\U0001F1E8', 2},
		{"star", '★', 2},
		{"heart", '❤', 2},
		{"variation-selector", '\uFE0F', 0},
		{"combining-acute", '\u0301', 0},
		{"combining-ring", '\U00001AB5', 0},
	}
	for _, c := range cases {
		if got := runeWidth(c.r); got != c.want {
			t.Errorf("runeWidth(%q U+%04X) = %d, want %d", c.name, c.r, got, c.want)
		}
	}
}

// TestWrapTextCJKNoOverflow ensures a long CJK+emoji paragraph never produces a
// line wider than the terminal, which is the visible symptom of mis-counted
// full-width characters (text spilling past the right border).
func TestWrapTextCJKNoOverflow(t *testing.T) {
	text := "这是一段包含中文、English 和 emoji 🚀🔥 的长文本，用于验证换行时全角字符宽度计算是否正确，避免出现超出终端列宽的折行。"
	const width = 40
	lines := wrapText(text, width)
	for _, ln := range lines {
		if w := runeWidthStr(ln); w > width {
			t.Errorf("wrapped line exceeds width (%d > %d): %q", w, width, ln)
		}
	}
}

// TestCursorColCJK verifies the drawInputBox cursor-column formula. A full-width
// prompt (❯ counted as 2 by runeWidth after the emoji/dingbat extension) must not
// cause the cursor to land on top of an already-rendered CJK character — that is
// the "汉字输入时重叠显示" symptom.
func TestCursorColCJK(t *testing.T) {
	prompt := "❯ " // prompt + space; visibleWidth may be 2 or 3 depending on ❯ width
	prefixW := visibleWidth(prompt)

	cases := []struct {
		input   string
		cursor  int // rune offset
		wantCol int
	}{
		// col = prefixW + visibleWidth(text-before-cursor) + 1 (1-based ANSI)
		{"", 0, prefixW + 1},
		{"a", 1, prefixW + 2},
		{"中", 1, prefixW + 3},    // 中 width 2 → vw=2
		{"中文", 1, prefixW + 3},   // cursor after 中 only
		{"中文", 2, prefixW + 5},   // cursor after both (vw=4)
		{"你好世界", 3, prefixW + 7}, // 你好世: 3×2=6, col=prefixW+6+1=prefixW+7
		{"你好世界", 4, prefixW + 9}, // all 4: vw=8, col=prefixW+9
	}
	for _, c := range cases {
		runes := []rune(c.input)
		cur := c.cursor
		if cur > len(runes) {
			cur = len(runes)
		}
		vw := visibleWidth(string(runes[:cur]))
		col := prefixW + vw + 1
		if col != c.wantCol {
			t.Errorf("input=%q cursor=%d: col=%d want=%d (prefixW=%d vw=%d)",
				c.input, c.cursor, col, c.wantCol, prefixW, vw)
		}
	}
}
