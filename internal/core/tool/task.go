// TaskTool — delegates work to a sub-agent running in its own Optimizer.
//
// This is the single most impactful token-saving feature: instead of the main
// agent reading/searching/analysing in-line (polluting the main context with
// thousands of intermediate tool-result tokens), it fires a sub-agent whose
// entire conversation lives in an independent Optimizer. Only the sub-agent's
// final answer (typically 200-800 tokens) comes back as a tool_result.
//
// Usage (models see):
//
//	{
//	  "name": "explore",
//	  "prompt": "Find all places in internal/core/ that handle permission checking"
//	}
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// SubAgentRunner is the interface the Task tool uses to invoke a sub-agent.
// The Engine implements this so the tool does not need to know about
// providers, optimizers, or tool registries.
type SubAgentRunner interface {
	RunSubAgent(ctx context.Context, name, prompt string) (result string, tokensUsed int, err error)
}

// TaskTool delegates to a sub-agent with its own Optimizer context.
type TaskTool struct {
	runner SubAgentRunner
}

// NewTaskTool creates a task tool wired to the engine's sub-agent runner.
func NewTaskTool(runner SubAgentRunner) *TaskTool {
	return &TaskTool{runner: runner}
}

func (t *TaskTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "task",
		Description: "Delegate a self-contained subtask to a named sub-agent. " +
			"Available agents: explore (read-only code search), plan (architecture/design), " +
			"general (catch-all). The sub-agent runs in its own context — only its final " +
			"answer is returned, saving thousands of tokens. Use when: searching for " +
			"code patterns, analysing architecture, or doing any work the main agent " +
			"could do but that would pollute the main conversation with intermediate output.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Sub-agent name: explore (read-only search), plan (architecture), or general (catch-all).",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The task description for the sub-agent. Be specific about what to find, look for, or analyse.",
				},
			},
			"required": []string{"name", "prompt"},
		},
	}
}

func (t *TaskTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct {
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   "invalid arguments for task: " + err.Error(),
		}, nil
	}

	in.Name = strings.TrimSpace(in.Name)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Name == "" || in.Prompt == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "task requires both 'name' and 'prompt'",
		}, nil
	}

	if t.runner == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "task runner not available (no Engine wired)",
		}, nil
	}

	// Delegate to the sub-agent runner.
	result, totalTokens, err := t.runner.RunSubAgent(ctx, in.Name, in.Prompt)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Content: result,
			Error:   fmt.Sprintf("sub-agent %q failed: %v", in.Name, err),
		}, nil
	}

	// Show cost info in the tool result so the main agent can report savings.
	summary := fmt.Sprintf("【子任务摘要 (agent: %s, token: %d)】\n%s",
		in.Name, totalTokens, result)

	return &types.ToolResult{Success: true, Content: summary}, nil
}
