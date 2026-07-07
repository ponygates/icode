package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/provider"
)

type Agent struct {
	provider provider.Provider
	tools    map[string]Tool
	history  []provider.Message
	prefix   []provider.Message
	config   Config
}

type Config struct {
	SystemPrompt string
	MaxTurns     int
	MaxTokens    int
}

type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, args string) (string, error)
}

func New(p provider.Provider, tools []Tool, cfg Config) *Agent {
	toolMap := make(map[string]Tool)
	for _, t := range tools {
		toolMap[t.Name()] = t
	}

	prefix := []provider.Message{
		{Role: "system", Content: cfg.SystemPrompt},
	}

	return &Agent{
		provider: p,
		tools:    toolMap,
		prefix:   prefix,
		config:   cfg,
	}
}

func (a *Agent) Run(ctx context.Context, input string) error {
	a.history = append(a.history, provider.Message{Role: "user", Content: input})

	turnCount := 0
	for turnCount < a.config.MaxTurns {
		turnCount++

		messages := append([]provider.Message{}, a.prefix...)
		messages = append(messages, a.history...)

		resp, err := a.provider.Complete(ctx, provider.CompletionRequest{
			Messages:  messages,
			Stream:    false,
			MaxTokens: a.config.MaxTokens,
		})
		if err != nil {
			return fmt.Errorf("completion error: %w", err)
		}

		a.history = append(a.history, provider.Message{Role: "assistant", Content: resp.Content})

		if len(resp.ToolCalls) == 0 {
			return nil
		}

		for _, tc := range resp.ToolCalls {
			tool, ok := a.tools[tc.Function.Name]
			if !ok {
				result := fmt.Sprintf("Error: unknown tool '%s'", tc.Function.Name)
				a.history = append(a.history, provider.Message{Role: "tool", Content: result})
				continue
			}

			result, err := tool.Execute(ctx, tc.Function.Arguments)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}
			a.history = append(a.history, provider.Message{Role: "tool", Content: result})
		}
	}

	return fmt.Errorf("max turns reached (%d)", a.config.MaxTurns)
}

func (a *Agent) History() []provider.Message {
	return a.history
}

func (a *Agent) ToolDefs() []provider.ToolDef {
	var defs []provider.ToolDef
	for _, t := range a.tools {
		defs = append(defs, provider.ToolDef{
			Type: "function",
			Function: provider.ToolFunc{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"input": map[string]any{
							"type":        "string",
							"description": "The input for this tool",
						},
					},
					"required": []string{"input"},
				},
			},
		})
	}
	return defs
}

func (a *Agent) SystemPrompt() string {
	var b strings.Builder
	b.WriteString(a.config.SystemPrompt)
	b.WriteString("\n\nAvailable tools:\n")
	for _, t := range a.tools {
		b.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), t.Description()))
	}
	return b.String()
}
