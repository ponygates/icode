package tui

import (
	"fmt"
	"os"
	"strings"
)

func formatTokens(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func shortDir(path string) string {
	parts := strings.Split(path, string(os.PathSeparator))
	n := len(parts)
	if n >= 3 {
		return parts[n-3] + "/" + parts[n-2] + "/" + parts[n-1]
	}
	if n >= 2 {
		return parts[n-2] + "/" + parts[n-1]
	}
	return path
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// wrapPrefixed wraps text to the terminal width, with a first-line prefix and
// a continuation indent. prefixWidth is the display width of `prefix`.
func wrapPrefixed(prefix, cont, text string, width int) []string {
	contentW := width - runeWidthStr(prefix)
	if contentW < 10 {
		contentW = 10
	}
	wrapped := wrapText(text, contentW)
	if len(wrapped) == 0 {
		return []string{prefix}
	}
	out := make([]string, len(wrapped))
	out[0] = prefix + wrapped[0]
	for i := 1; i < len(wrapped); i++ {
		out[i] = cont + wrapped[i]
	}
	return out
}

// wrapText wraps text to the given display width (counting CJK as width 2).
func wrapText(text string, width int) []string {
	if width < 4 {
		width = 4
	}
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		if para == "" {
			lines = append(lines, "")
			continue
		}
		runes := []rune(para)
		var cur []rune
		curW := 0
		for _, r := range runes {
			w := runeWidth(r)
			if curW+w > width && len(cur) > 0 {
				lines = append(lines, string(cur))
				cur = cur[:0]
				curW = 0
			}
			cur = append(cur, r)
			curW += w
		}
		lines = append(lines, string(cur))
	}
	return lines
}

func runeWidthStr(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	if r == 0 {
		return 0
	}
	// Zero-width: combining marks and variation selectors must not consume a
	// cell, otherwise emoji like "❤️" (U+2764 + U+FE0F) or accented text would
	// be over-counted and wrap one cell too early.
	switch {
	case r >= 0x0300 && r <= 0x036F, // Combining Diacritical Marks
		r >= 0x1AB0 && r <= 0x1AFF,   // Combining Diacritical Marks Extended
		r >= 0x1DC0 && r <= 0x1DFF,   // Combining Diacritical Marks Supplement
		r >= 0x20D0 && r <= 0x20FF,   // Combining Diacritical Marks for Symbols
		r >= 0xFE00 && r <= 0xFE0F,   // Variation Selectors
		r >= 0xE0100 && r <= 0xE01EF: // Variation Selectors Supplement
		return 0
	}
	if r >= 0x1100 && (r <= 0x115F ||
		r == 0x2329 || r == 0x232A ||
		(r >= 0x2E80 && r <= 0x303E) ||
		(r >= 0x3041 && r <= 0x33FF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0xA000 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		// Block Elements (U+2580..U+259F) — ▀ ▁ ▂ ▃ ▄ ▅ ▆ ▇ █ ▉ ▊ ▋ ▌ ▍ ▎ ▏ ▐
		// ░ ▒ ▓ ▔ ▕ ▖ ▗ ▘ ▙ ▚ ▛ ▜ ▝ ▞ ▟. Every one of these is East-Asian
		// WIDE (display width 2) on a real terminal, including legacy conhost
		// raster-font and OEM-CP437 code page. Mis-measuring them as width 1
		// was the source of the mis-aligned box borders in the welcome panel:
		// the ICODE wordmark, context meter, and progress bar all silently
		// shrank by half a cell per glyph, pushing the centring math off and
		// making side borders visibly jut out.
		(r >= 0x2580 && r <= 0x259F) ||
		// Geometric Shapes (U+25A0..U+25FF) — ● ○ ◆ ◇ ◌ ▶ ▼ ◀ ▲ ■ □ and the
		// like. East-Asian Ambiguous in a CJK locale, but in practice every
		// terminal the TUI targets renders them as 2 cells (CJK Wide), so we
		// count them as 2 to keep progress indicators aligned with framed
		// boxes. These never appear in the welcome box content itself, so
		// the over-count is harmless; under-counting was the visible bug.
		(r >= 0x25A0 && r <= 0x25FF) ||
		// General Punctuation (U+2010..U+2027) — – (en dash) — (em dash) …
		// (ellipsis) ‗ „ " " ‛ '. These are East-Asian Ambiguous and rendered
		// as fullwidth (2 cells) on Windows Terminal / WezTerm / iTerm2 in a
		// zh-CN locale. The welcome panel uses ─ and — in the context/cache
		// rows; counting them as width 1 left those rows one cell short,
		// which is what made the right-hand │ "stick out" past the rest of
		// the border.
		(r >= 0x2010 && r <= 0x2027) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6)) {
		return 2
	}
	// Emoji and Supplementary CJK planes (East Asian Wide / Fullwidth).
	if r >= 0x1F000 && r <= 0x1FAFF {
		return 2 // emoji pictographs (😀🚀🔥…)
	}
	if r >= 0x1F1E6 && r <= 0x1F1FF {
		return 2 // regional indicator symbols (flags)
	}
	if r >= 0x2600 && r <= 0x27BF {
		return 2 // Miscellaneous Symbols + Dingbats (★☀☂✅…)
	}
	if r >= 0x2B00 && r <= 0x2BFF {
		return 2 // Miscellaneous Symbols and Arrows
	}
	if r >= 0x20000 {
		return 2 // CJK Extension B+ and the rest of the Supplementary planes
	}
	return 1
}

func thinkingLines(text string, width int) []string {
	inner := width - 4
	if inner < 10 {
		inner = 10
	}
	label := " 思考 "
	pad := inner - runeWidthStr(label)
	if pad < 0 {
		pad = 0
	}
	top := "┌" + label + repeat("─", pad) + "┐"
	var out []string
	out = append(out, "  "+top)
	for _, l := range wrapText(text, inner) {
		out = append(out, "  │ "+fitVis(l, inner-1)+"│")
	}
	out = append(out, "  └"+repeat("─", inner)+"┘")
	return out
}

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}

func padEnd(s string, n int) string {
	w := runeWidthStr(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// hrule returns a full-width horizontal separator line (dim), used to divide
// the banner, messages, and footer regions of the TUI.
func (t *TUI) hrule(width int) string {
	if width < 2 {
		width = 2
	}
	return t.paint("dim", repeat("─", width))
}

// appVersionStr returns the human-readable version shown in the banner.
func appVersionStr() string {
	return tuiVersion
}

// tuiVersion is the app version, injected from ldflags via Config.Version.
var tuiVersion = "dev"
