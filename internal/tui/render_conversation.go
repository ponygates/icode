package tui

import (
	"fmt"
	"strings"
)

// maxToolLines caps the excerpt shown for a folded tool card.
const maxToolLines = 8

func (t *TUI) messageLinesW(m Message, width int) []string {
	switch m.Role {
	case RoleThinking:
		return thinkingLines(m.Content, width)
	case RoleUser:
		// Claude Code parity: the user's own prompt renders Markdown (code
		// blocks, inline code, lists) the same way replies do.
		if t.rawMode {
			return t.renderMarkdown(m.Content, t.paint("orange", "❯ "), "    ", width)
		}
		return wrapPrefixed(t.paint("orange", "❯ ")+"  ", "    ", m.Content, width)
	case RoleAssistant:
		if t.rawMode {
			// Keep prefix and cont equal ("  " vs "  "): an empty first-line
			// prefix with a 2-space continuation produced a hanging indent
			// on every wrapped CJK paragraph — line 0 flush left, all
			// following lines shifted right by 2 cells. Streaming (stream.go
			// printAssistant) and the plain-text path below already align
			// at 2/2; this must match or the same message visibly jumps
			// between layouts when it settles.
			return t.renderMarkdown(m.Content, "  ", "  ", width)
		}
		return wrapPrefixed("  ", "  ", m.Content, width)
	case RoleSystem:
		if t.rawMode {
			return t.renderMarkdown(m.Content, "  ", "  ", width)
		}
		return wrapPrefixed("  ", "  ", m.Content, width)
	case RoleError:
		return wrapPrefixed(t.paint("red", "× ")+"  ", "    ", m.Content, width)
	case RoleTool:
		var out []string
		// opencode-style fold indicator: ▸ collapsed / ▾ expanded. Clicking the
		// header row toggles this block (see handleMouse → toolHeadRows).
		marker := t.paint("yellow", "▸")
		if !m.Folded {
			marker = t.paint("yellow", "▾")
		}
		head := marker + " " + t.paint("cyan", "⏺ "+m.Tool)
		// Hide empty/no-op parameter objects like "{}" so the tool line
		// shows "* git_status" instead of "* git_status {}".
		args := strings.TrimSpace(m.ToolArgs)
		if args == "{}" || args == "" {
			args = ""
		}
		if args != "" {
			head += " " + truncate(args, 60)
		}
		// Clamp the head row to the content width: MCP tool names are long
		// (mcp_xxx_yyy_zzz) and 60 excerpt chars on top can exceed the
		// terminal width, which triggers autowrap and desynchronises the
		// whole frame (see the width-clip note in render()).
		if vw := visibleWidth(head); vw > width {
			head = truncVisible(head, width)
		}
		out = append(out, head)
		// In-flight tool with live output (bash): render a Claude Code-style
		// scrolling tail window — the LAST few lines, not the first — so the
		// user watches output advance in real time. The authoritative result
		// replaces this once the tool finishes (LiveTail is cleared then).
		if m.LiveTail != "" && !t.zenMode {
			const liveTailRows = 5
			tail := strings.TrimRight(m.LiveTail, "\n")
			lines := strings.Split(tail, "\n")
			if len(lines) > liveTailRows {
				lines = lines[len(lines)-liveTailRows:]
				out = append(out, t.paint("dim", "    ⎿ …"))
			}
			for _, l := range lines {
				for _, wl := range wrapPrefixed("    ⎿ ", "      ", l, width) {
					out = append(out, t.paint("dim", wl))
				}
			}
			return out
		}
		if m.Content != "" {
			// Per-block folding (Claude Code / opencode style): a folded card
			// shows the header plus a short excerpt; expanded shows everything.
			toolOutput := m.Content
			if t.zenMode {
				// Focus view (Claude Code parity): in zen/focus mode show only
				// the tool header — no output excerpt — so the transcript reads
				// as pure conversation with a one-line activity marker.
				out = append(out, t.paint("dim", "    ⎿ … (工具输出已折叠，/zen 展开)"))
				return out
			}
			if m.Folded {
				fold := strings.Split(toolOutput, "\n")
				if len(fold) > maxToolLines {
					toolOutput = strings.Join(fold[:maxToolLines], "\n") + "\n" +
						t.paint("dim", fmt.Sprintf("    ⎿  ... %d more lines (click ▸ to expand)", len(fold)-maxToolLines))
				}
			}
			for _, l := range wrapPrefixed("    ⎿ ", "      ", toolOutput, width) {
				out = append(out, t.paint("dim", l))
			}
		}
		return out
	}
	return wrapPrefixed("  ", "  ", m.Content, width)
}

