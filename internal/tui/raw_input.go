package tui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// ── Raw mode (full screen) ───────────────────────────────────────

// escFollowTimeout is how long handleKey waits after a lone ESC for a possible
// follow-up byte (arrow keys, Alt+key, CSI) before treating it as a standalone
// Esc. With the keyPump escape-sequence reassembly (pumpEscape) a sequence is
// pushed onto keyCh as ONE atomic burst, so this timeout only ever fires for a
// genuinely lone Esc — the pump-side timeouts below do the timing work.
const escFollowTimeout = 10 * time.Millisecond

// escAggFirstWait is how long pumpEscape waits for the byte right after an ESC
// before deciding it was a standalone Esc press. Terminals write a sequence as
// one burst, but ConPTY pipes and heavy load CAN split it — the follow-up then
// lands a little late. 30ms keeps lone-Esc latency imperceptible while
// absorbing the split-burst latency that used to leak "[3~" fragments into the
// input box ("Delete/scroll-wheel prints garbage" bug).
const escAggFirstWait = 30 * time.Millisecond

// escAggSeqWait is the same wait for bytes INSIDE a sequence (after "ESC[" or
// "ESC O"). ConPTY/Windows Terminal occasionally split one input event (a
// wheel report) across two writes whose gap can exceed 15ms under load —
// measured repro: "ESC[<" + 40ms + "64;34;29M". A too-short window made the
// pump flush a HALF sequence (the split that produced the wheel-garbage
// regression), so keep this comfortably above observed write latencies.
// Sequence bytes normally arrive back-to-back (the wait only applies while
// starved), so the cost is zero on the common path.
const escAggSeqWait = 40 * time.Millisecond

// escSwallowWait is the follow-up window used by the SEQUENCE consumers
// (swallowCSI, drainStream's u/c2 reads, X10 coordinate reads) — it must be
// strictly LARGER than escAggSeqWait: when the pump DOES split and flush a
// half sequence, the payload arrives up to the real write-gap later, and the
// consumer must still catch it instead of giving up and letting the printable
// tail ("64;34;29M") be inserted into the input buffer as if typed. The lone
// Esc decision is NOT affected — that is escFollowTimeout on the FIRST byte
// after ESC, which must stay snappy for Esc to feel instant.
const escSwallowWait = 100 * time.Millisecond

// escAggMaxLen caps sequence reassembly so a pathological stream (e.g. a
// bracketed paste embedding raw ESC bytes) can never wedge the pump; past the
// cap the bytes collected so far are flushed as-is, degrading to the pre-
// reassembly byte-by-byte behaviour.
const escAggMaxLen = 512

// keyRuneReader adapts the key-pump channel to io.RuneReader so the shared
// escape-sequence parsers (handleMouse / readPaste) can read follow-up bytes
// from the same single-owner stream instead of grabbing t.reader directly.
// site names the consumer for traceKeySite; mouse reads pass "" (untraced —
// wheel/motion events would flood the log, and they cannot race with the
// main loop anyway: handleMouse runs inside handleKey on its goroutine).
type keyRuneReader struct {
	ch   <-chan rune
	site string
}

func (k keyRuneReader) ReadRune() (rune, int, error) {
	r, ok := <-k.ch
	if !ok {
		return 0, 0, io.EOF
	}
	if k.site != "" {
		traceKeySite(k.site, r)
	}
	return r, 1, nil
}

// fullRepaintSoon asks the next render() to clear the screen first — used
// after detecting a conhost ECHO revival, so characters conhost echoed into
// the conversation area get wiped instead of lingering between unchanged rows.
func (t *TUI) fullRepaintSoon() {
	t.mu.Lock()
	t.fullRepaintPending = true
	t.mu.Unlock()
	t.scheduleRender()
}

