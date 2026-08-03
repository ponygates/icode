// Package sessionum archives a lightweight, zero-token summary of a session
// so later turns (and the session list) can see what was discussed without
// re-reading the full transcript. Summaries are generated locally from stats
// and user questions — no model call, keeping iCode's token-saving promise.
package sessionum

import (
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// MetadataKey is where the summary lives on a session.
const MetadataKey = "summary"

// Generate produces a local summary of a session: message count, model,
// provider, mode, token usage and a list of user questions. It never calls
// the model, so it is free to run on any exit path.
func Generate(sess *types.Session, model, provider, mode string) string {
	if sess == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 对话总结\n\n")
	msgCount := 0
	for _, m := range sess.Messages {
		if m.Role == types.RoleUser || m.Role == types.RoleAssistant {
			msgCount++
		}
	}
	if msgCount == 0 {
		return ""
	}
	if model == "" {
		model = sess.ModelID
	}
	if provider == "" {
		provider = sess.ProviderName
	}
	sb.WriteString(fmt.Sprintf("共 %d 条消息，模型: %s，提供商: %s，模式: %s\n\n",
		msgCount, short(model), short(provider), short(mode)))

	if sess.TotalTokens.PromptTokens > 0 || sess.TotalTokens.CompletionTokens > 0 {
		sb.WriteString(fmt.Sprintf("Token: %s 输入 + %s 输出 = %s 总计\n\n",
			formatInt(sess.TotalTokens.PromptTokens), formatInt(sess.TotalTokens.CompletionTokens),
			formatInt(sess.TotalTokens.PromptTokens+sess.TotalTokens.CompletionTokens)))
	}

	sb.WriteString("### 用户提问\n\n")
	for _, m := range sess.Messages {
		if m.Role == types.RoleUser && strings.TrimSpace(m.Content) != "" {
			sb.WriteString(fmt.Sprintf("- %s\n", trunc(m.Content, 120)))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// Save stores the summary in the session's Metadata and persists it via the
// store. Failures are swallowed — archiving is best-effort and must never
// block an exit path.
func Save(store types.SessionStore, sess *types.Session, summary string) error {
	if store == nil || sess == nil || strings.TrimSpace(summary) == "" {
		return nil
	}
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	if s, _ := sess.Metadata[MetadataKey].(string); s == summary {
		return nil
	}
	sess.Metadata[MetadataKey] = summary
	return store.Update(sess)
}

// Get returns the archived summary for a session, or "" when absent.
func Get(sess *types.Session) string {
	if sess == nil || sess.Metadata == nil {
		return ""
	}
	s, _ := sess.Metadata[MetadataKey].(string)
	return s
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func short(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func formatInt(v int) string {
	if v <= 0 {
		return "0"
	}
	return fmt.Sprintf("%d", v)
}
