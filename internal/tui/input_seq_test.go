package tui

import (
	"strings"
	"testing"
)

// These tests replay the escape sequences that real terminals send for
// modified keys (Ctrl+Delete, Ctrl+arrows, F1–F4, X10 mouse) and assert two
// invariants the old parser broke:
//
//  1. No leakage — a modifier variant like "3;5~" must be consumed whole;
//     its tail must never be typed into the input box as garbage.
//  2. No swallowing — any sequence (known or unknown) that ends without a
//     '~'/'m'/'M' terminator must give up after escFollowTimeout instead of
//     eating the keystrokes the user types next.

// TestCtrlDeleteDoesNotLeakTail guards the "pressing Delete prints junk"
// bug: Ctrl+Delete arrives as ESC [ 3 ; 5 ~ and the old case '3' consumed
// exactly one follow-up byte (";"), leaving "5~" to be inserted as text.
func TestCtrlDeleteDoesNotLeakTail(t *testing.T) {
	tu := newTestTUI()
	tu.inputBuf = "abc"
	tu.cursor = 1
	feedEsc(t, tu, "\x1b[3;5~")
	if tu.inputBuf != "ac" {
		t.Fatalf("Ctrl+Delete: inputBuf=%q want %q (tail leaked?)", tu.inputBuf, "ac")
	}
	if tu.cursor != 1 {
		t.Fatalf("Ctrl+Delete: cursor=%d want 1", tu.cursor)
	}
}

// TestDeletePlainStillWorks: the unmodified Delete key (ESC [ 3 ~) must
// keep its original behavior after the case '3' rework.
func TestDeletePlainStillWorks(t *testing.T) {
	tu := newTestTUI()
	tu.inputBuf = "abc"
	tu.cursor = 1
	feedEsc(t, tu, "\x1b[3~")
	if tu.inputBuf != "ac" {
		t.Fatalf("Delete: inputBuf=%q want %q", tu.inputBuf, "ac")
	}
}

// TestSS3FunctionKeysDoNotEatKeys guards the "typing goes dead after F1"
// bug: ESC O P has no case, so it fell into the CSI default drain and
// waited for a '~'/'m'/'M' that never arrives — swallowing everything the
// user typed afterwards.
func TestSS3FunctionKeysDoNotEatKeys(t *testing.T) {
	for _, seq := range []string{"\x1bOP", "\x1bOQ", "\x1bOR", "\x1bOS"} {
		tu := newTestTUI()
		feedEsc(t, tu, seq)
		if !tu.handleKey('a') {
			t.Fatalf("%q: handleKey(a) signalled exit", seq)
		}
		if tu.inputBuf != "a" {
			t.Fatalf("%q: following key was swallowed; inputBuf=%q want %q", seq, tu.inputBuf, "a")
		}
	}
}

// TestCtrlArrowsDoNotEatKeys: Ctrl+Up arrives as ESC [ 1 ; 5 A. The digit
// '1' must not trigger Home, and the terminating 'A' must end the sequence
// so the next keystroke lands in the input box.
func TestCtrlArrowsDoNotEatKeys(t *testing.T) {
	tu := newTestTUI()
	tu.inputBuf = "xy"
	tu.cursor = 2
	feedEsc(t, tu, "\x1b[1;5A")
	if tu.cursor != 2 {
		t.Fatalf("Ctrl+Up: cursor=%d want 2 (Home must not fire on modifier form)", tu.cursor)
	}
	if !tu.handleKey('c') || tu.inputBuf != "xyc" {
		t.Fatalf("Ctrl+Up: following key lost; inputBuf=%q want %q", tu.inputBuf, "xyc")
	}
}

// TestUnknownCSITimeoutDoesNotEatKeys: a sequence ending in neither ~ m M
// (here a device-attributes-style reply "ESC [ ? 1 c") must stop draining
// after the timeout instead of eating later keystrokes. feedEsc only
// returns once handleKey has finished, so the drain has already timed out
// by the time the next key is fed.
func TestUnknownCSITimeoutDoesNotEatKeys(t *testing.T) {
	tu := newTestTUI()
	feedEsc(t, tu, "\x1b[?1c")
	if !tu.handleKey('b') {
		t.Fatal("handleKey(b) signalled exit")
	}
	if tu.inputBuf != "b" {
		t.Fatalf("unknown CSI ate following key; inputBuf=%q want %q", tu.inputBuf, "b")
	}
}

// TestHomeEndTildeSequences: the legacy "1~"/"4~" forms of Home/End still
// work after case '1'/'4' gained the consumeSeqFinal rework.
func TestHomeEndTildeSequences(t *testing.T) {
	tu := newTestTUI()
	tu.inputBuf = "abc"
	tu.cursor = 3
	feedEsc(t, tu, "\x1b[1~")
	if tu.cursor != 0 {
		t.Fatalf("Home(1~): cursor=%d want 0", tu.cursor)
	}
	feedEsc(t, tu, "\x1b[4~")
	if tu.cursor != 3 {
		t.Fatalf("End(4~): cursor=%d want 3", tu.cursor)
	}
}

// TestX10ShortReportDoesNotEatKeys: an X10 mouse report "ESC [ M" whose
// coordinate bytes never arrive (they are dropped by the UTF-8 decoder on
// terminals wider than 95 columns) must time out and not block.
func TestX10ShortReportDoesNotEatKeys(t *testing.T) {
	tu := newTestTUI()
	feedEsc(t, tu, "\x1b[M")
	if !tu.handleKey('d') {
		t.Fatal("handleKey(d) signalled exit")
	}
	if tu.inputBuf != "d" {
		t.Fatalf("X10 short report ate following key; inputBuf=%q want %q", tu.inputBuf, "d")
	}
}

// TestX10WheelReport: a complete wheel-up X10 report (ESC [ M ` ! ") must
// reach the small-scroll branch without panicking on a zero-height
// transcript.
func TestX10WheelReport(t *testing.T) {
	tu := newTestTUI()
	feedEsc(t, tu, "\x1b[M`!\"")
	if tu.inputBuf != "" {
		t.Fatalf("X10 wheel leaked into inputBuf: %q", tu.inputBuf)
	}
}

// TestPgUpModifierSwallowed: Ctrl+PgUp ("5;5~") is consumed whole — no
// residue in the input box, no panic on an empty transcript.
func TestPgUpModifierSwallowed(t *testing.T) {
	tu := newTestTUI()
	tu.inputBuf = "keep"
	feedEsc(t, tu, "\x1b[5;5~")
	if tu.inputBuf != "keep" || strings.Contains(tu.inputBuf, ";") {
		t.Fatalf("Ctrl+PgUp residue: inputBuf=%q want %q", tu.inputBuf, "keep")
	}
}

// TestBracketedPasteStillWorks: the 200~ rework must not regress
// bracketed paste — the payload after ESC [ 2 0 0 ~ lands in the input.
// Bytes are queued in wire order (sequence head, payload, terminator)
// before dispatching ESC, because feedEsc queues its tail only when called.
func TestBracketedPasteStillWorks(t *testing.T) {
	tu := newTestTUI()
	for _, r := range "\x1b[200~hi\x1b[201~"[1:] { // ESC itself comes from handleKey
		tu.keyCh <- r
	}
	if !tu.handleKey('\x1b') {
		t.Fatal("handleKey(ESC paste) signalled exit")
	}
	if tu.inputBuf != "hi" {
		t.Fatalf("bracketed paste: inputBuf=%q want %q", tu.inputBuf, "hi")
	}
}
