package tui

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
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
	// Miscellaneous Symbols + Dingbats (U+2600..U+27BF) such as ★ ☀ ☂ ✅ are
	// East-Asian Ambiguous: they render fullwidth (2 cells) only when the
	// terminal widens ambiguous glyphs (legacy conhost raster / CJK locale).
	// Default Windows Terminal and most modern terminals render them at 1 cell
	// — counting them as 2 pushed the ❯ prompt cursor one cell too far right
	// and left the IME pre-edit text drawn one cell off ("光标错位/中文上浮").
	// The TUI targets modern terminals, so count them as 1.
	//
	// Note: ❯ (U+276F) used for the input prompt lives in this block; measuring
	// it as width 2 made the editable cursor land past the last typed rune on
	// every keystroke. Same fix as the block-elements / geometric-shapes /
	// general-punctuation ranges above: match the terminal, not the locale.
	if r >= 0x2B00 && r <= 0x2BFF {
		return 2 // Miscellaneous Symbols and Arrows (★-adjacent ⭐⬤ etc.)
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

// ansiSequenceLen returns the length (in bytes) of the ANSI escape sequence
// starting at s[0], which must be 0x1B. Handles CSI (ESC [ … final), OSC (ESC ]
// … BEL or ESC \), and single-character escapes (ESC X). Returns 0 when the
// sequence is incomplete (streaming chunks may split one escape across two
// chunks — the caller must buffer the fragment and re-feed it).
func ansiSequenceLen(s string) int {
	if len(s) == 0 || s[0] != 0x1b {
		return 0
	}
	if len(s) == 1 {
		return 0 // lone ESC — wait for the next byte
	}
	switch s[1] {
	case '[': // CSI: ESC [ params… final (0x40–0x7E)
		for i := 2; i < len(s); i++ {
			c := s[i]
			if c >= 0x40 && c <= 0x7E {
				return i + 1
			}
		}
		return 0 // incomplete
	case ']': // OSC: ESC ] … BEL (0x07) or ESC \
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b {
				if i+1 < len(s) && s[i+1] == '\\' {
					return i + 2
				}
				return 0 // nested/truncated
			}
		}
		return 0 // incomplete
	default: // single-character escape (ESC 7, ESC M, ESC (, …)
		return 2
	}
}

// sanitizeStreamText strips ANSI escape sequences and C0/C1 control characters
// from a chunk of streamed model text, keeping only printable content plus
// \n, \r and \t. Without this, a model reply that echoes terminal escapes
// (or a provider that leaks control bytes) would corrupt the TUI layout and
// render as caret notation garbage such as "^¿^¿" between CJK runs.
//
// Because a single escape (or a multi-byte UTF-8 rune) may be split across
// streaming chunks, any trailing incomplete sequence is returned as `pending`;
// the caller must prepend it to the next chunk before sanitising again.
func sanitizeStreamText(s string) (clean, pending string) {
	if s == "" {
		return "", ""
	}
	var b strings.Builder
	for len(s) > 0 {
		// ANSI escape sequence (always pure ASCII).
		if s[0] == 0x1b {
			n := ansiSequenceLen(s)
			if n == 0 {
				return b.String(), s // incomplete — hold for next chunk
			}
			s = s[n:]
			continue
		}
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			// Invalid byte — unless it is a multi-byte rune truncated at the
			// chunk boundary, in which case hold the fragment as pending.
			if n := incompleteUTF8Len(s); n > 0 {
				return b.String(), s[:n]
			}
			s = s[1:]
			continue
		}
		switch {
		case r < 0x20 && r != '\n' && r != '\r' && r != '\t':
			// drop other C0 controls
		case r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// drop DEL and C1 controls (U+0080–U+009F, incl. 2-byte UTF-8)
		default:
			b.WriteString(s[:size])
		}
		s = s[size:]
	}
	return b.String(), ""
}

// incompleteUTF8Len reports how many leading bytes of s form a multi-byte
// UTF-8 rune whose remaining continuation bytes are missing (i.e. it was cut
// off at a streaming chunk boundary). Returns 0 when s[0] is not such a
// truncated lead byte.
func incompleteUTF8Len(s string) int {
	if len(s) == 0 {
		return 0
	}
	first := s[0]
	var need int
	switch {
	case first < 0x80:
		return 0
	case first < 0xC2:
		return 0 // illegal lead byte (0x80–0xC1)
	case first < 0xE0:
		need = 2
	case first < 0xF0:
		need = 3
	case first < 0xF5:
		need = 4
	default:
		return 0 // illegal lead byte (0xF5+)
	}
	// Count how many continuation bytes are present.
	n := 1
	for n < len(s) && n < need {
		if s[n] >= 0x80 && s[n] <= 0xBF {
			n++
		} else {
			break
		}
	}
	if n < need && n == len(s) {
		return n // truncated at the end of the chunk
	}
	return 0
}

// sanitizeFullText sanitises a complete (non-streamed) block of model text in
// one pass — no pending buffer, so an incomplete trailing escape or truncated
// rune is simply dropped.
func sanitizeFullText(s string) string {
	clean, _ := sanitizeStreamText(s)
	return clean
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
