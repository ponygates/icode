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

// blossomRaw is the 棉花梅花 motif: a five-petal plum flower (top) carried on
// a curved branch with two leaves (bottom) — the classic 梅花 composition.
// Every row is plain ASCII (no East-Asian Ambiguous glyphs, no Unicode art
// that vanishes on legacy conhost) so it renders on any terminal, and it is
// re-coloured per render. Glyph map:
//   '(' ')' '*' = petal (arc outline + fill)   'o' = pistil   '.' = stamen
//   '|' '/' '\' = branch                       '~' = leaf
// Each row is exactly 13 cells wide so the art stays symmetric when centred.
// Five-petal layout: top petal (row 0), upper-left+upper-right (row 1),
// lower-left+lower-right (row 3), with stamens ringing the pistil (row 2).
var blossomRaw = []string{
	"     (*)     ",
	"   (*) (*)   ",
	"    . o .    ",
	"   (*) (*)   ",
	"      |      ",
	"    / | \\    ",
	"   ~  |  ~   ",
}

// paintBlossomRow colours one raw blossom row: petals magenta (arc + fill),
// pistil and stamens yellow, branch dim (grey — palette-safe on both
// light/dark terminals), leaves green. A plain (no-ANSI) renderer passes a
// paint func that returns the text unchanged, so `icode version` / pipes
// still show the ASCII art.
func paintBlossomRow(paint logoPainter, row string) string {
	var b strings.Builder
	for _, r := range row {
		switch r {
		case '(', ')', '*':
			b.WriteString(paint("magenta", string(r)))
		case 'o', '.':
			b.WriteString(paint("yellow", string(r)))
		case '|', '/', '\\':
			b.WriteString(paint("dim", string(r)))
		case '~':
			b.WriteString(paint("green", string(r)))
		default:
			b.WriteString(string(r))
		}
	}
	return b.String()
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
	art := make([]string, 0, len(blossomRaw)+len(wordRows)+1)
	for _, fl := range blossomRaw {
		art = append(art, padToCenter(paintBlossomRow(paint, fl), wordW))
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