// rawBytePump is the ONLY goroutine that reads from t.reader while the
// raw-mode TUI is running. It forwards raw bytes to byteCh, from where keyPump
// consumes them. Splitting the pump in two is what enables sequence
// reassembly: bufio.Reader has no deadline reads, but a channel select does,
// so keyPump can wait for the rest of an ESC-burst with a timeout without
// ever touching the reader itself.
func (t *TUI) rawBytePump() {
	br, ok := t.reader.(*bufio.Reader)
	if !ok {
		close(t.byteCh)
		return
	}
	defer close(t.byteCh)
	for {
		b, err := br.ReadByte()
		if err != nil {
			return // EOF or read error — session input is gone
		}
		t.traceByte(b)
		// Per-key raw-mode enforcement: conhost can revive ECHO between
		// watchResize's 150ms ticks (buffer mutations, IME wrappers). If the
		// bit was live when this key arrived, conhost already echoed it at
		// the physical cursor — re-clear the flags now so the NEXT key is
		// clean, and schedule a full repaint to wash off the stray char.
		if hardenConsoleInput() {
			t.fullRepaintSoon()
		}
		select {
		case t.byteCh <- b:
		case <-t.keyStop:
			return
		}
	}
}

// nextByte reads the next raw byte from byteCh. timeout == 0 blocks until a
// byte, the pump stops, or byteCh closes; otherwise it also gives up after
// the timeout (the byte, if it arrives later, stays buffered for the next
// call). Returns ok=false on timeout/stop/EOF.
func (t *TUI) nextByte(timeout time.Duration) (byte, bool) {
	if timeout <= 0 {
		select {
		case b, ok := <-t.byteCh:
			return b, ok
		case <-t.keyStop:
			return 0, false
		}
	}
	select {
	case b, ok := <-t.byteCh:
		return b, ok
	case <-time.After(timeout):
		return 0, false
	case <-t.keyStop:
		return 0, false
	}
}

// dispatchRunes forwards a fully-reassembled unit (a single key rune, an
// entire escape sequence, or a degraded partial sequence) to keyCh — or to
// permKeyCh while a permission prompt is pending. Runes are pushed back-to-
// back so the consumer's escFollowTimeout select finds them already queued:
// an atomic burst on a buffered channel cannot lose the race.
func (t *TUI) dispatchRunes(rs []rune) {
	for _, r := range rs {
		t.mu.Lock()
		pending := t.permPending
		t.mu.Unlock()
		if pending {
			select {
			case t.permKeyCh <- r:
			case <-t.keyStop:
				return
			}
		} else {
			select {
			case t.keyCh <- r:
			case <-t.keyStop:
				return
			}
		}
	}
}

