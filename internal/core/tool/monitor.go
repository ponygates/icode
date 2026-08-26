package tool

// Monitor — Claude Code parity. Streams a background task's output into the
// conversation incrementally so the model can tail logs / watch a build and
// react while it runs, instead of one-shot task_output polls.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// MonitorTool watches a background task and returns new output as it
// arrives, stopping on quiet period or deadline.
type MonitorTool struct{}

func (t *MonitorTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "monitor",
		Description: "Watch a background task (bg-N shell command or agt-N sub-agent) and stream its NEW output into this conversation as it appears. Returns when the task finishes, output goes quiet, or the deadline hits — whatever comes first. Use after run_in_background to follow a build/log live and react early.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id":       map[string]any{"type": "string", "description": "Task id from bash(run_in_background) or task(background): bg-1, agt-2 …"},
				"max_seconds":   map[string]any{"type": "integer", "description": "Give up after this long even if still running. Default 15, cap 60."},
				"quiet_seconds": map[string]any{"type": "number", "description": "Stop early after this much silence in the output. Default 3."},
			},
			"required": []string{"task_id"},
		},
	}
}

func (t *MonitorTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	id := parseStrArg(args, "task_id", "")
	if id == "" {
		return &types.ToolResult{Success: false, Error: "monitor requires 'task_id'"}, nil
	}
	maxSecs := parseIntArg(args, "max_seconds", 15)
	if maxSecs <= 0 {
		maxSecs = 15
	}
	if maxSecs > 60 {
		maxSecs = 60
	}
	quietMs := parseIntArg(args, "quiet_seconds", 3) * 1000
	if quietMs <= 0 {
		quietMs = 3000
	}

	var (
		read    func(pos int) (string, int)
		status  func() (done bool, errMsg string)
		isAgent bool
	)
	if len(id) > 4 && id[:4] == "agt-" {
		task := agentBGTasks.get(id)
		if task == nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("no such task: %s", id)}, nil
		}
		isAgent = true
		read = func(int) (string, int) { return "", 0 } // agent tasks surface only their final result
		status = func() (bool, string) {
			_, _, done, errMsg, _ := task.snapshot()
			return done, errMsg
		}
	} else {
		task := bgTasks.Get(id)
		if task == nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("no such task: %s", id)}, nil
		}
		pos := 0
		read = func(int) (string, int) {
			chunk, np := task.newSince(pos)
			pos = np
			return chunk, pos
		}
		status = func() (bool, string) {
			_, done, errMsg, _ := task.snapshot()
			return done, errMsg
		}
	}

	var collected strings.Builder
	lastActivity := time.Now()
	deadline := time.After(time.Duration(maxSecs) * time.Second)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return monitorResult(id, isAgent, collected.String(), "interrupted"), nil
		case <-deadline:
			return monitorResult(id, isAgent, collected.String(), fmt.Sprintf("still running after %ds", maxSecs)), nil
		case <-tick.C:
		}
		chunk, _ := read(0)
		if chunk != "" {
			collected.WriteString(chunk)
			lastActivity = time.Now()
		}
		if done, errMsg := status(); done {
			extra := ""
			// One final drain so nothing written between ticks is lost.
			if !isAgent {
				final, _ := read(0)
				extra = final
			}
			note := "finished"
			if errMsg != "" {
				note = "failed: " + errMsg
			}
			return monitorResult(id, isAgent, collected.String()+extra, note), nil
		}
		if time.Since(lastActivity) > time.Duration(quietMs) {
			return monitorResult(id, isAgent, collected.String(), fmt.Sprintf("quiet for %ds (task still running)", quietMs/1000)), nil
		}
	}
}

func monitorResult(id string, isAgent bool, output, note string) *types.ToolResult {
	head := fmt.Sprintf("[monitor %s] %s\n---\n", id, note)
	body := strings.TrimRight(output, "\n")
	if body == "" && !isAgent {
		body = "(no new output)"
	}
	if isAgent && body == "" {
		body = "(sub-agents report only their final result; poll with task_output)"
	}
	return &types.ToolResult{Success: true, Content: head + body}
}
