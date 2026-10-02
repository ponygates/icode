package tui

import (
	"fmt"
	"strings"
)

// handleSearchKey routes every key while the reverse-search overlay is open.
func (t *TUI) handleSearchKey(r rune) bool {
	switch r {
	case 0x03, 0x07, 0x1b: // Ctrl+C / Ctrl+G / Esc — cancel search
		t.cancelSearch()
		return true
	case 0x0c: // Ctrl+L — clear screen and cancel search
		t.cancelSearch()
		fmt.Fprint(t.writer, "\x1b[2J\x1b[H")
		return true
	case 0x12: // Ctrl+R again — cycle to the next match (bash isearch)
		t.cycleSearch()
		return true
	case '\r', '\n', 0x09: // Enter / Tab — accept the current match into input
		t.acceptSearch()
		return true
	case 0x7f, 0x08: // Backspace / DEL — delete last query char
		if len([]rune(t.searchBuf)) > 0 {
			t.searchBuf = string([]rune(t.searchBuf)[:len([]rune(t.searchBuf))-1])
			t.updateSearchMatches()
			t.searchIdx = 0
		} else {
			t.cancelSearch()
			return true
		}
		t.render()
		return true
	}
	if r < 0x20 {
		return true // ignore other control characters
	}
	// Printable rune — append to the search query and re-filter.
	t.searchBuf += string(r)
	t.updateSearchMatches()
	t.searchIdx = 0
	t.render()
	return true
}

// startSearch opens the reverse-history-search overlay.
func (t *TUI) startSearch() {
	if len(t.history) == 0 {
		return
	}
	t.mu.Lock()
	t.searchMode = true
	t.searchBuf = ""
	t.restoreInput = t.inputBuf
	t.inputBuf = ""
	t.cursor = 0
	t.acOpen = false
	t.acItems = nil
	t.searchIdx = 0
	t.mu.Unlock()
	t.updateSearchMatches()
	t.render()
}

// updateSearchMatches recomputes matches (most-recent-first) filtered by the
// current query. Safe to call from any goroutine that already holds t.mu is
// NOT assumed — it locks internally.
func (t *TUI) updateSearchMatches() {
	t.mu.Lock()
	defer t.mu.Unlock()
	q := strings.ToLower(t.searchBuf)
	// Transcript search (Claude Code Ctrl+R parity): match the conversation
	// log — user AND assistant messages — newest first, then fall back to the
	// typed-input history. Dedup keeps repeated prompts from stacking.
	matches := make([]string, 0, len(t.messages)+len(t.history))
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		matches = append(matches, s)
	}
	for i := len(t.messages) - 1; i >= 0; i-- {
		m := t.messages[i]
		switch m.Role {
		case RoleUser, RoleAssistant, RoleSystem:
			if q == "" || strings.Contains(strings.ToLower(m.Content), q) {
				add(m.Content)
			}
		}
	}
	for i := len(t.history) - 1; i >= 0; i-- {
		if q == "" || strings.Contains(strings.ToLower(t.history[i]), q) {
			add(t.history[i])
		}
	}
	t.searchMatches = matches
	if t.searchIdx >= len(matches) {
		t.searchIdx = len(matches) - 1
	}
	if t.searchIdx < 0 {
		t.searchIdx = 0
	}
}

// cycleSearch moves to the next match (Ctrl+R pressed again).
func (t *TUI) cycleSearch() {
	t.mu.Lock()
	if len(t.searchMatches) <= 1 {
		t.mu.Unlock()
		return
	}
	t.searchIdx = (t.searchIdx + 1) % len(t.searchMatches)
	t.mu.Unlock()
	t.render()
}

// acceptSearch loads the highlighted match into the input line and closes the
// overlay. The match is NOT submitted — the user can edit or press Enter.
func (t *TUI) acceptSearch() {
	t.mu.Lock()
	var chosen string
	if t.searchIdx >= 0 && t.searchIdx < len(t.searchMatches) {
		chosen = t.searchMatches[t.searchIdx]
	}
	t.searchMode = false
	t.searchBuf = ""
	t.searchMatches = nil
	t.searchIdx = 0
	t.inputBuf = chosen
	t.cursor = len([]rune(chosen))
	t.mu.Unlock()
	t.render()
}

// cancelSearch closes the overlay and restores the input buffer.
func (t *TUI) cancelSearch() {
	t.mu.Lock()
	t.searchMode = false
	t.searchBuf = ""
	t.searchMatches = nil
	t.searchIdx = 0
	t.inputBuf = t.restoreInput
	t.cursor = len([]rune(t.restoreInput))
	t.restoreInput = ""
	t.mu.Unlock()
	t.render()
}