// pumpEscape reassembles the escape sequence that starts with the ESC the
// caller already consumed. It is the fix for the "Delete / mouse wheel prints
// garbage" bug: previously every byte was forwarded individually, so a burst
// split by ConPTY (or delayed by load) made handleKey's 10ms follow-up window
// expire, the ESC was treated as a lone Esc, and the printable rest of the
// sequence ("[3~", "[<64;103;15M", …) was inserted into the input box as
// literal text. Reassembling first means the consumer always gets the whole
// sequence in one burst.
//
// The X10 mouse case ("\x1b[M" + 3 RAW coordinate bytes) additionally fixes
// the ">95 columns" wheel failure: X10 coordinates are col+32 / row+32 and
// exceed 0x7F on wide/tall windows, which the incremental UTF-8 decoder used
// to swallow. Reassembly reads them as raw bytes and dispatches them as-is.
//
// Degradation guarantees: on timeout or overlength the bytes collected so far
// are flushed unchanged (never silently dropped, never orphaned mid-sequence
// state left behind), which is exactly the pre-reassembly behaviour.
//
// It returns a byte to requeue into the main pump loop (ok=true) for the one
// case where the byte after ESC is a UTF-8 lead byte: an Esc press followed
// immediately by an IME commit. Dispatching that as "Alt+<byte>" would eat
// the first character, so the ESC goes out alone and the lead byte is handed
// back to the normal UTF-8 path.
func (t *TUI) pumpEscape() (byte, bool) {
	// First byte after ESC — non-blocking drain first (the common case: the
	// whole burst is already sitting in byteCh), then wait.
	first, ok := t.nextByte(escAggFirstWait)
	if !ok {
		t.dispatchRunes([]rune{0x1b}) // genuinely lone Esc
		return 0, false
	}

	switch first {
	case '[':
		// CSI … final-byte-terminated, plus the X10 mouse special case.
		seq := []rune{0x1b, '['}
		x10 := false
		for {
			b, ok := t.nextByte(escAggSeqWait)
			if !ok {
				t.dumpInputTrace("csi-agg-split")
				t.dispatchRunes(seq) // degraded: flush what we have
				return 0, false
			}
			seq = append(seq, rune(b))
			if b >= 0x40 && b <= 0x7E { // final byte terminates CSI
				// "\x1b[M" with no parameters is X10 mouse: exactly three
				// RAW bytes follow — any value 0x00-0xFF, NOT UTF-8.
				x10 = len(seq) == 3 && b == 'M'
				break
			}
			if len(seq) >= escAggMaxLen {
				t.dumpInputTrace("csi-agg-overflow")
				break
			}
		}
		if x10 {
			for i := 0; i < 3; i++ {
				b, ok := t.nextByte(escAggSeqWait)
				if !ok {
					t.dumpInputTrace("x10-agg-split")
					break // flush what arrived; consumer's timeout handles the rest
				}
				seq = append(seq, rune(b))
			}
		}
		t.dispatchRunes(seq)
		return 0, false
	case 'O':
		// SS3 (ESC O …) — one more byte, e.g. arrows in application mode.
		seq := []rune{0x1b, 'O'}
		if b, ok := t.nextByte(escAggSeqWait); ok {
			seq = append(seq, rune(b))
		} else {
			t.dumpInputTrace("ss3-agg-split")
		}
		t.dispatchRunes(seq)
		return 0, false
	default:
		// ESC + byte that is not [ or O: either Alt+key (the terminal encodes
		// Alt+k as ESC k) or an Esc press immediately followed by a
		// multi-byte UTF-8 character (IME commit right after Esc). Only the
		// UTF-8 lead case needs special handling — hand it back to the main
		// loop so the character decodes normally.
		if first >= 0xC0 && first <= 0xF7 {
			t.dispatchRunes([]rune{0x1b})
			return first, true
		}
		t.dispatchRunes([]rune{0x1b, rune(first)})
		return 0, false
	}
}

