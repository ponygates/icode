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

// textWordRows renders the enlarged wordmark "iCode" out of solid block glyphs
// (█ ▀ ▄). Each letter is a 4-cell box (a cell = filled block "██" = 2 columns,
// or two spaces), so every letter is a uniform 8 columns. The wordmark is 7 rows
// tall: a dot row, a spacer row that visibly separates the "i" dot from its
// stem, and five rows of letterforms.
//
// Colouring matches the request: every letter is plain block (white), and the
// only coloured element is the yellow dot (██) above the "i".
func textWordRows(paint logoPainter) []string {
	dot := paint("yellow", "●")

	const cell = "  " // one empty 2-column cell (2 spaces)
	const fill = "█"  // one filled 2-column block (█ = East Asian Wide, renders 2 cols)
	const blank = cell + cell + cell + cell

	// iCODE: lowercase "i" (stem in cell 1, 4 rows + 1 blank top), uppercase "CODE" (full block).
	iRows := []string{
		cell + cell + cell + cell, // blank row — makes i visually shorter
		cell + fill + cell + cell, // stem in cell 1
		cell + fill + cell + cell,
		cell + fill + cell + cell,
		cell + fill + cell + cell,
	}
	cRows := []string{
		fill + fill + fill + fill, // top bar
		fill + cell + cell + cell, // left vertical
		fill + cell + cell + cell,
		fill + cell + cell + cell,
		fill + fill + fill + fill, // bottom bar
	}
	oRows := []string{
		fill + fill + fill + fill, // top bar
		fill + cell + cell + fill, // left + right verticals
		fill + cell + cell + fill,
		fill + cell + cell + fill,
		fill + fill + fill + fill, // bottom bar
	}
	dRows := []string{
		fill + fill + fill + cell, // top bar
		fill + cell + cell + fill, // left + right
		fill + cell + cell + fill,
		fill + cell + cell + fill,
		fill + fill + fill + cell, // bottom bar
	}
	eRows := []string{
		fill + fill + fill + fill, // top bar
		fill + fill + fill + cell, // middle bar (right open)
		fill + cell + cell + cell, // left vertical
		fill + fill + fill + cell, // bottom bar (right open)
		fill + fill + fill + fill, // base bar
	}

	// Letters stay UNPAINTED (terminal default foreground). Colouring them
	// "white" backfired: the light theme maps white → \x1b[30m (black), so on
	// any theme/background mismatch the whole wordmark rendered invisible —
	// the "LOGO 空白" bug. Plain blocks are visible everywhere; only the dot
	// keeps its yellow accent.
	i := iRows
	c := cRows
	o := oRows
	d := dRows
	e := eRows

	rows := make([]string, 7)

	// Row 0: the yellow dot above "i" stem (cell 1).
	rows[0] = cell + dot + cell + cell + " " + blank + " " + blank + " " + blank + " " + blank

	// Row 1: spacer row so the dot does not touch the stem.
	rows[1] = blank + " " + blank + " " + blank + " " + blank + " " + blank

	// Rows 2–6: the letterforms, one space apart.
	for r := 0; r < 5; r++ {
		rows[r+2] = i[r] + " " + c[r] + " " + o[r] + " " + d[r] + " " + e[r]
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
