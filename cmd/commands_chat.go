package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/core/voice"
	"github.com/ponygates/icode/internal/tui"
	"github.com/ponygates/icode/internal/types"
)

// chatCallback bridges the TUI to the iCode backend.
type chatCallback struct {
	app       *app.App
	tui       *tui.TUI
	sessionID string
	lastTool  string
	voiceRec  *voice.Recorder // active /voice recorder (nil when idle)
}

func (c *chatCallback) OnSend(text string, attachments []types.Attachment) {
	if c.app == nil || c.app.Engine == nil {
		c.tui.AddMessage(tui.RoleSystem, "[Engine not available. Configure an API key with 'icode auth set']")
		return
	}

	model := c.tui.CurrentModel()
	provider := c.tui.CurrentProvider()

	// Create session on first message
	if c.sessionID == "" {
		sess := &types.Session{
			ID:           fmt.Sprintf("%x", time.Now().UnixNano()),
			ModelID:      model,
			ProviderName: provider,
			Title:        text,
		}
		if len(text) > 40 {
			sess.Title = text[:40] + "..."
		}
		if err := c.app.SessStore.Create(sess); err == nil {
			c.sessionID = sess.ID
		} else {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("[Session error: %v]", err))
			return
		}
	} else if c.app.SessStore != nil {
		// Keep the session's model/provider in sync with the TUI.
		if sess, err := c.app.SessStore.Get(c.sessionID); err == nil {
			if sess.ModelID != model || sess.ProviderName != provider {
				sess.ModelID = model
				sess.ProviderName = provider
				_ = c.app.SessStore.Update(sess)
			}
		}
	}

	c.lastTool = ""
	ctx := context.Background()
	eventCh, err := c.app.Engine.Send(ctx, c.sessionID, text, attachments)
	if err != nil {
		c.tui.AddMessage(tui.RoleError, fmt.Sprintf("Engine error: %v", err))
		return
	}

	// Read streaming events and push to TUI
	for event := range eventCh {
		switch event.Type {
		case types.EventText:
			if c.lastTool != "" {
				// This text is the tool-result wrapper emitted by the engine.
				result := strings.TrimSpace(strings.TrimPrefix(
					strings.TrimPrefix(event.Content, "\n"),
					"[Tool: "+c.lastTool+"]"))
				if result != "" {
					c.tui.AppendToolResult(result)
				}
				c.lastTool = ""
			} else {
				c.tui.AppendStream(event.Content)
			}
		case types.EventThinking:
			// Merge consecutive reasoning deltas into one thinking block —
			// previously every delta became its own box and a long reasoning
			// phase piled up dozens of them.
			c.tui.AppendThinkingDelta(event.Content)
		case types.EventSystem:
			c.tui.AddMessage(tui.RoleSystem, strings.TrimSpace(event.Content))
		case types.EventPlanProposal:
			// A plan-mode turn finished — arm the confirmation prompt so the
			// next Enter executes the plan (Esc cancels).
			c.tui.SetPlanPending(true)
		case types.EventToolUse:
			c.lastTool = event.ToolCall.Name
			// Strip empty/no-op parameter objects so the conversation
			// shows "⏺ git_status" instead of "⏺ git_status {}".
			args := event.ToolCall.Arguments
			if strings.TrimSpace(args) == "{}" {
				args = ""
			}
			c.tui.AddToolMessage(event.ToolCall.Name, args, "")
		case types.EventToolProgress:
			// Live tool output (bash streaming): append to the active tool card.
			c.tui.AppendToolProgress(event.Content)
		case types.EventDone:
			u := event.Meta.Usage
			var cacheRate float64
			if total := u.PromptTokens + u.CompletionTokens; total > 0 {
				cacheRate = float64(u.CacheHitTokens) / float64(total)
			}
			// Resolve the model to compute cost + context-window usage for the
			// Claude Code-style status bar.
			costStr := ""
			ctxWin := 0
			if _, mi, rerr := c.app.Reg.ResolveModel(model); rerr == nil {
				costStr = formatCost(estimateCost(u, mi), primaryCurrency(mi))
				ctxWin = mi.ContextWindow
			}
			c.tui.SetStatus(u.PromptTokens, u.CompletionTokens, cacheRate, costStr)
			c.tui.SetContext(u.PromptTokens, ctxWin)
			c.tui.EndStream()
			return
		case types.EventError:
			c.tui.AddMessage(tui.RoleError, event.Content)
			c.tui.EndStream()
			return
		}
	}
	// The event channel closed without an explicit EventDone/EventError
	// (e.g. the user hit Esc and the engine stopped the stream). Reset the
	// streaming state unconditionally — EndStream is idempotent — so the UI
	// can never get stuck in "generating…" with a dead Esc key.
	c.tui.EndStream()
}

