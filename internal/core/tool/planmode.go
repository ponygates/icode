package tool

import (
	"context"

	"github.com/ponygates/icode/internal/types"
)

// EnterPlanModeTool lets the MODEL ask for a read-only planning turn instead
// of waiting for the user to flip the mode switch. The user-facing behaviour is
// identical to starting in plan mode: the turn may read and search, but every
// mutating tool asks (or is denied), and the finished reply is surfaced as a
// plan the user accepts or discards.
//
// The actual mode switch lives in the conversation engine (it owns the gate),
// which intercepts this tool by name before permission handling. Execute here
// is a guard for paths that bypass the engine (e.g. a sub-agent), where
// silently changing the parent's mode would be wrong.
type EnterPlanModeTool struct{}

func (t *EnterPlanModeTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "enter_plan_mode",
		Description: "Switch this conversation into read-only PLAN mode when a request is large, " +
			"ambiguous, or would touch several files. Investigate with read-only tools, then reply " +
			"with a concrete plan (goal, steps, files, risks, verification). The user accepts or " +
			"discards it; no files are changed until they accept. Use this instead of starting to edit " +
			"when you are not yet sure of the approach.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *EnterPlanModeTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	return &types.ToolResult{
		Success: false,
		Error:   "enter_plan_mode 只能由主对话引擎处理（子代理不能切换父会话的模式）",
	}, nil
}
