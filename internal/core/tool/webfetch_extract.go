package tool

import "strings"

// htmlToText converts a raw HTML document into readable plain text for the
// model (Claude Code WebFetch parity): drops <script>/<style> blocks, strips
// tags, decodes common entities, collapses whitespace, and prepends the
// <title>. Raw markup would burn tokens and hurt comprehension — this keeps
// the fetch tool useful for web pages while the 256KB cap stays for APIs.
func htmlToText(raw string, maxChars int) string {
	// 1. Drop <script>…</script> blocks (inline handlers too).
	raw = dropBlock(raw, "<script", "</script>")
	raw = dropBlock(raw, "<style", "</style>")
	raw = dropBlock(raw, "<!--", "-->") // comments

	// 2. Extract <title> before tags are stripped.
	title := ""
	if ts := strings.Index(strings.ToLower(raw), "<title>"); ts >= 0 {
		te := strings.Index(raw[ts:], "</title>")
		if te > 0 {
			title = strings.TrimSpace(stripTags(raw[ts+7 : ts+te]))
		}
	}

	// 3. Strip tags, decode entities, collapse runs of whitespace.
	text := stripTags(raw)
	text = decodeHTMLEntities(text)
	var b strings.Builder
	prevSpace := true
	for _, r := range text {
		if r == '\n' || r == '\t' || r == '\r' {
			r = ' '
		}
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		b.WriteRune(r)
	}
	text = strings.TrimSpace(b.String())

	if title != "" && !strings.HasPrefix(text, title) {
		text = title + "\n\n" + text
	}
	runes := []rune(text)
	if len(runes) > maxChars {
		text = string(runes[:maxChars]) + "…"
	}
	return text
}

// dropBlock removes every occurrence of a block delimited by open/close
// markers (case-insensitive on the open marker, e.g. "<script").
func dropBlock(s, open, close string) string {
	for {
		i := strings.Index(strings.ToLower(s), strings.ToLower(open))
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], close)
		if j < 0 {
			return s
		}
		s = s[:i] + s[i+j+len(close):]
	}
}

// decodeHTMLEntities decodes the most common HTML entities.
func decodeHTMLEntities(s string) string {
	repl := map[string]string{
		"&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": "\"",
		"&#39;": "'", "&nbsp;": " ", "&mdash;": "—", "&ndash;": "–",
		"&hellip;": "…", "&copy;": "©", "&reg;": "®", "&euro;": "€",
	}
	for k, v := range repl {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// isHTML reports whether a fetched body looks like an HTML page (via
// Content-Type or the first bytes) rather than a JSON/plain API response.
func isHTML(body, contentType string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml") {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(body))
	if len(lower) > 0 && (strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html")) {
		return true
	}
	return false
}
