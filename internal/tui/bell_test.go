package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestEndStreamBell verifies the task-completion bell: BEL (\x07) fires only
// when the turn ran ≥30s AND the bell is enabled — short turns and opt-out
// both stay silent.
func TestEndStreamBell(t *testing.T) {
	newTUI := func(bellOn bool, turnDur time.Duration) (*TUI, *bytes.Buffer) {
		buf := &bytes.Buffer{}
		tu := &TUI{
			theme: "dark", lang: "zh-CN", rawMode: false,
			bellOn: bellOn, writer: buf,
			streamDone: make(chan struct{}, 1),
		}
		tu.turnStart = time.Now().Add(-turnDur)
		return tu, buf
	}

	// Long turn + bell on → BEL present.
	tu, buf := newTUI(true, 40*time.Second)
	tu.EndStream()
	if !strings.Contains(buf.String(), "\x07") {
		t.Fatalf("expected BEL after a >30s turn (bell on): %q", buf.String())
	}

	// Short turn → silent.
	tu, buf = newTUI(true, 5*time.Second)
	tu.EndStream()
	if strings.Contains(buf.String(), "\x07") {
		t.Fatalf("unexpected BEL on a short turn: %q", buf.String())
	}

	// Long turn + bell off → silent.
	tu, buf = newTUI(false, 40*time.Second)
	tu.EndStream()
	if strings.Contains(buf.String(), "\x07") {
		t.Fatalf("unexpected BEL with bell disabled: %q", buf.String())
	}
}
