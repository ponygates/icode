package tui

import (
	"strings"
	"testing"
)

func TestRenderMarkdownSmoke(t *testing.T) {
	tui := &TUI{color: true, width: 80}

	sample := "**Bold** and *italic* and `code`. Visit [iCode](https://example.com).\n" +
		"# Heading 1\n" +
		"## Heading 2\n" +
		"> a blockquote line that should wrap nicely across the terminal width\n" +
		"- item one\n" +
		"- item two with **bold** inside\n" +
		"1. first\n" +
		"2. second\n" +
		"---\n" +
		"```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n" +
		"Plain trailing paragraph with Chinese 中文测试 to verify CJK width."

	lines := tui.renderMarkdown(sample, "  * ", "    ", tui.width)
	if len(lines) == 0 {
		t.Fatal("expected non-empty markdown output")
	}
	// Every line must fit within width (counting CJK as width 2).
	for _, ln := range lines {
		if w := runeWidthStr(stripANSI(ln)); w > tui.width {
			t.Errorf("line exceeds width (%d > %d): %q", w, tui.width, ln)
		}
	}
	// Headings, code fence, list marker should be present somewhere.
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"*", "─", "│", ">", "┌", "└"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected decorative marker %q in output", want)
		}
	}
}

// stripANSI removes escape sequences so we can measure display width.
func stripANSI(s string) string {
	var b strings.Builder
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		if runes[i] == '\x1b' {
			j := i + 1
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

func TestWrapANSIPreservesStyle(t *testing.T) {
	tui := &TUI{color: true, width: 30}
	in := "\x1b[1mbold text that is long enough to wrap\x1b[0m"
	out := tui.wrapANSI("  ", "  ", in, tui.width)
	if len(out) < 2 {
		t.Fatalf("expected wrapping, got %d lines", len(out))
	}
	// Each continuation line should re-open the bold code and close it.
	for _, ln := range out[1:] {
		if !strings.Contains(ln, "\x1b[1m") {
			t.Errorf("continuation line missing reopened style: %q", ln)
		}
		if !strings.HasSuffix(ln, "\x1b[0m") {
			t.Errorf("continuation line missing reset: %q", ln)
		}
	}
}
