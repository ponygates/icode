package tui

import (
	"bufio"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/core/permission"
)

// startPumpForTest wires a TUI with the two-goroutine byte pipeline
// (rawBytePump → byteCh → keyPump → keyCh) against the given input and
// returns keyCh plus a stop function. Feeding happens through a pipe so the
// test can split a burst across writes exactly like ConPTY does.
func startPumpForTest(t *testing.T) (chan<- string, <-chan rune) {
	t.Helper()
	pr, pw := io.Pipe()
	tu := &TUI{
		reader:        bufio.NewReader(pr),
		keyCh:         make(chan rune, 64),
		byteCh:        make(chan byte, 512),
		permKeyCh:     make(chan rune, 8),
		keyStop:       make(chan struct{}),
		keyReaderDone: make(chan struct{}),
	}
	go tu.rawBytePump()
	go tu.keyPump()
	writeCh := make(chan string, 16)
	go func() {
		defer pw.Close()
		for s := range writeCh {
			_, _ = pw.Write([]byte(s))
		}
	}()
	t.Cleanup(func() {
		close(writeCh)
		close(tu.keyStop)
	})
	return writeCh, tu.keyCh
}

// readRunes collects n runes from ch (fails the test on 2s starvation).
func readRunes(t *testing.T, ch <-chan rune, n int) []rune {
	t.Helper()
	out := make([]rune, 0, n)
	for len(out) < n {
		select {
		case r, ok := <-ch:
			if !ok {
				t.Fatalf("keyCh closed after %d/%d runes", len(out), n)
			}
			out = append(out, r)
		case <-time.After(2 * time.Second):
			t.Fatalf("starved waiting for rune %d/%d", len(out)+1, n)
		}
	}
	return out
}

