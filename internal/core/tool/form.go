package tool

import (
	"context"
	"encoding/json"

	"github.com/ponygates/icode/internal/types"
)

// AskUserFormFunc is the interactive multi-question form asker (opencode
// AskQuestion wizard parity): the engine calls it with a batch of questions
// and waits for the user's answers. Injected by the TUI; nil in headless /
// simpleui / desktop HTTP degrades the tool to an error instead of hanging.
type AskUserFormFunc func(questions []FormQuestion) ([]FormAnswer, error)

// FormQuestion is one item of an ask_user_form call.
type FormQuestion struct {
	Question    string   `json:"question"`
	Options     []string `json:"options,omitempty"` // empty = free text
	MultiSelect bool     `json:"multi_select,omitempty"`
	Text        bool     `json:"text,omitempty"` // free-text answer (no options)
}

// FormAnswer is the user's answer to one FormQuestion.
type FormAnswer struct {
	Index   int      `json:"index,omitempty"`  // single-select choice
	Indices []int    `json:"indices,omitempty"` // multi-select choices
	Text    string   `json:"text,omitempty"`   // free-text answer
}

// askUserFormKey carries the form asker through the context.
type askUserFormKey struct{}

// WithAskUserForm stores the form asker in ctx for the duration of a turn.
func WithAskUserForm(ctx context.Context, fn AskUserFormFunc) context.Context {
	return context.WithValue(ctx, askUserFormKey{}, fn)
}

// AskUserFormFromContext returns the form asker, or nil when not injected.
func AskUserFormFromContext(ctx context.Context) AskUserFormFunc {
	fn, _ := ctx.Value(askUserFormKey{}).(AskUserFormFunc)
	return fn
}

// AskUserFormTool is the multi-question wizard tool (opencode AskQuestion
// parity): one call asks up to 8 questions at once; each question is
// single-select, multi-select, or free text.
type AskUserFormTool struct{}

func (t *AskUserFormTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "ask_user_form",
		Description: "Ask the user up to 8 questions at once (wizard, opencode AskQuestion parity). " +
			"Each item: question + optional options (with multi_select for multiple picks) or text for a free-form answer. " +
			"Returns one answer per question. Use when you need several decisions/inputs from the user in a single interaction. " +
			"Degrades with an error in non-interactive environments.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":  "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question":     map[string]any{"type": "string"},
							"options":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							"multi_select": map[string]any{"type": "boolean"},
							"text":         map[string]any{"type": "boolean"},
						},
						"required": []string{"question"},
					},
					"description": "1-8 questions to ask (required)",
				},
			},
			"required": []string{"questions"},
		},
	}
}

func (t *AskUserFormTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var req struct {
		Questions []FormQuestion `json:"questions"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
		return &types.ToolResult{Success: false, Error: "ask_user_form: 参数解析失败: " + err.Error()}, nil
	}
	if len(req.Questions) == 0 {
		return &types.ToolResult{Success: false, Error: "ask_user_form: questions 不能为空"}, nil
	}
	if len(req.Questions) > 8 {
		req.Questions = req.Questions[:8]
	}
	asker := AskUserFormFromContext(ctx)
	if asker == nil {
		return &types.ToolResult{Success: false, Error: "ask_user_form: 当前环境不支持交互式提问（非交互模式）"}, nil
	}
	answers, err := asker(req.Questions)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "ask_user_form: " + err.Error()}, nil
	}
	// Serialize answers back to the model (opencode returns structured JSON).
	out := make([]map[string]any, 0, len(answers))
	for _, a := range answers {
		switch {
		case a.Text != "":
			out = append(out, map[string]any{"text": a.Text})
		case len(a.Indices) > 0:
			out = append(out, map[string]any{"indices": a.Indices})
		default:
			out = append(out, map[string]any{"index": a.Index})
		}
	}
	data, _ := json.Marshal(out)
	return &types.ToolResult{Success: true, Content: string(data)}, nil
}