// keyPump consumes raw bytes from byteCh (fed by rawBytePump) and forwards
// decoded keys to keyCh (normal keys) or permKeyCh (decision keys while a
// permission prompt is pending). Escape sequences are reassembled atomically
// by pumpEscape before dispatch — see its comment for the garbage-leak bug
// this fixes. Because all consumers — the main loop, drainStream and
// PromptPermission — read from these channels, no two goroutines ever touch
// the terminal reader concurrently: keys cannot be stolen and bufio.Reader
// stays race-free.
func (t *TUI) keyPump() {
	defer close(t.keyReaderDone)
	defer close(t.keyCh)
	acc := make([]byte, 0, 4)
	var requeue byte
	hasRequeue := false
	for {
		var b byte
		if hasRequeue {
			b, hasRequeue = requeue, false
		} else {
			nb, ok := t.nextByte(0)
			if !ok {
				return // pump stopped or byteCh closed (stdin EOF)
			}
			b = nb
		}

		// ── ConHost extended keys ────────────────────────────────────────
		// Windows consoles deliver arrows/Home/End/Delete/Insert/PgUp/PgDn
		// as a 0xE0 (or legacy 0x00) prefix byte + a scan-code letter. The
		// prefix is INVALID UTF-8 — a rune-based reader misreads it and the
		// scan-code letter then leaks into the input as garbage ("S" for
		// Delete, "G" for Home, …). Translate the pair into the VT sequence
		// the key parser expects. A 0xE0 followed by a UTF-8 continuation
		// byte (0x80-0xBF) is a legit multibyte character lead, not a prefix.
		if b == 0x00 || b == 0xE0 {
			nb, ok := t.nextByte(0)
			if !ok {
				return
			}
			if b == 0xE0 && nb >= 0x80 && nb <= 0xBF {
				acc = append(acc[:0], b, nb)
				continue
			}
			var seq string
			switch nb {
			case 'H':
				seq = "\x1b[A" // ↑
			case 'P':
				seq = "\x1b[B" // ↓
			case 'K':
				seq = "\x1b[D" // ←
			case 'M':
				seq = "\x1b[C" // →
			case 'G':
				seq = "\x1b[H" // Home
			case 'O':
				seq = "\x1b[F" // End
			case 'S':
				seq = "\x1b[3~" // Delete
			case 'R':
				seq = "\x1b[2~" // Insert
			case 'I':
				seq = "\x1b[5~" // PgUp
			case 'Q':
				seq = "\x1b[6~" // PgDn
			default:
				continue // unknown extended key — drop silently
			}
			runes := make([]rune, 0, len(seq))
			for _, c := range seq {
				runes = append(runes, c)
			}
			t.dispatchRunes(runes)
			continue
		}

		// ── Escape-sequence reassembly ───────────────────────────────────
		if b == 0x1b {
			if rb, ok := t.pumpEscape(); ok {
				requeue = rb
				hasRequeue = true
			}
			continue
		}

		// ── Incremental UTF-8 decoding ───────────────────────────────────
		// Byte-level accumulation handles IME commit bursts that can split a
		// multibyte character across reads — a rune-based reader misreads
		// such splits and half characters surfaced as "¿" garbage. ASCII
		// fast-paths through with zero overhead.
		if b < 0x80 {
			acc = acc[:0]
			t.dispatchRunes([]rune{rune(b)})
			continue
		}

		acc = append(acc, b)
		need := 1
		switch {
		case acc[0] >= 0xF0:
			need = 4
		case acc[0] >= 0xE0:
			need = 3
		case acc[0] >= 0xC0:
			need = 2
		}
		if len(acc) < need {
			continue // wait for the rest of the character
		}
		r, size := utf8.DecodeRune(acc)
		if r == utf8.RuneError || size != need {
			// Invalid sequence — drop it entirely instead of leaking
			// replacement characters into the input.
			t.dumpInputTrace("utf8-invalid")
			acc = acc[:0]
			continue
		}
		acc = acc[:0]
		t.dispatchRunes([]rune{r})
	}
}
func (t *TUI) nextKey() (rune, bool) {
	r, ok := <-t.keyCh
	return r, ok
}

// nextKeyTimeout reads the next key like nextKey, but gives up after d.
// Escape-sequence bursts arrive as one write, so a follow-up byte that
// hasn't landed within a few milliseconds never will: the parser treats the
// sequence as complete/truncated instead of blocking forever and swallowing
// the user's subsequent keystrokes (the "keys get eaten after pressing F1"
// class of bug — an SS3/DCS fragment with no terminator used to make the
// drain loop consume everything until an 'm'/'M'/'~' happened by).
func (t *TUI) nextKeyTimeout(d time.Duration) (rune, bool) {
	return readRuneTimeout(t.keyCh, d)
}

// readRuneTimeout reads one rune from ch, giving up after d. Shared by the
// keyCh consumers (main loop) and the permKeyCh consumer (PromptPermission)
// so both can swallow escape sequences with the same truncated-burst guard.
func readRuneTimeout(ch <-chan rune, d time.Duration) (rune, bool) {
	select {
	case r, ok := <-ch:
		return r, ok
	case <-time.After(d):
		return 0, false
	}
}

