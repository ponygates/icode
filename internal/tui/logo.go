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
		return []string{paint("yellow", "●") + paint("white", "i") + paint("dim", "Code") + "  " + paint("dim", "多模型 AI 编程助手")}
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

// textWordRows renders the enlarged wordmark "iCode" in opencode's half-block
// lettering style: each letter is a 4-cell box where a cell is either a block
// glyph (█ ▀ ▄, which iCode's width model counts as 2 columns) or two spaces.
// Using this uniform "cell = 2 columns" convention keeps every letter exactly
// 8 columns wide, so the five letters line up into one clean wordmark.
//
// Colouring follows the design request: the "i" dot is yellow (▀), the "i"
// stem is light white (█), and the letters "Code" are a lighter black (dim).
func textWordRows(paint logoPainter) []string {
	dot := paint("yellow", "▀")
	tint := func(s string, fg string) string { return paint(fg, s) }

	const cell = "  " // one empty 2-column cell

	iRows := []string{
		"█" + cell + cell + cell,
		"█" + cell + cell + cell,
		"█" + cell + cell + cell,
	}
	cRows := []string{
		"█▀▀▀",
		"█" + cell + cell + cell,
		"▀▀▀▀",
	}
	oRows := []string{
		"█▀▀█",
		"█" + cell + cell + "█",
		"▀▀▀▀",
	}
	dRows := []string{
		"█▀▀█",
		"█" + cell + cell + "█",
		"▀▀▀█",
	}
	eRows := []string{
		"█▀▀█",
		"█▀▀▀",
		"▀▀▀▀",
	}

	// Colour one letter's glyphs (any non-space rune) with the given colour,
	// leaving spaces untouched so ANSI codes never break alignment.
	colorize := func(rows []string, fg string) []string {
		out := make([]string, 3)
		for r, row := range rows {
			var b strings.Builder
			for _, ch := range row {
				if ch == ' ' {
					b.WriteRune(ch)
				} else {
					b.WriteString(tint(string(ch), fg))
				}
			}
			out[r] = b.String()
		}
		return out
	}

	i := colorize(iRows, "white")
	c := colorize(cRows, "dim")
	o := colorize(oRows, "dim")
	d := colorize(dRows, "dim")
	e := colorize(eRows, "dim")

	blank := cell + cell + cell + cell // empty 8-column letter box
	rows := make([]string, 4)

	// Row 0: the yellow dot over the "i"; the other four letter boxes are blank.
	rows[0] = dot + cell + cell + cell + " " + blank + " " + blank + " " + blank + " " + blank

	// Rows 1–3: the letters, one space apart.
	for r := 0; r < 3; r++ {
		rows[r+1] = i[r] + " " + c[r] + " " + o[r] + " " + d[r] + " " + e[r]
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
