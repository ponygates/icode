package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ponygates/icode/internal/provider"
	"github.com/ponygates/icode/internal/provider/optimizer"
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
	Profile      optimizer.Profile
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

	if cfg.Profile.SystemPrompt == "" {
		cfg.Profile = optimizer.ForProvider(p.Name(), cfg.Model)
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = cfg.Profile.MaxTokens
	}

	return &Agent{
		provider: p,
		tools:    toolMap,
		config:   cfg,
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
			Model:       a.config.Model,
			Messages:    messages,
			Tools:       a.ToolDefs(),
			Stream:      false,
			MaxTokens:   a.config.MaxTokens,
			Temperature: a.config.Profile.Temperature,
			TopP:        a.config.Profile.TopP,
		})
		if err != nil {
			return fmt.Errorf("completion error: %w", err)
		}

		content := resp.Content
		if a.config.Profile.StripThinkTag {
			content = stripThinkTags(content)
		}

		a.mu.Lock()
		a.history = append(a.history, provider.Message{Role: "assistant", Content: content})
		a.mu.Unlock()

		if content != "" {
			a.emit(StreamEvent{Type: "text", Content: content})
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
	if a.config.Profile.SystemPrompt != "" {
		return a.config.Profile.SystemPrompt + "\n\n## Available Tools\n\n" + a.toolList()
	}
	return a.config.SystemPrompt + "\n\n## Available Tools\n\n" + a.toolList()
}

func (a *Agent) toolList() string {
	var b strings.Builder
	for _, t := range a.tools {
		b.WriteString(fmt.Sprintf("- `%s`: %s\n", t.Name(), t.Description()))
	}
	return b.String()
}

func (a *Agent) Profile() optimizer.Profile {
	return a.config.Profile
}

func stripThinkTags(s string) string {
	tag := "```"
	for {
		start := strings.Index(s, tag)
		if start < 0 {
			break
		}
		after := s[start+len(tag):]
		end := strings.Index(after, tag)
		if end < 0 {
			break
		}
		s = s[:start] + after[end+len(tag):]
	}
	return strings.TrimSpace(s)
}
