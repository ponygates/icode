package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// AskUserFunc is an interactive "ask the user a multiple-choice question"
// callback (Claude Code AskUserQuestion parity). It blocks until the user
// picks an option; returns the 0-based index (negative or error when the user
// cancels). Environments without an interactive surface (e.g. headless) leave
// it nil and the tool degrades gracefully instead of hanging.
type AskUserFunc func(question string, options []string) (int, error)

type askUserKey struct{}

// WithAskUser injects the interactive asker into a tool-execution context.
func WithAskUser(ctx context.Context, fn AskUserFunc) context.Context {
	return context.WithValue(ctx, askUserKey{}, fn)
}

// AskUserFromContext returns the injected asker, or nil when the environment
// cannot ask the user interactively.
func AskUserFromContext(ctx context.Context) AskUserFunc {
	v, _ := ctx.Value(askUserKey{}).(AskUserFunc)
	return v
}

// AskUserTool is a multiple-choice interactive question tool: the model asks
// the user up to 9 options and receives the chosen one (Claude Code
// AskUserQuestion parity). Used when the model needs a decision that only the
// human can make (e.g. which directory, which approach, go/no-go).
type AskUserTool struct{}

type askUserInput struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

func (t *AskUserTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "ask_user_question",
		Description: "Ask the user a multiple-choice question (up to 9 options) and wait for their selection. Use when you need a decision only the user can make — which directory, which approach, whether to proceed. Returns the chosen option text. Degrades with an error in non-interactive environments.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question to ask the user (required)",
				},
				"options": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Up to 9 answer options (required, at least 1)",
				},
			},
			"required": []string{"question", "options"},
		},
	}
}

func (t *AskUserTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in askUserInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("invalid args: %v", err)}, nil
	}
	q := strings.TrimSpace(in.Question)
	opts := cleanAskOptions(in.Options)
	if q == "" {
		return &types.ToolResult{Success: false, Error: "question is required"}, nil
	}
	if len(opts) == 0 {
		return &types.ToolResult{Success: false, Error: "at least one option is required"}, nil
	}
	if len(opts) > 9 {
		opts = opts[:9]
	}

	fn := AskUserFromContext(ctx)
	if fn == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "当前环境不支持交互提问（ask_user_question 需要交互式终端）。请改用其他方式让用户决策。",
		}, nil
	}
	idx, err := fn(q, opts)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "提问已取消或超时"}, nil
	}
	if idx < 0 || idx >= len(opts) {
		return &types.ToolResult{Success: false, Error: "用户取消了提问"}, nil
	}
	return &types.ToolResult{
		Success: true,
		Content: fmt.Sprintf("用户选择了选项 %d：%s", idx+1, opts[idx]),
	}, nil
}

// cleanAskOptions trims whitespace and drops empties.
func cleanAskOptions(opts []string) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		o = strings.TrimSpace(o)
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}
