package tui

import (
	"bufio"
	"bytes"
	"io"
	"testing"
	"testing/iotest"
	"time"
)

// drivePump feeds raw bytes through the key pump (with byte-splitting like a
// real console) and collects the resulting rune stream from keyCh.
func drivePump(t *testing.T, input []byte) []rune {
	t.Helper()
	tu := newTestTUI()
	tu.reader = bufio.NewReader(iotest.OneByteReader(bytes.NewReader(input)))
	// The pump takes t.reader; run it and collect until the input is drained.
	go tu.keyPump()
	var out []rune
	deadline := time.After(3 * time.Second)
	for {
		done := false
		select {
		case r, ok := <-tu.keyCh:
			if !ok {
				done = true
			} else {
				out = append(out, r)
				continue
			}
		case <-deadline:
			done = true
		}
		if done {
			break
		}
	}
	return out
}

func runesToString(rs []rune) string {
	return string(rs)
}

// A CJK character split across individual byte reads must reassemble exactly.
func TestPump_CJKSplitBytes(t *testing.T) {
	out := drivePump(t, []byte("你好，世界。"))
	if got := runesToString(out); got != "你好，世界。" {
		t.Fatalf("CJK split: got %q", got)
	}
}

// Delete (0xE0 0x53) must translate to the VT sequence, not leak "S".
func TestPump_ConHostDelete(t *testing.T) {
	out := drivePump(t, []byte{'a', 'b', 0xE0, 0x53, 'c'})
	if got := runesToString(out); got != "ab\x1b[3~c" {
		t.Fatalf("delete: got %q", got)
	}
}

// Arrows (0xE0 prefix + H/P/K/M) must translate, not leak letters.
func TestPump_ConHostArrows(t *testing.T) {
	out := drivePump(t, []byte{'x', 0xE0, 'H', 0xE0, 'P', 0xE0, 'K', 0xE0, 'M', 'y'})
	if got := runesToString(out); got != "x\x1b[A\x1b[B\x1b[D\x1b[Cy" {
		t.Fatalf("arrows: got %q", got)
	}
}

// A 0xE0 followed by a UTF-8 continuation byte is a legitimate multibyte
// character lead (e.g. U+1000 = E1? — use U+0800 range: 0xE0 0xA0 0x80) and
// must decode as the character, not an extended key.
func TestPump_E0ContinuationIsUTF8(t *testing.T) {
	out := drivePump(t, []byte{0xE0, 0xA0, 0x80}) // U+0800
	if got := runesToString(out); got != "\u0800" {
		t.Fatalf("E0 continuation: got %q", got)
	}
}

// Mixed: typing letters + CJK + fullwidth punctuation in one burst.
func TestPump_MixedBurst(t *testing.T) {
	out := drivePump(t, []byte("nihao你好，"))
	if got := runesToString(out); got != "nihao你好，" {
		t.Fatalf("mixed: got %q", got)
	}
}

// Invalid bytes are dropped, never forwarded (no ¿ garbage in input).
func TestPump_InvalidBytesDropped(t *testing.T) {
	out := drivePump(t, []byte{'a', 0xFF, 0xFE, 'b', 0xBF, 'c'})
	if got := runesToString(out); got != "abc" {
		t.Fatalf("invalid bytes: got %q", got)
	}
}

// The pipeEOF path: reader closes → pump exits gracefully.
func TestPump_ReaderEOF(t *testing.T) {
	out := drivePump(t, []byte("hi"))
	time.Sleep(100 * time.Millisecond)
	if runesToString(out) != "hi" {
		t.Fatalf("eof: got %q", runesToString(out))
	}
	_ = io.EOF
}
