package tui

import (
	"fmt"
	"strings"
	"time"
)

// ── StreamWriter ─────────────────────────────────────────────────

// AddMessage renders a complete message (user/system/error/assistant).
func (t *TUI) AddMessage(role Role, content string) {
	t.mu.Lock()
	switch role {
	case RoleUser:
		t.messages = append(t.messages, Message{Role: RoleUser, Content: content})
	case RoleAssistant:
		t.messages = append(t.messages, Message{Role: RoleAssistant, Content: content})
	case RoleSystem:
		t.messages = append(t.messages, Message{Role: RoleSystem, Content: content})
	case RoleError:
		t.messages = append(t.messages, Message{Role: RoleError, Content: content})
	case RoleThinking:
		t.messages = append(t.messages, Message{Role: RoleThinking, Content: content})
	default:
		t.messages = append(t.messages, Message{Role: RoleSystem, Content: content})
	}
	t.mu.Unlock()
	if t.rawMode {
		t.render()
	} else {
		t.printMessage(t.messages[len(t.messages)-1])
	}
}

// AddToolMessage records a tool invocation.
func (t *TUI) AddToolMessage(tool, toolArgs, content string) {
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleTool, Tool: tool, ToolArgs: toolArgs, Content: content})
	idx := len(t.messages) - 1
	t.mu.Unlock()
	if t.rawMode {
		t.render()
	} else {
		t.printMessage(t.messages[idx])
	}
}

// AppendToolResult appends result text to the most recent tool message.
func (t *TUI) AppendToolResult(content string) {
	t.mu.Lock()
	for i := len(t.messages) - 1; i >= 0; i-- {
		if t.messages[i].Role == RoleTool {
			if t.messages[i].Content != "" {
				t.messages[i].Content += "\n"
			}
			t.messages[i].Content += content
			idx := i
			t.mu.Unlock()
			if t.rawMode {
				t.render()
			} else {
				t.printMessage(t.messages[idx])
			}
			return
		}
	}
	t.mu.Unlock()
	if t.rawMode {
		t.render()
	}
}

// AppendToolProgress appends live tool output (bash stdout/stderr) to the
// most recent tool message. Repaints are throttled so a chatty process can't
// force a full-screen redraw per line.
func (t *TUI) AppendToolProgress(content string) {
	t.mu.Lock()
	if len(t.messages) == 0 {
		t.mu.Unlock()
		return
	}
	last := t.messages[len(t.messages)-1]
	if last.Role != RoleTool {
		// Progress arrived before the tool card (race with the tool_use
		// event) — surface it on a fresh tool message with the tool name.
		t.messages = append(t.messages, Message{Role: RoleTool, Tool: "…", ToolArgs: "", Content: content})
	} else if t.messages[len(t.messages)-1].Content != "" {
		t.messages[len(t.messages)-1].Content += content
	} else {
		t.messages[len(t.messages)-1].Content = content
	}
	t.mu.Unlock()
	if t.rawMode {
		t.scheduleRender()
	} else {
		fmt.Fprint(t.writer, content)
	}
}

// printMessage writes a single message to the line-mode writer.
func (t *TUI) printMessage(m Message) {
	switch m.Role {
	case RoleUser:
		fmt.Fprintf(t.writer, "  > %s\n\n", m.Content)
	case RoleAssistant:
		t.printAssistant(m.Content)
	case RoleSystem:
		fmt.Fprintf(t.writer, "  %s\n\n", m.Content)
	case RoleError:
		fmt.Fprintf(t.writer, "  × %s\n\n", m.Content)
	case RoleTool:
		toolArgs := m.ToolArgs
		if toolArgs == "{}" || strings.TrimSpace(toolArgs) == "" {
			toolArgs = ""
		}
		fmt.Fprintf(t.writer, "  » %s %s\n", m.Tool, truncate(toolArgs, 60))
		if m.Content != "" {
			for _, l := range strings.Split(m.Content, "\n") {
				fmt.Fprintf(t.writer, "    %s\n", l)
			}
		}
		fmt.Fprintln(t.writer)
	case RoleThinking:
		// Bordered "thinking" box. Top and bottom span the same 16 columns
		// (┐/┘ at col 15); the middle row carries a closing │ that lines up
		// with them, and the content is truncated to the 12-cell inner width
		// by *visible* columns so CJK text can't push the border out of line.
		const thinkInner = 12
		tc := fitVis(m.Content, thinkInner)
		fmt.Fprintf(t.writer, "  ┌─ thinking ─┐\n  │%s│\n  └────────────┘\n\n", tc)
	}
}

// printAssistant renders assistant text in line mode (* prefix).
func (t *TUI) printAssistant(text string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 {
			fmt.Fprintf(t.writer, "  * %s\n", line)
		} else {
			fmt.Fprintf(t.writer, "    %s\n", line)
		}
	}
	fmt.Fprintln(t.writer)
}

// AppendStream appends assistant text as it streams in.
func (t *TUI) AppendStream(text string) {
	t.mu.Lock()
	t.streamBuf.WriteString(text)
	t.mu.Unlock()
	if t.rawMode {
		t.ensureAnim()
		t.scheduleRender()
	} else {
		fmt.Fprint(t.writer, text)
	}
}

// scheduleRender coalesces full-screen redraws: rapid token bursts collapse
// into at most one repaint per ~25ms window instead of one per chunk. A direct
// t.render() call (e.g. at end-of-stream) bypasses the throttle.
func (t *TUI) scheduleRender() {
	if !t.rawMode {
		return
	}
	t.mu.Lock()
	if t.renderPending {
		t.mu.Unlock()
		return
	}
	t.renderPending = true
	t.mu.Unlock()

	t.renderTimer = time.AfterFunc(25*time.Millisecond, func() {
		t.mu.Lock()
		t.renderPending = false
		t.mu.Unlock()
		t.render()
	})
}

// EndStream finalizes the streaming turn.
func (t *TUI) EndStream() {
	final := strings.TrimSpace(t.streamBuf.String())
	if final != "" {
		t.mu.Lock()
		t.messages = append(t.messages, Message{Role: RoleAssistant, Content: final})
		t.mu.Unlock()
	}
	t.streamBuf.Reset()
	select {
	case t.streamDone <- struct{}{}:
	default:
	}
	if t.rawMode {
		t.render()
	}
}

// SetStatus records token usage and cost for the status bar.
func (t *TUI) SetStatus(input, output int, cacheHit float64, cost string) {
	t.promptTokens = input
	t.completionTokens = output
	t.cacheHitRate = cacheHit
	t.cost = cost
}
