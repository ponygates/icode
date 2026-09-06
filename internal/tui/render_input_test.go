package tui

import (
	"bytes"
	"strings"
	"fmt"
	"testing"
)

// Reproduction of the user report: while typing, the input text appears BOTH
// in the input box AND in the log area above ("交替出现在输入框和上部").
// The render must contain the typed text EXACTLY ONCE, and it must appear on
// the input row (the bottom of the frame), never inside the log region.
func TestRender_InputTextAppearsExactlyOnce(t *testing.T) {
	tui := newTestTUI()
	var buf bytes.Buffer
	tui.writer = &buf
	tui.statusVisible = true
	tui.width = 100
	tui.height = 30
	tui.rawMode = true
	tui.messages = []Message{
		{Role: RoleUser, Content: "第一条消息"},
		{Role: RoleAssistant, Content: "第一条回复"},
	}

	for _, typed := range []string{"你好", "w k kdf afkajk lwqw", "你，学会去。什么"} {
		buf.Reset()
		tui.mu.Lock()
		tui.inputBuf = typed
		tui.cursor = len([]rune(typed))
		tui.mu.Unlock()
		tui.render()
		out := buf.String()

		n := strings.Count(out, typed)
		if n != 1 {
			// Locate each occurrence for the failure report.
			var at []int
			for k := 0; ; {
				k = strings.Index(out[k:], typed)
				if k < 0 {
					break
				}
				at = append(at, k)
				k += len(typed)
			}
			var parts []string
			for _, k := range at {
				lo := k - 60
				if lo < 0 {
					lo = 0
				}
				parts = append(parts, sprintf("@%d: %q", k, out[lo:k+len(typed)+20]))
			}
			t.Fatalf("typed %q appears %d times: %s", typed, n, strings.Join(parts, " | "))
		}
	}
}

// Two consecutive keystrokes must not leave stale input content in the log
// region (the row-diff must clean up moved rows).
func TestRender_ConsecutiveTyping_NoStaleCopies(t *testing.T) {
	tui := newTestTUI()
	var buf bytes.Buffer
	tui.writer = &buf
	tui.statusVisible = true
	tui.width = 100
	tui.height = 30
	tui.rawMode = true
	tui.mu.Lock()
	tui.inputBuf = ""
	tui.mu.Unlock()
	tui.render()

	typed := "正在输入的中文内容"
	for i, r := range []rune(typed) {
		buf.Reset()
		tui.mu.Lock()
		tui.inputBuf += string(r)
		tui.cursor = len([]rune(tui.inputBuf))
		tui.mu.Unlock()
		tui.render()
		out := buf.String()
		needle := string([]rune(typed)[:i+1])
		if n := strings.Count(out, needle); n != 1 {
			// Show every occurrence with context.
			var parts []string
			for k := 0; ; {
				rel := strings.Index(out[k:], needle)
				if rel < 0 {
					break
				}
				k += rel
				lo := k - 50
				if lo < 0 {
					lo = 0
				}
				hi := k + len(needle) + 20
				if hi > len(out) {
					hi = len(out)
				}
				parts = append(parts, sprintf("MATCH@%d (winpos %d) bytes=% x ctx=% x", k, k-lo, out[k:k+len(needle)], out[lo:hi]))
				k += len(needle)
			}
			t.Fatalf("after %d chars, input text appears %d times: %s", i+1, n, strings.Join(parts, " || "))
		}
	}
}

// sprintf avoids importing fmt for one helper.
func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
