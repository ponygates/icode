package tui

import (
	"strings"
)

// renderMarkdown converts a Markdown string into a slice of pre-prefixed,
// ANSI-styled terminal lines. It supports headings, bold/italic, inline code,
// fenced code blocks, blockquotes, ordered/unordered lists, links, and
// horizontal rules — enough for typical LLM responses. When color is disabled
// (t.color == false) all styling is dropped and the text is returned plain.
//
// prefix is the first-line indent (e.g. "  ◆ "); cont is the continuation
// indent for wrapped lines (e.g. "    "); width is the terminal width.
func (t *TUI) renderMarkdown(content, prefix, cont string, width int) []string {
	lines := strings.Split(content, "\n")
	var out []string

	inCode := false
	var codeBuf []string

	flushCode := func() {
		if len(codeBuf) == 0 {
			return
		}
		inner := width - runeWidthStr(prefix) - 2
		if inner < 8 {
			inner = 8
		}
		out = append(out, prefix+t.paint("dim", "┌"+repeat("─", inner)+"┐"))
		for _, cl := range codeBuf {
			for _, wl := range wrapText(cl, inner-1) {
				out = append(out, cont+t.paint("dim", "│ ")+t.c("cyan")+wl+"\x1b[0m")
			}
		}
		out = append(out, prefix+t.paint("dim", "└"+repeat("─", inner)+"┘"))
		codeBuf = nil
	}

	for _, ln := range lines {
		trim := strings.TrimSpace(ln)

		// Fenced code block toggle.
		if strings.HasPrefix(trim, "```") {
			if !inCode {
				inCode = true
				continue
			}
			inCode = false
			flushCode()
			continue
		}
		if inCode {
			codeBuf = append(codeBuf, ln)
			continue
		}

		// Headings: # .. ######
		if h := headingLevel(trim); h > 0 {
			text := strings.TrimSpace(trim[h:])
			styled := t.c("cyan") + "\x1b[1m" + text + "\x1b[0m"
			out = append(out, t.wrapANSI(prefix, cont, styled, width)...)
			out = append(out, prefix+t.paint("dim", repeat("─", width-runeWidthStr(prefix))))
			continue
		}

		// Horizontal rule.
		if trim == "---" || trim == "***" || trim == "___" {
			out = append(out, prefix+t.paint("dim", repeat("─", width-runeWidthStr(prefix))))
			continue
		}

		// Blockquote.
		if strings.HasPrefix(trim, ">") {
			text := strings.TrimSpace(strings.TrimPrefix(trim, ">"))
			styled := t.c("dim") + "▌ " + t.renderInline(text) + "\x1b[0m"
			out = append(out, t.wrapANSI(prefix, cont, styled, width)...)
			continue
		}

		// List items.
		if marker, rest, ok := listItemParts(trim); ok {
			styled := t.c("yellow") + marker + "\x1b[0m " + t.renderInline(rest)
			out = append(out, t.wrapANSI(prefix, cont, styled, width)...)
			continue
		}

		// Blank line.
		if trim == "" {
			out = append(out, "")
			continue
		}

		// Normal paragraph line.
		out = append(out, t.wrapANSI(prefix, cont, t.renderInline(ln), width)...)
	}

	if inCode {
		flushCode()
	}
	return out
}

// headingLevel returns the number of leading '#' characters (1–6) for a
// Markdown heading line, or 0 if the line is not a heading.
func headingLevel(trim string) int {
	if len(trim) == 0 || trim[0] != '#' {
		return 0
	}
	n := 0
	for n < len(trim) && trim[n] == '#' && n < 6 {
		n++
	}
	if n < len(trim) && trim[n] == ' ' {
		return n
	}
	return 0
}

// listItemParts parses an unordered (-, *, +) or ordered (1., 1)) list item,
// returning the marker, the remaining text, and whether it matched.
func listItemParts(trim string) (marker, rest string, ok bool) {
	if len(trim) == 0 {
		return "", "", false
	}
	if len(trim) >= 2 {
		switch trim[0] {
		case '-', '*', '+':
			if trim[1] == ' ' {
				return string(trim[0]), strings.TrimSpace(trim[2:]), true
			}
		}
	}
	if trim[0] >= '0' && trim[0] <= '9' {
		j := 1
		for j < len(trim) && trim[j] >= '0' && trim[j] <= '9' {
			j++
		}
		if j < len(trim) && (trim[j] == '.' || trim[j] == ')') && j+1 < len(trim) && trim[j+1] == ' ' {
			return trim[:j+1], strings.TrimSpace(trim[j+1:]), true
		}
	}
	return "", "", false
}

