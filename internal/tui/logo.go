package tui

import (
	"strings"
)

// logoPainter applies a colour; asciiLogo takes it as a parameter so the same
// layout can be emitted coloured (TUI) or plain (Logo()).
type logoPainter func(color, s string) string

// asciiLogo returns the iCode startup LOGO: a plum-blossom (棉花梅花) ASCII
// motif above a block-letter "ICODE" wordmark, plus a centered tagline. It is
// rendered WITHOUT a surrounding box — the user asked to drop the old bordered
// banner — so there is nothing to mis-align. Every glyph is ASCII, and █ / ░
// are CP437 block elements that render reliably on legacy Windows conhost
// (raster font / OEM codepage) where decorative Unicode (✦ ◆ ✻ 🐴 …) has no
// glyph and would make the logo disappear.

// logoFont defines each block letter as 5 equal-width rows using █ (full
// block). Widths: I=3, C/O/D/E=6 — verified so the wordmark stays rectangular.
var logoFont = map[rune][]string{
	'I': {"███", " █ ", " █ ", " █ ", "███"},
	'C': {"██████", "█     ", "█     ", "█     ", "██████"},
	'O': {"██████", "█    █", "█    █", "█    █", "██████"},
	'D': {"█████ ", "█    █", "█    █", "█    █", "█████ "},
	'E': {"██████", "█     ", "█████ ", "█     ", "██████"},
}

const logoWord = "ICODE"

// plumBlossom is the 棉花梅花 motif; each row is exactly 11 cells wide so it
// can be centred over the wordmark.
var plumBlossom = []string{
	"   .-~-.   ",
	"  (     )  ",
	"  <  o  >  ",
	"  (     )  ",
	"   '-.-'   ",
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

// asciiLogo builds the full art block (flower above wordmark above tagline),
// all rows exactly wordW visible cells wide, then centres the block within the
// terminal width.
func (t *TUI) asciiLogo(width int, paint logoPainter) []string {
	if width < 40 {
		// Too narrow for the block wordmark — fall back to a single line.
		return []string{paint("cyan", "ICODE") + "  " + paint("dim", "多模型 AI 编程助手")}
	}

	// Build the block "ICODE" wordmark first, then measure its TRUE visible
	// width. Using len() would mis-count the █ block runes (3 UTF-8 bytes
	// each) and push the centring maths off by ~50 columns.
	wordRows := make([]string, 5)
	for r := 0; r < 5; r++ {
		var b strings.Builder
		for i, ch := range logoWord {
			letter := logoFont[ch]
			b.WriteString(paint("cyan", letter[r]))
			if i < len(logoWord)-1 {
				b.WriteString(" ")
			}
		}
		wordRows[r] = b.String()
	}
	wordW := visibleWidth(wordRows[0])

	// Compose the art block (flower, then wordmark, then tagline), each row
	// padded to exactly wordW cells so terminal-centring stays consistent.
	tag := paint("dim", "多模型 AI 编程助手")
	art := make([]string, 0, len(plumBlossom)+len(wordRows)+1)
	for _, fl := range plumBlossom {
		art = append(art, padToCenter(paint("magenta", fl), wordW))
	}
	art = append(art, wordRows...)
	art = append(art, padToCenter(tag, wordW))

	// Centre the whole block within the terminal width.
	left := (width - wordW) / 2
	if left < 0 {
		left = 0
	}
	pad := strings.Repeat(" ", left)
	out := make([]string, 0, len(art))
	for _, l := range art {
		out = append(out, pad+l)
	}
	return out
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
