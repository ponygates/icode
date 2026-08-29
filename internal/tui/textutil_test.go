package tui

import "testing"

func TestRuneWidthStrEmojiSymbols(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"abc", 3},
		{"中文", 4},
		{"✅", 2},      // default-emoji symbol
		{"❌", 2},      // default-emoji symbol
		{"❗", 2},      // default-emoji symbol
		{"⚠️", 2},      // symbol + VS16 → emoji presentation
		{"❤️", 2},      // U+2764 + U+FE0F
		{"☀", 1},       // ambiguous symbol, modern terminal renders 1
		{"😀", 2},      // emoji pictograph
		{"A✅B", 4},    // 1 + 2 + 1
		{"标题：A", 7}, // 2+2 + fullwidth colon 2 + A 1
	}
	for _, c := range cases {
		if got := runeWidthStr(c.s); got != c.want {
			t.Errorf("runeWidthStr(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestRuneWidthStrTableAlign(t *testing.T) {
	// Two cells that must pad to the same visual column when tabular:
	// "✅完成" (2+2) vs "未完成" (2+2) vs "A" (1).
	pad := func(cell string, colW int) int { return colW - runeWidthStr(cell) }
	colW := 8
	for _, cell := range []string{"✅完成", "未完成", "英文AB", "⚠️警告"} {
		if pad(cell, colW) < 0 {
			t.Errorf("cell %q wider than column %d", cell, colW)
		}
	}
}