// OnListSessions returns a formatted list of saved sessions.
func (c *chatCallback) OnPlanConfirm() {
	if c.app != nil && c.app.Gate != nil {
		c.app.Gate.SetMode(permission.ModeAuto)
	}
	c.tui.SetPlanPending(false)
	c.OnSend("计划已确认。请按上述计划立即开始执行，不要再重复或重新规划，直接动手。", nil)
}

func (c *chatCallback) OnListSessions() string {
	if c.app == nil || c.app.SessStore == nil {
		return "No session store available."
	}
	sessions, err := sessionum.ListNonDeleted(c.app.SessStore, 20)
	if err != nil || len(sessions) == 0 {
		return "No saved sessions yet. Start chatting to create one."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Saved sessions (%d):\n", len(sessions)))
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		sb.WriteString(fmt.Sprintf("  %s  %s  [%s]\n", s.ID, title, s.ModelID))
	}
	return sb.String()
}

// OnListSessionsStructured implements tui.Callback — returns lightweight
// session descriptors for the interactive /resume picker.
func (c *chatCallback) OnListSessionsStructured(limit int) []tui.SessionInfo {
	if c.app == nil || c.app.SessStore == nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	sessions, err := sessionum.ListNonDeleted(c.app.SessStore, limit)
	if err != nil {
		return nil
	}
	out := make([]tui.SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		out = append(out, tui.SessionInfo{
			ID:      s.ID,
			Title:   title,
			Model:   s.ModelID,
			Updated: s.UpdatedAt.Format("01-02 15:04"),
		})
	}
	return out
}

// OnResume loads a past session's messages into the TUI.
func (c *chatCallback) OnResume(id string) string {
	if c.app == nil || c.app.SessStore == nil {
		return "No session store available."
	}
	sess, err := c.app.SessStore.Get(id)
	if err != nil {
		return fmt.Sprintf("Session not found: %s", id)
	}
	if sessionum.IsDeleted(sess) {
		return fmt.Sprintf("该会话已被软删除，先用 /restore %s 恢复。", id)
	}
	c.sessionID = sess.ID

	var msgs []tui.Message
	for _, m := range sess.Messages {
		tm := tui.Message{Role: tui.Role(m.Role), Content: m.Content}
		if m.Role == "tool" && len(m.ToolCalls) > 0 {
			tm.Tool = m.ToolCalls[0].Name
			tm.ToolArgs = m.ToolCalls[0].Arguments
		}
		msgs = append(msgs, tm)
	}
	c.tui.LoadSession(msgs)
	return fmt.Sprintf("Resumed session %s — %d messages loaded", id, len(msgs))
}

// OnCompactSummarize asks the engine to produce a model-generated semantic
// summary of the active session's older turns (/compact parity). The summary
// is cached into the session metadata (marked semantic) so a later
// /resume --compact or /resume --lite reuses it without another model call.
// Returns "" on any failure — the TUI then falls back to the free local trim.
func (c *chatCallback) OnCompactSummarize(instruction string) string {
	if c.app == nil || c.app.Engine == nil || c.sessionID == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sum, err := c.app.Engine.SummarizeConversation(ctx, c.sessionID, instruction)
	if err != nil || sum == "" {
		return ""
	}
	if sess, err := c.app.SessStore.Get(c.sessionID); err == nil {
		_ = sessionum.Save(c.app.SessStore, sess, sum)
		_ = sessionum.MarkSemantic(c.app.SessStore, sess)
	}
	return sum
}
