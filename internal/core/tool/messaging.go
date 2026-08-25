package tool

// Cross-session agent messaging — Claude Code SendMessage/ListAgents parity.
// Sessions discover peers with list_agents, message them with send_message,
// and read what others sent via inbox. Messages persist in SQLite so a peer
// can be offline (crashed/closed) when it is addressed.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// MessageStore is the persistence surface the messaging tools need.
type MessageStore interface {
	SendAgentMessage(fromID, toID, body string) error
	AgentInbox(sessionID string, limit int, unreadOnly bool) ([]types.AgentMessage, error)
	MarkAgentMessagesRead(sessionID string) error
	ListSessions(limit, offset int) ([]types.Session, error)
}

// SendMessageTool delivers a note to another live session.
type SendMessageTool struct{ store MessageStore }

func NewSendMessageTool(store MessageStore) *SendMessageTool { return &SendMessageTool{store: store} }

func (t *SendMessageTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "send_message",
		Description: "Send a short message to another iCode session (peer discovery: use list_agents). The peer sees it in its next turn or inbox poll.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":   map[string]any{"type": "string", "description": "Recipient session id from list_agents."},
				"body": map[string]any{"type": "string", "description": "Message body. Keep it concise — it lands verbatim in the peer's context."},
			},
			"required": []string{"to", "body"},
		},
	}
}

func (t *SendMessageTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct {
		To   string `json:"to"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{Success: false, Error: "invalid arguments: " + err.Error()}, nil
	}
	in.To = strings.TrimSpace(in.To)
	in.Body = strings.TrimSpace(in.Body)
	if in.To == "" || in.Body == "" {
		return &types.ToolResult{Success: false, Error: "send_message requires 'to' and 'body'"}, nil
	}
	if t.store == nil {
		return &types.ToolResult{Success: false, Error: "message store unavailable"}, nil
	}
	from := SessionIDFromContext(ctx)
	if from == "" {
		return &types.ToolResult{Success: false, Error: "current session unknown"}, nil
	}
	if from == in.To {
		return &types.ToolResult{Success: false, Error: "cannot message your own session"}, nil
	}
	if err := t.store.SendAgentMessage(from, in.To, in.Body); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	return &types.ToolResult{Success: true, Content: fmt.Sprintf("已送达 %s（%d 字符）", in.To, len(in.Body))}, nil
}

// InboxTool reads the current session's messages.
type InboxTool struct{ store MessageStore }

func NewInboxTool(store MessageStore) *InboxTool { return &InboxTool{store: store} }

func (t *InboxTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "inbox",
		Description: "Read messages other sessions sent to this one. unread=true returns only new ones (and marks them read).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"unread": map[string]any{"type": "boolean", "description": "Only unread messages. Default false (recent history)."},
				"limit":  map[string]any{"type": "integer", "description": "Max messages to return. Default 20."},
			},
		},
	}
}

func (t *InboxTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	unread := parseBoolArgWithDefault(args, "unread", false)
	limit := parseIntArg(args, "limit", 20)
	if t.store == nil {
		return &types.ToolResult{Success: false, Error: "message store unavailable"}, nil
	}
	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		return &types.ToolResult{Success: false, Error: "current session unknown"}, nil
	}
	msgs, err := t.store.AgentInbox(sessionID, limit, unread)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	if len(msgs) == 0 {
		return &types.ToolResult{Success: true, Content: "收件箱为空。"}, nil
	}
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s] %s → 本会话:\n%s\n\n", m.CreatedAt.Format("15:04:05"), m.FromID, m.Body)
	}
	if unread {
		_ = t.store.MarkAgentMessagesRead(sessionID)
	}
	return &types.ToolResult{Success: true, Content: strings.TrimRight(b.String(), "\n")}, nil
}

// ListAgentsTool enumerates sibling sessions for addressing.
type ListAgentsTool struct{ store MessageStore }

func NewListAgentsTool(store MessageStore) *ListAgentsTool { return &ListAgentsTool{store: store} }

func (t *ListAgentsTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "list_agents",
		Description: "List recent iCode sessions you can message (id + title). Use send_message with one of these ids.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (t *ListAgentsTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	if t.store == nil {
		return &types.ToolResult{Success: false, Error: "message store unavailable"}, nil
	}
	sessions, err := t.store.ListSessions(20, 0)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	me := SessionIDFromContext(ctx)
	var b strings.Builder
	n := 0
	for _, s := range sessions {
		if s.ID == me {
			continue
		}
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "%s  %s\n", s.ID, truncN(title, 60))
		n++
	}
	if n == 0 {
		return &types.ToolResult{Success: true, Content: "没有其他会话。"}, nil
	}
	return &types.ToolResult{Success: true, Content: strings.TrimRight(b.String(), "\n")}, nil
}

// parseIntArg reads an integer JSON field with a fallback default.
func parseIntArg(args string, key string, def int) int {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return def
	}
	raw, ok := m[key]
	if !ok {
		return def
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil || n <= 0 {
		return def
	}
	return n
}
