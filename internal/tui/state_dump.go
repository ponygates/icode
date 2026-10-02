package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ponygates/icode/internal/core/privacy"
)

// ── F12 forensic state dump ──────────────────────────────────────────

// dumpState appends the full in-memory TUI state to ~/.icode/screen-dump.txt,
// right after dumpScreen()'s visual snapshot. The screen dump only shows the
// visible character cells of the alternate buffer; this section shows the RAW
// buffers (input box, queue, stream, every message) with escapes made visible
// via %q — control bytes and ANSI fragments that never paint a cell can still
// be traced back to the exact buffer that holds them. When the caller passes
// the rows dumpScreen() read, it also diffs the live screen against the
// renderer's last frame: any body row that differs was written by something
// OTHER than our renderer (conhost echo revival, an IME writing directly into
// the console buffer, or a zombie second instance) — the incremental repaint
// would otherwise leave that stray row on screen forever. Bound to F12.
func (t *TUI) dumpState(scr []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".icode", "screen-dump.txt")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	// screen-dump.txt holds every message buffer verbatim — tool output and
	// provider errors in it can carry live keys, so mask before it hits disk.
	w := privacy.NewRedactingWriter(f)

	// Copy everything under one lock, then format outside it.
	t.mu.Lock()
	inputBuf := t.inputBuf
	cursor := t.cursor
	queue := append([]string{}, t.queue...)
	streamBuf := t.streamBuf.String()
	statusNotice := t.statusNotice
	msgs := make([]Message, len(t.messages))
	copy(msgs, t.messages)
	streaming := t.streaming
	backgrounded := t.backgrounded
	permPending := t.permPending
	permNoteOpen := t.permNoteOpen
	acOpen := t.acOpen
	welcomeVisible := t.welcomeVisible
	helpVisible := t.helpVisible
	zenMode := t.zenMode
	resumePickerOpen := t.resumePickerOpen
	replayOpen := t.replayOpen
	modelPickerOpen := t.modelPickerOpen
	diffBoxOpen := t.diffBoxOpen
	planPending := t.planPending
	scrollOffset := t.scrollOffset
	width, height := t.width, t.height
	t.mu.Unlock()
	t.renderMu.Lock()
	frameRows := append([]string{}, t.lastFrame...)
	t.renderMu.Unlock()

	fmt.Fprintf(w, "tui-state %s\n", time.Now().Format("15:04:05.000"))
	fmt.Fprintf(w, "  flags: streaming=%v backgrounded=%v permPending=%v permNoteOpen=%v acOpen=%v welcomeVisible=%v helpVisible=%v zenMode=%v resumePickerOpen=%v replayOpen=%v modelPickerOpen=%v diffBoxOpen=%v planPending=%v\n",
		streaming, backgrounded, permPending, permNoteOpen,
		acOpen, welcomeVisible, helpVisible, zenMode,
		resumePickerOpen, replayOpen, modelPickerOpen,
		diffBoxOpen, planPending)
	fmt.Fprintf(w, "  size: %dx%d scrollOffset=%d cursor=%d\n", width, height, scrollOffset, cursor)
	fmt.Fprintf(w, "  inputBuf: %s\n", qTrim(inputBuf, 400))
	fmt.Fprintf(w, "  queue(%d):\n", len(queue))
	for i, s := range queue {
		fmt.Fprintf(w, "    [%d] %s\n", i, qTrim(s, 300))
	}
	fmt.Fprintf(w, "  streamBuf: %s\n", qTrim(streamBuf, 800))
	fmt.Fprintf(w, "  statusNotice: %s\n", qTrim(statusNotice, 200))
	fmt.Fprintf(w, "  messages(%d):\n", len(msgs))
	for i, m := range msgs {
		fmt.Fprintf(w, "    [%d] role=%s tool=%q folded=%v liveTailLen=%d content=%s\n",
			i, m.Role, m.Tool, m.Folded, len(m.LiveTail), qTrim(m.Content, 400))
	}

	// Screen-vs-frame diff: F12 fires between renders, so the live screen and
	// the renderer's last frame should agree row for row (modulo the input
	// box / status tail, which drawInputBox owns, and the scrollbar gutter).
	// A body row that disagrees was painted by something else — echo revival,
	// an IME writing straight into the console buffer, a zombie instance.
	if len(scr) > 0 && len(frameRows) > 0 {
		fmt.Fprintf(w, "  screen-vs-lastFrame diff (external-write detector):\n")
		// Skip the tail block: input box (~3 rows) + status bar (1).
		bodyLimit := len(scr) - 4
		if bodyLimit > len(frameRows) {
			bodyLimit = len(frameRows)
		}
		mismatch := 0
		for i := 0; i < bodyLimit; i++ {
			want := strings.TrimRight(sanitizeInput(frameRows[i]), " ")
			got := strings.TrimRight(scr[i], " ")
			// The scrollbar glyph overlays the final column of body rows.
			if rl := len([]rune(got)); rl > 0 {
				lastR := []rune(got)[rl-1]
				if lastR == '│' || lastR == '█' {
					got = strings.TrimRight(string([]rune(got)[:rl-1]), " ")
				}
			}
			if want != got {
				mismatch++
				fmt.Fprintf(w, "    row %d MISMATCH: frame=%q screen=%q\n",
					i+1, qTrim(want, 120), qTrim(got, 120))
			}
		}
		if mismatch == 0 {
			fmt.Fprintf(w, "    (body rows match the last frame — no external writes)\n")
		}
	}
	fmt.Fprint(w, "\n")
}

// qTrim renders s with %q (escapes visible) after trimming to the first n
// runes, so huge buffers don't blow up the dump file.
func qTrim(s string, n int) string {
	cnt := 0
	for i := range s {
		if cnt == n {
			return fmt.Sprintf("%q…", s[:i])
		}
		cnt++
	}
	return fmt.Sprintf("%q", s)
}

// ── key-consumer site trace ──────────────────────────────────────────

// lastKeySite remembers which top-level key consumer received the previous
// rune. Package-level (not per-TUI) on purpose: the question it answers —
// "which two code paths are splitting one typing burst?" — is about the
// process, not a specific instance.
var lastKeySite atomic.Value // holds string

// traceKeySite records a rune reaching a top-level consumer ("main", "drain",
// "shell", "perm", "paste"). In normal use the site never changes while the
// user types (always "main"), so nothing is logged; a live alternation —
// e.g. main→drain→main→drain on consecutive runes — logs one line per switch
// and names the racing pair directly in cli.log. Nested sequence parsing
// (nextKey/nextKeyTimeout inside handleKey) deliberately has no site: it runs
// on the consumer's own goroutine and cannot race with it.
func traceKeySite(site string, r rune) {
	prev, _ := lastKeySite.Load().(string)
	if prev == site {
		return
	}
	lastKeySite.Store(site)
	writeCliLog(fmt.Sprintf("[tui] key-site: %q -> %q (rune=%q)", prev, site, r))
}