// renderInline applies inline Markdown styling (bold, italic, inline code,
// links) to a single line of text and returns the ANSI-decorated string.
// Returns the plain text unchanged when color is disabled.
func (t *TUI) renderInline(text string) string {
	if !t.color {
		return text
	}

	runes := []rune(text)
	n := len(runes)
	var b strings.Builder
	i := 0

	for i < n {
		r := runes[i]

		// Inline code: `code`
		if r == '`' {
			end := -1
			for j := i + 1; j < n; j++ {
				if runes[j] == '`' {
					end = j
					break
				}
			}
			if end > i+1 {
				code := string(runes[i+1 : end])
				b.WriteString(t.c("cyan"))
				b.WriteString("`" + code + "`")
				b.WriteString("\x1b[0m")
				i = end + 1
				continue
			}
		}

		// Bold: **text**
		if r == '*' && i+1 < n && runes[i+1] == '*' {
			end := -1
			for j := i + 2; j < n-1; j++ {
				if runes[j] == '*' && runes[j+1] == '*' {
					end = j
					break
				}
			}
			if end > i+1 {
				inner := string(runes[i+2 : end])
				b.WriteString("\x1b[1m")
				b.WriteString(inner)
				b.WriteString("\x1b[0m")
				i = end + 2
				continue
			}
		}

		// Italic: *text*
		if r == '*' {
			end := -1
			for j := i + 1; j < n; j++ {
				if runes[j] == '*' {
					end = j
					break
				}
			}
			if end > i+1 {
				inner := string(runes[i+1 : end])
				b.WriteString("\x1b[3m")
				b.WriteString(inner)
				b.WriteString("\x1b[0m")
				i = end + 1
				continue
			}
		}

		// Italic: _text_
		if r == '_' {
			end := -1
			for j := i + 1; j < n; j++ {
				if runes[j] == '_' {
					end = j
					break
				}
			}
			if end > i+1 {
				inner := string(runes[i+1 : end])
				b.WriteString("\x1b[3m")
				b.WriteString(inner)
				b.WriteString("\x1b[0m")
				i = end + 1
				continue
			}
		}

		// Link: [text](url)
		if r == '[' {
			closeB := -1
			for j := i + 1; j < n; j++ {
				if runes[j] == ']' {
					closeB = j
					break
				}
			}
			if closeB > i && closeB+1 < n && runes[closeB+1] == '(' {
				closeP := -1
				for j := closeB + 2; j < n; j++ {
					if runes[j] == ')' {
						closeP = j
						break
					}
				}
				if closeP > closeB+1 {
					linkText := string(runes[i+1 : closeB])
					url := string(runes[closeB+2 : closeP])
					b.WriteString("\x1b[4m")
					b.WriteString(linkText)
					b.WriteString("\x1b[0m")
					b.WriteString(t.paint("dim", " ("+url+")"))
					i = closeP + 1
					continue
				}
			}
		}

		b.WriteRune(r)
		i++
	}
	return b.String()
}

// wrapANSI wraps a string that may contain ANSI escape codes to the given
// total width, preserving inline styles across wrapped continuation lines.
// prefix is the first-line indent; cont is the continuation indent.
func (t *TUI) wrapANSI(prefix, cont, s string, width int) []string {
	if width < 6 {
		width = 6
	}
	prefixW := runeWidthStr(prefix)
	contentW := width - prefixW
	if contentW < 6 {
		contentW = 6
	}

	var out []string
	var cur strings.Builder
	curW := 0
	active := ""          // ANSI codes currently open (since the last reset)
	startStyle := ""      // style that was open when the current fragment began

	flush := func(first bool) {
		p := prefix
		if !first {
			p = cont
		}
		var line strings.Builder
		line.WriteString(p)
		if startStyle != "" {
			line.WriteString(startStyle)
		}
		line.WriteString(cur.String())
		if active != "" {
			line.WriteString("\x1b[0m")
		}
		out = append(out, line.String())
		cur.Reset()
		curW = 0
		startStyle = active // next fragment reopens whatever is still open
	}

	runes := []rune(s)
	i := 0
	for i < len(runes) {
		r := runes[i]
		if r == '\x1b' {
			// Capture the escape sequence (up to and including 'm').
			j := i + 1
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			if j >= len(runes) {
				i++
				continue
			}
			seq := string(runes[i : j+1])
			cur.WriteString(seq)
			if seq == "\x1b[0m" {
				active = ""
			} else {
				active += seq
			}
			i = j + 1
			continue
		}
		w := runeWidth(r)
		if curW+w > contentW && cur.Len() > 0 {
			flush(false)
			continue
		}
		cur.WriteRune(r)
		curW += w
		i++
	}
	flush(true)
	return out
}