// conversationLines builds the full-width conversation: every message (+ the
// in-flight stream). Turns are separated by a thin dim rule — the opencode
// message divider — so each new turn is visible at a glance while the chrome
// stays quiet. Tool messages belong to the assistant turn that invoked them,
// so they get no rule above. While the model is "thinking" (stream started but
// no tokens yet) a single animated spinner + gradient bar is shown.
//
// The returned map records, for every tool card, the display-line index of its
// header row → index into msgs. render() uses it to map a mouse click on a card
// header back to the message for per-block fold toggling.
func (t *TUI) conversationLines(msgs []Message, streaming bool, streamContent string, width int) ([]string, map[int]int) {
	var lines []string
	heads := map[int]int{}
	all := append([]Message{}, msgs...)
	if streaming {
		all = append(all, Message{Role: RoleAssistant, Content: streamContent})
	}
	for i, m := range all {
		// Replace the empty in-flight assistant message with the thinking line.
		if streaming && i == len(all)-1 && strings.TrimSpace(m.Content) == "" {
			continue
		}
		// A thin dim rule separates turns; tool messages belong to the
		// assistant turn that invoked them, so they get no rule above.
		if i > 0 && m.Role != RoleTool {
			lines = append(lines, t.paint("dim", repeat("─", width)))
		}
		if m.Role == RoleTool {
			heads[len(lines)] = i // header row of this tool card
		}
		lines = append(lines, t.messageLinesW(m, width)...)
	}
	if streaming && strings.TrimSpace(streamContent) == "" {
		lines = append(lines, "")
		// The thinking indicator lives at the bottom (drawInputBox), not
		// here — keeps the message area from repainting every frame.
	}
	return lines, heads
}

// convHeight returns the number of rows available for conversation content.
func (t *TUI) convHeight() int {
	// header(1) + hrule(1) + input borders(2) + input(1) + status(1)
	return t.height - 6
}

// totalConvLines counts all display lines for the current conversation.
func (t *TUI) totalConvLines(msgs []Message, streaming bool, streamContent string, width int) int {
	lines, _ := t.conversationLines(msgs, streaming, streamContent, width)
	return len(lines)
}

func (t *TUI) scrollPgUp() {
	t.mu.Lock()
	bodyH := t.convHeight()
	if bodyH < 1 {
		bodyH = 10
	}
	t.scrollOffset += bodyH
	if t.welcomeVisible {
		t.welcomeVisible = false
	}
	t.mu.Unlock()
	t.scheduleRender()
}

func (t *TUI) scrollPgDn() {
	t.mu.Lock()
	bodyH := t.convHeight()
	if bodyH < 1 {
		bodyH = 10
	}
	t.scrollOffset -= bodyH
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.mu.Unlock()
	t.scheduleRender()
}

func (t *TUI) scrollToTop() {
	t.mu.Lock()
	msgs := append([]Message{}, t.messages...)
	lines, _ := t.conversationLines(msgs, t.streaming, t.streamBuf.String(), t.width)
	total := len(lines)
	bodyH := t.convHeight()
	if total > bodyH {
		t.scrollOffset = total - bodyH
	}
	t.mu.Unlock()
	t.scheduleRender()
}

// scrollToBottom resumes auto-follow (scroll to latest content).
func (t *TUI) scrollToBottom() {
	t.mu.Lock()
	t.scrollOffset = 0
	t.mu.Unlock()
	t.scheduleRender()
}

func (t *TUI) scrollUpSmall() {
	t.mu.Lock()
	t.scrollOffset += 3
	t.mu.Unlock()
	t.scheduleRender()
}

func (t *TUI) scrollDownSmall() {
	t.mu.Lock()
	t.scrollOffset -= 3
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.mu.Unlock()
	t.scheduleRender()
}

// canScroll reports whether the conversation has more display lines than the
// visible body, i.e. the viewport can be scrolled. It snapshots the needed
// state under the lock so callers (key handler) don't race the stream.
func (t *TUI) canScroll() bool {
	t.mu.Lock()
	streaming := t.streaming
	msgs := append([]Message{}, t.messages...)
	streamContent := t.streamBuf.String()
	W := t.width
	H := t.height
	t.mu.Unlock()
	bodyH := H - 4
	if bodyH < 3 {
		bodyH = 3
	}
	total := t.totalConvLines(msgs, streaming, streamContent, W)
	return total > bodyH
}

// scrollUp moves the conversation viewport up by n display lines (towards
// older content). render() clamps scrollOffset to the maximum, so over-
// scrolling is harmless. It also dismisses the welcome banner if present.
func (t *TUI) scrollUp(n int) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.welcomeVisible {
		t.welcomeVisible = false
	}
	t.scrollOffset += n
	t.scheduleRender()
}

// scrollDown moves the conversation viewport down by n display lines (towards
// newer content), never past the bottom (auto-follow at offset 0).
func (t *TUI) scrollDown(n int) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollOffset -= n
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.scheduleRender()
}
