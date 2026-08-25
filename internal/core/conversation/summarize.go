package conversation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// SummarizeConversation asks the configured model to produce a semantic
// summary of the session's older turns — the Claude Code /compact and
// /resume --compact parity. Unlike the free local stats summary produced by
// sessionum.Generate (message count + question list), this one is
// model-generated and preserves concrete decisions, file changes and open
// items, so a later turn keeps the gist without replaying every old message.
//
// Best-effort by design: on any failure (engine not ready, too few turns,
// model error, timeout) it returns "" so callers fall back to the free local
// summary instead of blocking the user. Callers that want the summary to be
// durable should cache it via sessionum.Save + sessionum.MarkSemantic.
func (e *Engine) SummarizeConversation(ctx context.Context, sessionID, instruction string) (string, error) {
	if e == nil || e.providerReg == nil || e.sessionSt == nil {
		return "", fmt.Errorf("summarize: engine not ready")
	}
	sess, err := e.sessionSt.Get(sessionID)
	if err != nil {
		return "", fmt.Errorf("summarize: get session: %w", err)
	}
	if sess == nil {
		return "", fmt.Errorf("summarize: session %q not found", sessionID)
	}
	if sess.ModelID == "" {
		return "", fmt.Errorf("summarize: session %q has no model configured", sessionID)
	}

	// Count real turns (user/assistant). Tool messages are folded into the
	// transcript text below; a handful of turns is not worth a model call.
	turns := 0
	for _, m := range sess.Messages {
		if m.Role == types.RoleUser || m.Role == types.RoleAssistant {
			turns++
		}
	}
	if turns < 4 {
		return "", nil // nothing meaningful to summarize yet
	}

	// Summarize the older turns; keep the last 4 intact as recent context.
	older := sess.Messages
	if len(older) > 4 {
		older = older[:len(older)-4]
	}
	transcript := renderTranscript(older, 60_000)
	if transcript == "" {
		return "", nil
	}

	provider, modelInfo, err := e.providerReg.ResolveModel(sess.ModelID)
	if err != nil {
		return "", fmt.Errorf("summarize: resolve model: %w", err)
	}

	prompt := `You are a conversation summarizer for a coding agent session. Summarize the OLDER turns of the conversation below in Chinese (keep code identifiers, file paths, commands and error messages verbatim; never invent details).

Return ONLY a structured markdown summary with these sections:

## 目标
## 已完成
## 关键决策
## 文件改动
## 未完成 / 待办
## 下一步建议

Keep it under 500 words.`
	if strings.TrimSpace(instruction) != "" {
		prompt += "\n\nUser focus for this summary: " + strings.TrimSpace(instruction)
	}
	prompt += "\n\n---\n\n" + transcript

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	ch, err := provider.ChatStream(ctx, types.ChatRequest{
		SessionID:    sessionID,
		Messages:     []types.Message{{Role: types.RoleUser, Content: prompt}},
		Model:        modelInfo.ID,
		ProviderName: modelInfo.Provider,
		MaxTokens:    1200,
		Temperature:  0.3,
		SystemPrompt: "You are a concise, faithful conversation summarizer. Output markdown only.",
	})
	if err != nil {
		return "", fmt.Errorf("summarize: chat: %w", err)
	}

	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case types.EventText:
			sb.WriteString(ev.Content)
		case types.EventError:
			return "", fmt.Errorf("summarize: %s", ev.Content)
		}
	}
	summary := strings.TrimSpace(sb.String())
	if summary == "" {
		return "", fmt.Errorf("summarize: empty model reply")
	}
	return firstN(summary, 6000), nil
}

// renderTranscript flattens session messages into labeled lines for the
// summarizer, capping the total at maxRunes with head+tail retention (the
// same spirit as the budget enforcer: keep the start and the end, omit the
// bloated middle).
func renderTranscript(msgs []types.Message, maxRunes int) string {
	var sb strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case types.RoleUser:
			if strings.TrimSpace(m.Content) != "" {
				fmt.Fprintf(&sb, "[user] %s\n", m.Content)
			}
		case types.RoleAssistant:
			text := m.Content
			if text == "" && len(m.ToolCalls) > 0 {
				text = "(tool calls: " + toolCallNames(m.ToolCalls) + ")"
			}
			if strings.TrimSpace(text) != "" {
				fmt.Fprintf(&sb, "[assistant] %s\n", text)
			}
		case types.RoleTool:
			text := m.Content
			if text == "" && len(m.ToolCalls) > 0 {
				text = "(result of " + m.ToolCalls[0].Name + ")"
			}
			if strings.TrimSpace(text) != "" {
				fmt.Fprintf(&sb, "[tool] %s\n", text)
			}
		}
	}
	s := sb.String()
	if len([]rune(s)) <= maxRunes {
		return s
	}
	r := []rune(s)
	// Reserve room for the omission marker so the output never exceeds the
	// cap: head + tail = maxRunes - 80, marker ~50 runes.
	head := maxRunes * 6 / 10
	tail := maxRunes - head - 80
	if tail < 80 {
		head = maxRunes * 4 / 10
		tail = maxRunes - head - 80
	}
	if tail < 20 {
		return string(r[:maxRunes]) + "\n[…truncated…]"
	}
	omitted := len(r) - head - tail
	marker := fmt.Sprintf("\n[… %d runes omitted by summarizer budget …]\n", omitted)
	return string(r[:head]) + marker + string(r[len(r)-tail:])
}

// toolCallNames joins tool names of a message's tool calls for the transcript.
func toolCallNames(calls []types.ToolCall) string {
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}