// swallowCSI consumes the rest of a CSI ("ESC [") sequence from ch, given c2
// — the first rune after the '['. It returns the sequence's final byte
// ('A', 'M', '~', …) or 0 when the burst was truncated. The key-pump
// reassembly guarantees the whole sequence is already queued on the channel,
// so the reads complete back-to-back; the timeouts are purely a fallback.
//
// Callers that only need to DISCARD a sequence (drainStream wheel reports,
// permission prompts) use this instead of hand-rolled one-byte peeks, which
// used to leak the printable payload — e.g. a wheel report's coordinates
// ("64;103;15") into the queue buffer, or its digits ('1'/'2'/'3') into the
// permission DECISION keys.
func (t *TUI) swallowCSI(ch <-chan rune, c2 rune) rune {
	// X10 mouse: "\x1b[M" + 3 raw coordinate bytes (any value 0x00-0xFF).
	if c2 == 'M' {
		readRuneTimeout(ch, escSwallowWait)
		readRuneTimeout(ch, escSwallowWait)
		readRuneTimeout(ch, escSwallowWait)
		return 'M'
	}
	// c2 already is the final byte (arrow keys, Z, …).
	if c2 >= 0x40 && c2 <= 0x7E {
		return c2
	}
	// Parameter/intermediate bytes (digits, ';', '<', '?', …) until final.
	// escSwallowWait (> pump's escAggSeqWait) is what makes a pump-split
	// half sequence survivable: the late payload still lands inside this
	// window, so the coordinates never leak into the queue buffer.
	for i := 0; i < 64; i++ {
		rr, ok := readRuneTimeout(ch, escSwallowWait)
		if !ok {
			return 0 // truncated burst
		}
		if rr >= 0x40 && rr <= 0x7E {
			return rr
		}
	}
	return 0 // pathological length — stop before we eat the next keystroke
}

// consumeSeqTail swallows the remainder of a "~"-terminated CSI sequence
// (e.g. the ";5" of a Ctrl+Delete "3;5~") up to and including the final '~'.
// On timeout the sequence was truncated, so it returns immediately — the
// next keystroke can never be misread as part of it.
func (t *TUI) consumeSeqTail() {
	for {
		rr, ok := t.nextKeyTimeout(escFollowTimeout)
		if !ok || rr == '~' {
			return
		}
	}
}

// consumeSeqFinal is consumeSeqTail that reports the terminator. Digit-
// prefixed sequences are ambiguous ("1~" = Home, "1;5A" = Ctrl+Up), so
// callers need to know whether the sequence ended in '~' before acting.
func (t *TUI) consumeSeqFinal() (rune, bool) {
	for {
		rr, ok := t.nextKeyTimeout(escFollowTimeout)
		if !ok {
			return 0, false
		}
		if rr == '~' || (rr >= 'A' && rr <= 'Z') || (rr >= 'a' && rr <= 'z') {
			return rr, true
		}
	}
}

// traceByte appends a raw stdin byte to the input-trace ring. Called for
// every byte from the single key-pump goroutine; the mutex keeps the ring
// consistent for a concurrent dump from the main loop.
func (t *TUI) traceByte(b byte) {
	t.mu.Lock()
	t.inputTrace[t.inputTracePos] = b
	t.inputTracePos = (t.inputTracePos + 1) % len(t.inputTrace)
	t.mu.Unlock()
}

// dumpInputTrace writes the ring contents (oldest first, hex) to cli.log
// tagged with the anomaly that triggered the dump. The trace is what turns
// a vague "garbled text in the input box" report into a reproducible byte
// stream.
func (t *TUI) dumpInputTrace(reason string) {
	t.mu.Lock()
	var sb strings.Builder
	for i := 0; i < len(t.inputTrace); i++ {
		pos := (t.inputTracePos + i) % len(t.inputTrace)
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(fmt.Sprintf("%02x", t.inputTrace[pos]))
	}
	t.mu.Unlock()
	writeCliLog(fmt.Sprintf("[tui] input-trace(%s): %s", reason, sb.String()))
}
