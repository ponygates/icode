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

// ForkingSubAgentRunner is optionally implemented by hosts that can replay
// the parent conversation prefix into a sub-agent (fork mode). When the task
// is called with fork=true and the runner supports it, the sub-agent starts
// from the parent's cached context instead of a cold one.
type ForkingSubAgentRunner interface {
	RunForkedSubAgent(ctx context.Context, name, prompt string) (result string, tokensUsed int, err error)
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
			"could do but that would pollute the main conversation with intermediate output. " +
			"Set background=true to run it detached and keep working — you get a task id " +
			"(agt-N) immediately; poll task_output later for the result.",
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
				"background": map[string]any{
					"type":        "boolean",
					"description": "Run detached in the background. Returns a task id at once; fetch results via task_output. Default false.",
				},
				"fork": map[string]any{
					"type":        "boolean",
					"description": "Replay the parent conversation into this sub-agent (prompt-cache friendly) so it knows the full context. Use when the subtask needs conversation history.",
				},
			},
			"required": []string{"name", "prompt"},
		},
	}
}

func (t *TaskTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct {
		Name       string `json:"name"`
		Prompt     string `json:"prompt"`
		Background bool   `json:"background"`
		Fork       bool   `json:"fork"`
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

	// run resolves fork mode: when requested and the host supports it, the
	// sub-agent replays the parent conversation prefix (cache-friendly).
	run := func(ctx context.Context, name, prompt string) (string, int, error) {
		if in.Fork {
			if fr, ok := t.runner.(ForkingSubAgentRunner); ok {
				return fr.RunForkedSubAgent(ctx, name, prompt)
			}
		}
		return t.runner.RunSubAgent(ctx, name, prompt)
	}

	brief := in.Prompt
	if len(brief) > 120 {
		brief = brief[:120] + "…"
	}

	// Background launch: hand off to the detached runner and return the
	// handle immediately so the main loop keeps working.
	if in.Background {
		id, err := launchBackgroundAgent(funcAdapter(run), in.Name, in.Prompt)
		if err != nil {
			return &types.ToolResult{Success: false, Error: "background launch failed: " + err.Error()}, nil
		}
		if progress := ProgressFromContext(ctx); progress != nil {
			progress(fmt.Sprintf("🚀 子代理 %s 已后台启动（%s）\n", in.Name, id))
		}
		return &types.ToolResult{
			Success: true,
			Content: fmt.Sprintf("【后台子代理已启动】id=%s agent=%s 任务：%s\n结果尚未就绪。继续你的工作；稍后用 task_output(task_id=%q) 查询状态与输出。",
				id, in.Name, brief, id),
		}, nil
	}

	// Foreground delegation. Emit a live progress line first so the user sees
	// the sub-agent start (instead of a silent multi-second stall).
	if progress := ProgressFromContext(ctx); progress != nil {
		progress(fmt.Sprintf("🔍 子代理 %s 正在执行：%s\n", in.Name, brief))
	}
	result, totalTokens, err := run(ctx, in.Name, in.Prompt)
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
