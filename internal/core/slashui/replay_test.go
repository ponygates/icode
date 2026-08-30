package slashui

import (
	"strings"
	"testing"
)

func TestTruncateIcode(t *testing.T) {
	if got := truncateIcode("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("字", 20)
	got := truncateIcode(long, 5)
	if len([]rune(got)) != 6 { // 5 runes + ellipsis
		t.Errorf("got %q (len %d)", got, len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("no ellipsis: %q", got)
	}
	// Whitespace is trimmed first.
	if got := truncateIcode("  padded  ", 20); got != "padded" {
		t.Errorf("got %q", got)
	}
}

func TestFromShort(t *testing.T) {
	if got := fromShort("abcdef1234567890"); got != "abcdef12" {
		t.Errorf("got %q", got)
	}
	if got := fromShort("abc"); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := fromShort(""); got != "" {
		t.Errorf("got %q", got)
	}
}