// TestPumpDeleteSequenceAtomic feeds "\x1b[3~" (the Delete key) as ONE burst
// and asserts the pump delivers the whole sequence to keyCh — the guarantee
// handleKey relies on to never mistake a real sequence for a lone Esc.
func TestPumpDeleteSequenceAtomic(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\x1b[3~"
	got := readRunes(t, keyCh, 4)
	want := []rune{0x1b, '[', '3', '~'}
	if string(got) != string(want) {
		t.Fatalf("Delete burst = %q, want %q", string(got), string(want))
	}
}

// TestPumpSplitDeleteSequence feeds Delete as TWO bursts — the ConPTY split
// that used to leak "[3~" into the input box. The second burst arrives well
// within the reassembly window, so the pump must still deliver one atomic
// sequence.
func TestPumpSplitDeleteSequence(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\x1b"
	writeCh <- "[3~"
	got := readRunes(t, keyCh, 4)
	want := []rune{0x1b, '[', '3', '~'}
	if string(got) != string(want) {
		t.Fatalf("split Delete = %q, want %q", got, want)
	}
}

// TestPumpSGRWheelAtomic feeds an SGR wheel report ("\x1b[<64;103;15M" —
// wheel-up at column 103, i.e. beyond the old 95-column X10 limit) and
// asserts it arrives as one burst.
func TestPumpSGRWheelAtomic(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\x1b[<64;103;15M"
	got := readRunes(t, keyCh, 13)
	want := "\x1b[<64;103;15M"
	if string(got) != want {
		t.Fatalf("SGR wheel = %q, want %q", string(got), want)
	}
}

// TestPumpX10WheelLargeCoords feeds an X10 wheel report with coordinate
// bytes >= 0x80 (col 160 → 0xC2, row 163 → 0xC5). The old incremental UTF-8
// decoder swallowed these (wheel died / input bytes got eaten); reassembly
// must pass all three raw bytes through untouched.
func TestPumpX10WheelLargeCoords(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\x1b[M\x60\xc2\xc5" // ESC [ M 0x60(btn=64) 0xC2 0xC5
	got := readRunes(t, keyCh, 6)
	want := []rune{0x1b, '[', 'M', 0x60, 0xC2, 0xC5}
	if string(got) != string(want) {
		t.Fatalf("X10 wheel = %q, want %q", string(got), string(want))
	}
}

// TestPumpLoneEsc delivers a real standalone Esc (nothing follows within the
// reassembly window) — must surface as a lone 0x1b rune.
func TestPumpLoneEsc(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\x1b"
	select {
	case r := <-keyCh:
		if r != 0x1b {
			t.Fatalf("lone Esc = %q, want ESC", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rune after lone Esc")
	}
	// Make sure nothing else leaks out afterwards.
	select {
	case r := <-keyCh:
		t.Fatalf("unexpected extra rune %q after lone Esc", r)
	case <-time.After(60 * time.Millisecond):
	}
}

// TestPumpAltKey feeds ESC+'k' (Alt+k) — must arrive as the 2-rune pair.
func TestPumpAltKey(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\x1bk"
	got := readRunes(t, keyCh, 2)
	if got[0] != 0x1b || got[1] != 'k' {
		t.Fatalf("Alt+k = %q, want ESC k", got)
	}
}

// TestPumpEscThenChinese presses Esc and immediately commits a Chinese
// character (IME workflow). The ESC must go out alone and the character must
// decode normally — NOT be misread as "Alt+0xE4" garbage.
func TestPumpEscThenChinese(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\x1b你"
	got := readRunes(t, keyCh, 2)
	if got[0] != 0x1b {
		t.Fatalf("first rune = %q, want lone ESC", got[0])
	}
	if got[1] != '你' {
		t.Fatalf("second rune = %q, want 你", got[1])
	}
}

// TestPumpChineseIMEBurst commits Chinese text — plain UTF-8 path regression.
func TestPumpChineseIMEBurst(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "你好世界"
	got := readRunes(t, keyCh, 4)
	if string(got) != "你好世界" {
		t.Fatalf("IME burst = %q, want 你好世界", string(got))
	}
}

// TestPumpConhostExtendedDelete feeds the legacy conhost 0xE0 0x53 pair and
// asserts it is translated to the VT Delete sequence atomically.
func TestPumpConhostExtendedDelete(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\xE0S"
	got := readRunes(t, keyCh, 4)
	want := []rune{0x1b, '[', '3', '~'}
	if string(got) != string(want) {
		t.Fatalf("conhost Delete = %q, want %q", got, want)
	}
}

// TestPumpBracketedPasteOpen feeds the bracketed-paste opener "\x1b[200~"
// and pasted text — the opener must reassemble atomically, the payload must
// flow through unchanged.
func TestPumpBracketedPasteOpen(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\x1b[200~hello"
	got := readRunes(t, keyCh, 11)
	want := "\x1b[200~hello"
	if string(got) != want {
		t.Fatalf("paste = %q, want %q", string(got), want)
	}
}

// TestPumpSS3Arrow feeds an SS3 arrow ("\x1bOA", application cursor mode) —
// must arrive as one 3-rune burst.
func TestPumpSS3Arrow(t *testing.T) {
	writeCh, keyCh := startPumpForTest(t)
	writeCh <- "\x1bOA"
	got := readRunes(t, keyCh, 3)
	want := []rune{0x1b, 'O', 'A'}
	if string(got) != string(want) {
		t.Fatalf("SS3 arrow = %q, want %q", got, string(want))
	}
}

// ── drainStream: wheel/escape sequences must never leak into the queue
// buffer or fire an interrupt while streaming ────────────────────────────
// (Previously only "ESC [ A" was recognized; every other sequence's
// printable tail — e.g. a wheel report's "64;103;15" — was inserted into
// the input box as if typed: "scrolling during streaming prints garbage".)

// drainStreamRunner starts drainStream and returns a probe that waits until
// inputBuf reaches the wanted value (or fails on timeout), plus a finisher
// that ends the stream and waits for drainStream to return.
func drainStreamRunner(t *testing.T, tu *TUI) (waitFor func(want string), finish func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tu.drainStream()
	}()
	return func(want string) {
			t.Helper()
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				tu.mu.Lock()
				got := tu.inputBuf
				tu.mu.Unlock()
				if got == want {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			tu.mu.Lock()
			got := tu.inputBuf
			tu.mu.Unlock()
			t.Fatalf("inputBuf = %q, want %q (sequence payload leaked as typed text)", got, want)
		}, func() {
			t.Helper()
			tu.streamDone <- struct{}{}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("drainStream did not return after streamDone")
			}
		}
}

// TestDrainStreamWheelNoGarbage feeds an SGR wheel report followed by real
// typing while streaming: the sequence must be swallowed whole, the typed
// characters must land in the queue draft, and nothing may interrupt.
func TestDrainStreamWheelNoGarbage(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.streaming = true
	tu.writer = io.Discard
	var interrupted atomic.Bool
	tu.callback = &testCallback{onInterrupt: func() { interrupted.Store(true) }}
	// Simulate the key pump's atomic delivery: wheel burst, then typing.
	for _, r := range "\x1b[<64;1;1M" { // coords contain digits 1/6/4
		tu.keyCh <- r
	}
	tu.keyCh <- 'o'
	tu.keyCh <- 'k'
	waitFor, finish := drainStreamRunner(t, tu)
	waitFor("ok")
	if interrupted.Load() {
		t.Error("wheel sequence must not fire OnInterrupt")
	}
	finish()
}

// TestDrainStreamX10WheelNoGarbage feeds an X10 wheel report (raw coordinate
// bytes 0x60 0xC2 0xC5) — all three raw bytes must be consumed, leaving the
// draft empty.
func TestDrainStreamX10WheelNoGarbage(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.streaming = true
	tu.writer = io.Discard
	var interrupted atomic.Bool
	tu.callback = &testCallback{onInterrupt: func() { interrupted.Store(true) }}
	for _, r := range "\x1b[M\x60\xc2\xc5" {
		tu.keyCh <- r
	}
	waitFor, finish := drainStreamRunner(t, tu)
	waitFor("")
	if interrupted.Load() {
		t.Error("X10 wheel sequence must not fire OnInterrupt")
	}
	finish()
}

// TestDrainStreamSS3NoLeak feeds an SS3 arrow ("\x1bOA") — the final letter
// 'A' used to leak into the queue buffer as typed text.
func TestDrainStreamSS3NoLeak(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.streaming = true
	tu.writer = io.Discard
	for _, r := range "\x1bOA" {
		tu.keyCh <- r
	}
	waitFor, finish := drainStreamRunner(t, tu)
	waitFor("")
	finish()
}

// TestDrainStreamWheelThenLoneEsc: after a swallowed wheel burst, a genuine
// lone Esc must STILL interrupt (the sequence swallow must not eat it).
func TestDrainStreamWheelThenLoneEsc(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.streaming = true
	tu.writer = io.Discard
	var interrupted atomic.Bool
	tu.callback = &testCallback{onInterrupt: func() { interrupted.Store(true) }}
	for _, r := range "\x1b[<64;1;1M" {
		tu.keyCh <- r
	}
	waitFor, _ := drainStreamRunner(t, tu)
	waitFor("")
	tu.keyCh <- 0x1b // nothing follows → genuine lone Esc
	deadline := time.Now().Add(2 * time.Second)
	for !interrupted.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !interrupted.Load() {
		t.Fatal("lone Esc after a wheel sequence must call OnInterrupt")
	}
	// End the stream to let drainStream return and the test goroutine exit.
	tu.streamDone <- struct{}{}
}

// ── PromptPermission: wheel sequences must never answer the dialog ───────
// (An SGR report's coordinate digits collide with the decision keys: a
// units digit '1' would Allow, '2' AllowAll, '3' Deny — scrolling could
// silently grant permission!)

// TestPromptPermissionWheelNoDecision scrolls the wheel (sequence contains
// digit '1' and '6' and '4') and then presses the REAL deny key. The wheel
// must be swallowed; only the explicit key decides.
func TestPromptPermissionWheelNoDecision(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.writer = io.Discard
	go func() {
		for _, r := range "\x1b[<64;1;1M" {
			tu.permKeyCh <- r
		}
		// Let PromptPermission finish swallowing the sequence before the
		// real key arrives (otherwise it could be eaten as part of it).
		time.Sleep(150 * time.Millisecond)
		tu.permKeyCh <- 'n'
	}()
	dec := tu.PromptPermission("run rm -rf?", permission.SeverityHigh)
	if dec != permission.DecisionDeny {
		t.Fatalf("wheel + 'n' must Deny, got %v (wheel digits answered the prompt?)", dec)
	}
}

// TestPromptPermissionWheelThenAllow: same, but the explicit key is '1'
// (Allow) — it must still work after wheel bursts, i.e. swallowing sequences
// must not break the real decision path.
func TestPromptPermissionWheelThenAllow(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.writer = io.Discard
	go func() {
		for _, r := range "\x1b[<64;103;15M" {
			tu.permKeyCh <- r
		}
		time.Sleep(150 * time.Millisecond)
		tu.permKeyCh <- '1'
	}()
	dec := tu.PromptPermission("read file?", permission.SeverityLow)
	if dec != permission.DecisionAllow {
		t.Fatalf("wheel + '1' must Allow, got %v", dec)
	}
}

// TestPromptPermissionLoneEscStillDenies: a genuine lone Esc (no follow-up
// within the window) must still deny — the sequence-vs-lone-Esc fix must
// not change Esc semantics.
func TestPromptPermissionLoneEscStillDenies(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.writer = io.Discard
	go func() {
		tu.permKeyCh <- 0x1b // nothing follows
	}()
	dec := tu.PromptPermission("read file?", permission.SeverityLow)
	if dec != permission.DecisionDeny {
		t.Fatalf("lone Esc must Deny, got %v", dec)
	}
}

// ── swallowCSI unit tests ────────────────────────────────────────────────

// TestSwallowCSISeqs drives swallowCSI with representative sequences and
// asserts the returned final byte plus a fully drained channel.
func TestSwallowCSISeqs(t *testing.T) {
	cases := []struct {
		name  string
		seq   string // full CSI sequence INCLUDING the leading "ESC ["
		final rune
	}{
		{"SGR wheel", "\x1b[<64;103;15M", 'M'},
		{"Delete", "\x1b[3~", '~'},
		{"ArrowUp", "\x1b[A", 'A'},
		{"Home", "\x1b[H", 'H'},
		{" bracketed paste open", "\x1b[200~", '~'},
		{"X10 wheel", "\x1b[M\x60\xc2\xc5", 'M'},
		{"SS3-like letter param", "\x1bO", 'O'},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tu := newTestTUI()
			for _, r := range tc.seq {
				tu.keyCh <- r
			}
			<-tu.keyCh // ESC
			if tc.seq[1] == 'O' {
				// Not a CSI (SS3) — swallowCSI is only called for '['; skip.
				return
			}
			<-tu.keyCh // '['
			c2 := <-tu.keyCh
			final := tu.swallowCSI(tu.keyCh, c2)
			if final != tc.final {
				t.Fatalf("final = %q, want %q", final, tc.final)
			}
			select {
			case r := <-tu.keyCh:
				t.Fatalf("channel not drained, leftover %q", r)
			default:
			}
		})
	}
}

// TestSwallowCSITruncated feeds a truncated CSI (ESC [ '<' then nothing) —
// swallowCSI must return 0 on timeout instead of blocking forever. ('<' is
// below the 0x40-0x7E final-byte range, so it enters the parameter loop.)
func TestSwallowCSITruncated(t *testing.T) {
	tu := newTestTUI()
	start := time.Now()
	final := tu.swallowCSI(tu.keyCh, '<')
	if final != 0 {
		t.Fatalf("truncated CSI must return 0, got %q", final)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("swallowCSI blocked %v on truncated input", elapsed)
	}
}
