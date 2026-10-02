package tui

import (
	"strings"
)

// maxVisibleWidth returns the largest visible width among the supplied lines.
func maxVisibleWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if vw := visibleWidth(l); vw > w {
			w = vw
		}
	}
	return w
}

// padSliceBottom pads a slice with empty strings at the bottom so it reaches n
// items. Side-by-side boxes keep their first content line on the same row,
// which makes the two panels look horizontally aligned.
func padSliceBottom(items []string, n int) []string {
	if len(items) >= n {
		return items
	}
	out := make([]string, n)
	copy(out, items)
	for i := len(items); i < n; i++ {
		out[i] = ""
	}
	return out
}

// joinSideBySide zips two equal-height box blocks row by row, separated by gap
// spaces. Both boxes must already have the same number of rows (the caller
// equalises content via welcomeBoxes).
func joinSideBySide(a, b []string, gap int) []string {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	gapStr := strings.Repeat(" ", gap)
	out := make([]string, n)
	for i := 0; i < n; i++ {
		x, y := "", ""
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		out[i] = x + gapStr + y
	}
	return out
}

// boxWidth returns the visible width of a box's top (or bottom) border line —
// i.e. the full rendered width of that box.
func boxWidth(topLine string) int {
	return visibleWidth(topLine)
}

// padVisible pads s with spaces to reach the given display width, accounting
// for embedded ANSI escape sequences that take zero visible columns.
func padVisible(s string, w int) string {
	vw := visibleWidth(s)
	if vw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vw)
}

// fit returns s padded (or truncated with an ellipsis) to exactly w *visible*
// columns, so split panes line up under the vertical frame line. ANSI escape
// sequences are measured as zero width so colored lines still align.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if visibleWidth(s) > w {
		return truncVisible(s, w)
	}
	return s + strings.Repeat(" ", w-visibleWidth(s))
}

// fitVis pins s to EXACTLY w visible columns: it hard-truncates (no overflow,
// even for CJK runes that would otherwise push truncVisible one cell past w)
// and pads with spaces. ANSI escape sequences are measured as zero width and
// preserved, so colored box content still aligns. It is used to build framed
// boxes whose borders must line up on every row regardless of content width.
func fitVis(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	cur := 0
	inEsc := false
	for _, ch := range s {
		if inEsc {
			b.WriteRune(ch)
			if ch == 'm' {
				inEsc = false
			}
			continue
		}
		if ch == '\x1b' {
			inEsc = true
			b.WriteRune(ch)
			continue
		}
		cw := runeWidth(ch)
		if cur+cw > w {
			break
		}
		b.WriteRune(ch)
		cur += cw
	}
	vw := visibleWidth(b.String())
	if vw < w {
		b.WriteString(strings.Repeat(" ", w-vw))
	}
	return b.String()
}

// visibleWidth returns the display width of s, ignoring ANSI escape sequences.
func visibleWidth(s string) int {
	w := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		w += runeWidth(r)
	}
	return w
}

// truncVisible truncates s to w visible columns, preserving ANSI sequences and
// appending an ellipsis if anything was cut. A reset code is appended when a
// colored run is cut, so color never bleeds past the frame line.
func truncVisible(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	cur := 0
	inEsc := false
	cut := false
	for _, ch := range s {
		if inEsc {
			b.WriteRune(ch)
			if ch == 'm' {
				inEsc = false
			}
			continue
		}
		if ch == '\x1b' {
			inEsc = true
			b.WriteRune(ch)
			continue
		}
		if cur >= w-1 {
			cut = true
			break
		}
		b.WriteRune(ch)
		cur += runeWidth(ch)
	}
	if cut {
		if inEsc {
			b.WriteString("\x1b[0m") // close any open color before the ellipsis
		}
		b.WriteString("…")
	}
	return b.String()
}

// visSlice extracts the substring of s starting at visible column startCol,
// spanning at most maxCols display columns (CJK-aware). A double-width rune
// straddling startCol is dropped rather than split.
func visSlice(s string, startCol, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	var b strings.Builder
	col := 0
	started := false
	for _, r := range s {
		w := runeWidth(r)
		if !started {
			if col+w <= startCol {
				col += w
				continue
			}
			// First rune at/after the cut. A straddling CJK rune (col <
			// startCol < col+w) is skipped so it never renders half-cut.
			started = true
			if col < startCol {
				continue
			}
			if maxCols < w {
				break
			}
			b.WriteRune(r)
			maxCols -= w
			continue
		}
		if maxCols < w {
			break
		}
		b.WriteRune(r)
		maxCols -= w
	}
	return b.String()
}

// visTail returns the trailing maxCols display columns of s (CJK-aware).
func visTail(s string, maxCols int) string {
	total := visibleWidth(s)
	start := total - maxCols
	if start < 0 {
		start = 0
	}
	return visSlice(s, start, maxCols)
}
