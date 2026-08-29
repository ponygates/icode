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
	// New cards follow the global fold preference (/expand); individual clicks
	// override per block afterwards.
	t.messages = append(t.messages, Message{Role: RoleTool, Tool: tool, ToolArgs: toolArgs, Content: content, Folded: !t.toolFolded})
	idx := len(t.messages) - 1
	// While streaming this invocation is the "current work" shown in the
	// status bar; the result append clears it.
	if t.streaming {
		t.curTool = tool
	}
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
			t.curTool = "" // this invocation finished
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
		t.messages = append(t.messages, Message{Role: RoleTool, Tool: "…", ToolArgs: "", Content: content, Folded: !t.toolFolded})
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

// printAssistant renders assistant text in line mode. Non-streamed messages
// (history replay, slash-command output, /resume) get full Markdown rendering
// (headings, bold/italic, inline code, fenced blocks, lists, tables) exactly
// like raw mode — line mode previously dumped raw Markdown syntax.
func (t *TUI) printAssistant(text string) {
	width := 100
	if w, _, ok := t.termSize(); ok && w > 24 {
		width = w - 4
	}
	lines := t.renderMarkdown(text, "  ", "  ", width)
	for _, l := range lines {
		fmt.Fprintln(t.writer, l)
	}
	fmt.Fprintln(t.writer)
}

// AppendStream appends assistant text as it streams in. Model output is
// sanitised first: ANSI escapes and control bytes that a model might echo
// would otherwise corrupt the TUI layout and render as caret garbage such as
// "^¿^¿" between CJK runs. An escape split across two chunks is buffered in
// t.ansiPending and joined with the next chunk.
func (t *TUI) AppendStream(text string) {
	t.mu.Lock()
	clean, pending := sanitizeStreamText(t.ansiPending + text)
	t.ansiPending = pending
	t.streamBuf.WriteString(clean)
	t.mu.Unlock()
	if t.rawMode {
		t.ensureAnim()
		t.scheduleRender()
	} else {
		fmt.Fprint(t.writer, clean)
	}
}

// scheduleRender coalesces full-screen redraws: rapid token bursts collapse
// into at most one repaint per ~33ms window instead of one per chunk. A
// direct t.render() call (e.g. at end-of-stream) bypasses the throttle.
// 33ms ≈ 30fps — smooth enough for text streaming while keeping Win10
// conhost/Windows Terminal from visibly flickering on full-screen repaints.
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

	t.renderTimer = time.AfterFunc(33*time.Millisecond, func() {
		t.mu.Lock()
		t.renderPending = false
		t.mu.Unlock()
		t.render()
	})
}

// EndStream finalizes the streaming turn.
func (t *TUI) EndStream() {
	t.mu.Lock()
	final := strings.TrimSpace(sanitizeFullText(t.streamBuf.String()))
	t.ansiPending = ""
	bged := t.backgrounded
	// A turn just finished = recent user activity, so reset the idle clock
	// (used by the 3-minute auto-recap) instead of firing it right after a reply.
	t.lastActivity = time.Now()
	t.mu.Unlock()
	if final != "" {
		t.messages = append(t.messages, Message{Role: RoleAssistant, Content: final})
	}
	t.streamBuf.Reset()
	select {
	case t.streamDone <- struct{}{}:
	default:
	}
	if bged {
		// A Ctrl+B turn finished while the UI was free-running (this runs on
		// the engine's goroutine, so never call submit()/drainStream() here —
		// that would steal keys from the main loop). Announce and let the main
		// loop flush any queued message on its next iteration.
		t.mu.Lock()
		t.backgrounded = false
		t.streaming = false
		t.pendingQueueFlush = true
		t.mu.Unlock()
		t.add(RoleSystem, "✅ 后台任务已完成")
		if t.rawMode {
			t.render()
		}
		return
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
