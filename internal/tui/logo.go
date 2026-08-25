package tui

import "strings"

// logoPainter applies a colour; asciiLogo takes it as a parameter so the same
// layout can be emitted coloured (TUI) or plain (Logo()).
type logoPainter func(color, s string) string

// asciiLogo returns the iCode startup LOGO: a large text wordmark "iCODE" in
// opencode's block-lettering style (chunky rectangular letters, █ body with a
// ░ inner-shadow bevel at the bottom of each counter) — a yellow dot above the
// "I", a bright white letter body, and dim inner shadows — plus a centred
// tagline. No box, no blossom, nothing to mis-align.
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
		return []string{paint("yellow", "●") + paint("white", "I") + paint("dim", "CODE") + "  " + paint("dim", "多模型 AI 编程助手")}
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
	lines = append(lines, "iCODE "+appVersionStr()+"  ·  多模型 AI 编程助手")
	return lines
}

// textWordRows returns the 5-row "iCODE" wordmark, colourised per glyph:
// █ = bright white body, ░ = dim inner shadow, ● = yellow dot.
//
// The letterforms follow opencode's wordmark geometry (packages/ui logo.tsx):
// every letter is a thick rectangular block whose counter carries a shadow
// band across its lower half — the "bevel" that makes the mark read at a
// glance. "I" keeps its dot so the word stays unambiguous.
//
//	●   ████ ████ ███  ████
//	█   █    █  █ █  █ █
//	█   █░░░ █░░█ █░░█ ████
//	█   █░░░ █░░█ █░░█ █░░░
//	█   ████ ████ ████ ████
func textWordRows(paint logoPainter) []string {
	rows := []string{
		" ●   ████ ████ ███  ████",
		" █   █    █  █ █  █ █   ",
		" █   █░░░ █░░█ █░░█ ████",
		" █   █░░░ █░░█ █░░█ █░░░",
		" █   ████ ████ ████ ████",
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = colorizeWordRow(row, paint)
	}
	return out
}

// colorizeWordRow maps each glyph of one wordmark row to its colour: █ body →
// white, ░ shadow → dim, ● dot → yellow; everything else stays a space. Runs
// of the same glyph are painted as one span so the ANSI output stays small.
func colorizeWordRow(row string, paint logoPainter) string {
	runes := []rune(row)
	var b strings.Builder
	for i := 0; i < len(runes); {
		ch := runes[i]
		j := i
		for j < len(runes) && runes[j] == ch {
			j++
		}
		seg := string(runes[i:j])
		switch ch {
		case '█':
			b.WriteString(paint("white", seg))
		case '░':
			b.WriteString(paint("dim", seg))
		case '●':
			b.WriteString(paint("yellow", seg))
		default:
			b.WriteString(seg)
		}
		i = j
	}
	return b.String()
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
