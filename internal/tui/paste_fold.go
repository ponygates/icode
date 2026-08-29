package tui

import (
	"strconv"
	"strings"
)

// Pasted-block folding (Claude Code parity): when the user pastes a large
// chunk (a log dump, a code file, an error trace) the input box — which is
// conceptually a single line — would be flooded and the screen would scroll
// away. Instead we collapse the paste into a compact "[粘贴 N 行 #k]"
// placeholder and keep the real content in pasteBlocks. At submit time
// expandPasteBlocks restores the original text, so the model still receives
// everything while the editable prompt stays readable.
//
// Small pastes (≤ pasteFoldMinLines lines AND ≤ pasteFoldMaxBytes) are inserted
// verbatim — folding a 2-line snippet would be pure overhead.

const (
	pasteFoldMinLines = 3
	pasteFoldMaxBytes = 500
)

// insertPasted routes clipboard / bracketed-paste content through the folding
// logic. Small pastes land verbatim; large ones become a placeholder.
func (t *TUI) insertPasted(content string) {
	content = strings.TrimRight(content, "\r\n")
	if content == "" {
		return
	}
	lines := strings.Count(content, "\n") + 1
	if lines < pasteFoldMinLines && len([]byte(content)) <= pasteFoldMaxBytes {
		t.insertAtCursor(content)
		t.updateSuggestions()
		return
	}
	t.mu.Lock()
	if t.pasteBlocks == nil {
		t.pasteBlocks = make(map[string]string)
	}
	t.pasteSeq++
	seq := t.pasteSeq
	placeholder := pastePlaceholder(seq, lines)
	t.pasteBlocks[placeholder] = content
	t.mu.Unlock()
	t.insertAtCursor(placeholder)
	t.updateSuggestions()
}

// pastePlaceholder builds a unique, human-readable token for a folded paste.
func pastePlaceholder(seq, lines int) string {
	return "[粘贴 " + strconv.Itoa(lines) + " 行 #" + strconv.Itoa(seq) + "]"
}

// expandPasteBlocks replaces every folded-paste placeholder in text with its
// original content. It is idempotent: already-expanded text is returned
// unchanged, and a placeholder whose block was somehow lost (e.g. after a
// restart) is left intact so no pasted data is silently dropped.
func (t *TUI) expandPasteBlocks(text string) string {
	t.mu.Lock()
	blocks := t.pasteBlocks
	t.mu.Unlock()
	if len(blocks) == 0 {
		return text
	}
	for ph, content := range blocks {
		if strings.Contains(text, ph) {
			text = strings.ReplaceAll(text, ph, content)
		}
	}
	return text
}
