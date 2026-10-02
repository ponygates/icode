package tui

import "fmt"

// Dynamic terminal/tab title (OSC 0). Claude Code flips the tab title between
// "working" (✳ spinner glyph), "needs your input" (⏸), and idle so a
// backgrounded tab is identifiable at a glance without switching to it —
// Windows Terminal, conhost (VT mode), and every xterm-family terminal render
// it; terminals that ignore OSC are unaffected.
//
// The title is composed as "<prefix> icode · <workdir>"; an empty prefix is
// the idle state.

// setTermTitle writes the OSC 0 title with the given state prefix ("⏳"
// generating, "⚠" waiting for approval, "" idle). Safe to call from any
// goroutine (single Fprint, same write pattern as the completion bell).
// The OSC is ST-terminated (\x1b\\), NOT BEL-terminated: a BEL would make
// every title update ring the terminal's bell — audible noise and a false
// positive for anything sniffing \x07 (tests included). Windows Terminal and
// xterm-family terminals accept ST; terminals that ignore OSC are unaffected.
func (t *TUI) setTermTitle(prefix string) {
	title := "icode"
	if t.titleDir != "" {
		title += " · " + t.titleDir
	}
	if prefix != "" {
		title = prefix + " " + title
	}
	fmt.Fprintf(t.writer, "\x1b]0;%s\x1b\\", title)
}
