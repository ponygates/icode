package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ponygates/icode/internal/provider"
)

type Callback func(chunk StreamEvent)

type StreamEvent struct {
	Type    string
	Content string
	Done    bool
	Error   error
}

type Agent struct {
	provider   provider.Provider
	tools      map[string]Tool
	history    []provider.Message
	config     Config
	mu         sync.Mutex
	callbacks  []Callback
}

type Config struct {
	SystemPrompt string
	MaxTurns     int
	MaxTokens    int
	Model        string
}

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, argsJSON string) (string, error)
}

func New(p provider.Provider, tools []Tool, cfg Config) *Agent {
	toolMap := make(map[string]Tool)
	for _, t := range tools {
		toolMap[t.Name()] = t
	}
	return &Agent{
		provider:  p,
		tools:     toolMap,
		config:    cfg,
	}
}

func (a *Agent) OnEvent(cb Callback) {
	a.callbacks = append(a.callbacks, cb)
}

func (a *Agent) emit(event StreamEvent) {
	for _, cb := range a.callbacks {
		cb(event)
	}
}

func (a *Agent) Run(ctx context.Context, input string) error {
	a.mu.Lock()
	a.history = append(a.history, provider.Message{Role: "user", Content: input})
	a.mu.Unlock()

	a.emit(StreamEvent{Type: "user", Content: input})

	prefix := []provider.Message{
		{Role: "system", Content: a.SystemPrompt()},
	}

	for turn := 0; turn < a.config.MaxTurns; turn++ {
		a.emit(StreamEvent{Type: "thinking"})

		messages := make([]provider.Message, 0, len(prefix)+len(a.history))
		messages = append(messages, prefix...)
		messages = append(messages, a.history...)

		resp, err := a.provider.Complete(ctx, provider.CompletionRequest{
			Model:     a.config.Model,
			Messages:  messages,
			Tools:     a.ToolDefs(),
			Stream:    false,
			MaxTokens: a.config.MaxTokens,
		})
		if err != nil {
			return fmt.Errorf("completion error: %w", err)
		}

		a.mu.Lock()
		a.history = append(a.history, provider.Message{Role: "assistant", Content: resp.Content})
		a.mu.Unlock()

		if resp.Content != "" {
			a.emit(StreamEvent{Type: "text", Content: resp.Content})
		}

		if len(resp.ToolCalls) == 0 {
			a.emit(StreamEvent{Type: "done", Done: true})
			return nil
		}

		for _, tc := range resp.ToolCalls {
			a.emit(StreamEvent{Type: "tool_call", Content: fmt.Sprintf("%s(%s)", tc.Function.Name, tc.Function.Arguments)})

			tool, ok := a.tools[tc.Function.Name]
			if !ok {
				errMsg := fmt.Sprintf("Error: unknown tool '%s'", tc.Function.Name)
				a.mu.Lock()
				a.history = append(a.history, provider.Message{Role: "tool", Content: errMsg})
				a.mu.Unlock()
				a.emit(StreamEvent{Type: "tool_result", Content: errMsg})
				continue
			}

			result, err := tool.Execute(ctx, tc.Function.Arguments)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}
			a.mu.Lock()
			a.history = append(a.history, provider.Message{Role: "tool", Content: result})
			a.mu.Unlock()
			a.emit(StreamEvent{Type: "tool_result", Content: result})
		}
	}

	return fmt.Errorf("max turns reached (%d)", a.config.MaxTurns)
}

func (a *Agent) History() []provider.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.history
}

func (a *Agent) ClearHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = nil
}

func (a *Agent) ToolDefs() []provider.ToolDef {
	var defs []provider.ToolDef
	for _, t := range a.tools {
		defs = append(defs, provider.ToolDef{
			Type: "function",
			Function: provider.ToolFunc{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return defs
}

func (a *Agent) SystemPrompt() string {
	var b strings.Builder
	b.WriteString(a.config.SystemPrompt)
	b.WriteString("\n\n## Available Tools\n\n")
	for _, t := range a.tools {
		b.WriteString(fmt.Sprintf("- `%s`: %s\n", t.Name(), t.Description()))
	}
	return b.String()
}
