package tui

import (
	"regexp"
	"strings"
)

// ansiEscapeRe matches a complete terminal escape sequence:
//   - CSI: ESC [ params… intermediates… final — colours, cursor moves,
//     erase/display modes (\x1b[31m, \x1b[1;5A, \x1b[2J …)
//   - OSC: ESC ] payload (BEL | ESC \) — window title, clipboard, hyperlinks
//   - two-byte escapes: ESC + one byte in 0x40–0x5C (SS3 "ESC O …", DECSC …)
//
// Whole sequences must be removed, not just the ESC byte: a stripped-ESC
// remainder like "[31m" survives as visible garbage text in the input box.
var ansiEscapeRe = regexp.MustCompile(
	"\x1b\\[[0-9;:<=>?]*[ -/]*[@-~]" + // CSI
		"|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC … BEL / ST
		"|\x1b[@-Z\\\\]") // two-byte escape

// sanitizeInput strips everything the terminal would otherwise EXECUTE when
// the input line is repainted.
//
// Paste is the main entry channel: clipboard text copied from a terminal
// (colour-coded command output, editor artifacts) often carries ANSI escape
// sequences. The input box renders its buffer verbatim, so a pasted escape
// sequence is re-executed on EVERY repaint — the cursor jumps, rows get
// erased, and random garbage shows up on and above the input box until the
// next repaint covers it ("打字出现乱码/乱码自动消失"). Keyboard input is
// already guarded (handleKey only inserts runes >= 0x20), so this is the
// one gate the poison can still sneak through.
//
// Order matters: complete escape sequences are removed first (an OSC payload
// may contain control bytes that would otherwise cut the sequence short),
// then any remaining loose control bytes (NUL, CR, DEL, C1 range …) are
// dropped one by one. \n and \t are kept — they are handled structurally
// (paste folding / tab spacing), never executed.
//
// The common case contains nothing to strip, so a fast ContainsFunc probe
// keeps this a no-cost pass-through for normal typing and paste.
func sanitizeInput(s string) string {
	if !strings.ContainsFunc(s, isControlByte) {
		return s
	}
	s = ansiEscapeRe.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControlByte(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isControlByte reports whether a rune must never be painted into the input
// box. \n and \t are the only exceptions — they are handled structurally
// (line folding / tab spacing) instead of being executed by the terminal.
func isControlByte(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return r <= 0x1F || r == 0x7F || (r >= 0x80 && r <= 0x9F)
}
