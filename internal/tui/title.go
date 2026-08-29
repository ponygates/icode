package tui

import "strings"

// autoTitle names a fresh session from its first user message (Claude Code
// parity: the /resume picker and /sessions list show a meaningful title
// instead of a blank default). It only fires on the very first user turn of a
// brand-new session (exactly one user message in the transcript) — resumed
// sessions carry history so they are naturally skipped, and a later manual
// /rename is never overwritten because the count only grows. Runs silently.
func (t *TUI) autoTitle(text string) {
	t.mu.Lock()
	users := 0
	for _, m := range t.messages {
		if m.Role == RoleUser {
			users++
		}
	}
	t.mu.Unlock()
	if users != 1 {
		return
	}
	title := deriveTitle(text)
	if title == "" {
		return
	}
	if t.callback != nil {
		t.callback.OnRenameSession(title)
	}
	t.mu.Lock()
	t.sessionTitle = title
	t.mu.Unlock()
	t.scheduleRender()
}

// deriveTitle extracts a short, single-line title from a message: drops
// @file references and attachment markers, collapses whitespace, trims
// leading punctuation, and caps at 24 runes (… suffix).
func deriveTitle(text string) string {
	// Walk runes; "@path" references are skipped (space preserved).
	var b strings.Builder
	inRef := false
	for _, r := range text {
		if r == '@' {
			inRef = true
			continue
		}
		if inRef {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				inRef = false
				b.WriteRune(' ')
			}
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimSpace(b.String())
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > 24 {
		s = string(runes[:24]) + "…"
	}
	s = strings.TrimLeft(s, " \t\"'“”‘’《》（）【】[]#*-:：·")
	if s == "" {
		return ""
	}
	return s
}
