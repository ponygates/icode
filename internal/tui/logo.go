package tui

import "strings"

// logoPainter applies a colour; asciiLogo takes it as a parameter so the same
// layout can be emitted coloured (TUI) or plain (Logo()).
type logoPainter func(color, s string) string

// asciiLogo returns the iCode startup LOGO: a large text wordmark "iCode" in
// opencode's half-block lettering style (█ ▀ ▄, rounded corners) — a yellow
// dot above the "i", a light-white "i" stem, and "Code" in a lighter black
// (dim grey) — plus a centred tagline. No box, no blossom, nothing to
// mis-align.
func (t *TUI) asciiLogo(width int, paint logoPainter) []string {
	wordRows := textWordRows(paint)
	wordW := 0
	for _, l := range wordRows {
		if w := visibleWidth(l); w > wordW {
			wordW = w
		}
	}

	if width < wordW {
		// Too narrow for the big wordmark — fall back to a single line.
		// Use "bold" instead of "white" to avoid the "LOGO 空白" bug on
		// light themes (where white maps to black/invisible).
		return []string{paint("yellow", "●") + paint("bold", "i") + paint("dim", "CODE") + "  " + paint("dim", "多模型 AI 编程助手")}
	}

	out := make([]string, 0, len(wordRows)+1)
	for _, l := range wordRows {
		out = append(out, padToCenter(padEndVisible(l, wordW), width))
	}
	// Tagline centred under the wordmark.
	out = append(out, padToCenter(paint("dim", "多模型 AI 编程助手"), width))
	return out
}

// logoLines renders the LOGO using the TUI's paint() so colours apply.
func (t *TUI) logoLines(width int) []string {
	return t.asciiLogo(width, t.paint)
}

// Logo returns the plain LOGO (no ANSI) plus a version line. Used for non-TTY
// output such as pipes and `icode version`.
func Logo() []string {
	t := &TUI{}
	lines := t.asciiLogo(80, func(_, s string) string { return s })
	lines = append(lines, "")
	lines = append(lines, "iCode "+appVersionStr()+"  ·  多模型 AI 编程助手")
	return lines
}

// textWordRows renders the enlarged wordmark "iCODE" in opencode's
// half-block lettering style (█ ▀ ▄, rounded corners). The wordmark is split
// into two halves — the left half "iC" is dim and the right half "ODE" is
// normal/bold — matching opencode's own logo layout.
//
// Each letter is 3 columns wide, drawn with half-block characters so
// "round" shapes (C, O, D, E) get curved corners instead of a hard square.
// The lowercase "i" has a yellow dot above its stem.
func textWordRows(paint logoPainter) []string {
	// Yellow dot above the lowercase "i" — only coloured element.
	dot := paint("yellow", "●")

	// Half-block letterforms in opencode style.
	// Layout (5 rows total):
	//
	//              ●   █▀▀▀
	//                  █
	//              █   █
	//              █   █▄▄▄
	//
	// Then right half (4 rows): "O" "D" "E" — appended on the same baseline.

	// Left half (dim): "i" (dot + 3 stem rows) + "C" (3 rows).
	// 4 rows: dot, stem+top, stem+left, stem+bottom. Dot is immediately
	// above the stem (no blank row).
	// "i"在第1列, "C"在第3列 (1空格间距, 与O/D/E间距一致), 宽度5字符。
	left := []string{
		dot + "   " + " ",       // row 0: ● + 3空格 + 1空格 = 5字符
		"█" + " " + "█▀▀▀",      // row 1: █ + 1空格 + █▀▀▀ = 5字符
		"█" + " " + "█   ",      // row 2: █ + 1空格 + █ + 3空格 = 5字符
		"█" + " " + "█▄▄▄",      // row 3: █ + 1空格 + █▄▄▄ = 5字符
	}

	// Right half (bold): "O" + "D" + "E" — 3 rows tall (rows 1–3 of left).
	right := []string{
		"█▀▀█" + " " + "█▀▀▄" + " " + "█▀▀▀", // top bars (O: ▀▀, D: ▀▀▄ right-open, E: ▀▀▀ solid)
		"█  █" + " " + "█  █" + " " + "█▀▀ ", // middle row (E closes top half)
		"█▄▄█" + " " + "█▄▄▀" + " " + "█▄▄▄", // bottom bars
	}

	rows := make([]string, len(left))
	dim := "dim"
	bold := "bold"
	for i, l := range left {
		var r string
		if i == 0 {
			// Dot row — only the left half has content.
			r = l
		} else {
			// Pair left[i] with right[i-1] (right is 1 row shorter).
			r = l + " " + right[i-1]
		}
		// Paint the left half dim, right half bold, mirroring opencode.
		if i == 0 {
			rows[i] = paint(dim, r)
		} else {
			parts := strings.SplitN(r, " ", 2)
			if len(parts) == 2 {
				rows[i] = paint(dim, parts[0]) + " " + paint(bold, parts[1])
			} else {
				rows[i] = paint(dim, r)
			}
		}
	}
	return rows
}

// padEndVisible pads s with trailing spaces to exactly w visible cells (ANSI
// escape sequences count as zero width), so the shorter dot row of the
// wordmark lines up with the wordmark row.
func padEndVisible(s string, w int) string {
	vw := visibleWidth(s)
	if vw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vw)
}

// padToCenter centres s (measured by visible width, ANSI ignored) within w
// cells, returning a string exactly w cells wide.
func padToCenter(s string, w int) string {
	vw := visibleWidth(s)
	if vw >= w {
		return s
	}
	left := (w - vw) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-vw-left)
}
