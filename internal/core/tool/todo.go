// TodoWriteTool — session-scoped todo list backed by internal/core/todo.
//
// Matches Claude Code's TodoWrite:
//   - single argument: `todos` = the FULL current list (idempotent replace)
//   - each item has content, status, and optional activeForm
//   - the tool returns a compact summary the model can rely on to plan next
//     steps.
//
// The session ID is picked up from the context via SessionIDFromContext, so
// concurrent sessions do not step on each other.

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/core/todo"
	"github.com/ponygates/icode/internal/types"
)

// TodoWriteTool is the "todo_write" tool registered in NewRegistry.
type TodoWriteTool struct {
	store *todo.Store
}

// NewTodoWriteTool wires a new tool against a specific store. Callers who
// want the process-wide default should pass todo.Default.
func NewTodoWriteTool(store *todo.Store) *TodoWriteTool {
	if store == nil {
		store = todo.Default
	}
	return &TodoWriteTool{store: store}
}

func (t *TodoWriteTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "todo_write",
		Description: "Manage a session-scoped todo list. Pass the FULL current list on every call — items you omit are removed. Use `pending` for not-yet-started, `in_progress` for the item you are actively working on (only one at a time), and `completed` for finished work. Setting `activeForm` provides a present-continuous label shown in the UI while an item is in_progress.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type":        "array",
					"description": "The complete todo list snapshot.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "string",
								"description": "Stable identifier. Optional; auto-generated when empty.",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "Imperative-form task description.",
							},
							"status": map[string]any{
								"type":        "string",
								"enum":        []string{"pending", "in_progress", "completed"},
								"description": "Current state.",
							},
							"activeForm": map[string]any{
								"type":        "string",
								"description": "Present-continuous label used while status is in_progress (e.g. 'Refactoring auth flow').",
							},
						},
						"required": []string{"content", "status"},
					},
				},
			},
			"required": []string{"todos"},
		},
	}
}

// todoInput mirrors the TodoWrite JSON schema.
type todoInput struct {
	Todos []struct {
		ID         string `json:"id"`
		Content    string `json:"content"`
		Status     string `json:"status"`
		ActiveForm string `json:"activeForm"`
	} `json:"todos"`
}

func (t *TodoWriteTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in todoInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   "invalid arguments for todo_write: " + err.Error(),
		}, nil
	}

	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		sessionID = "default"
	}

	items := make([]todo.TodoItem, 0, len(in.Todos))
	for _, r := range in.Todos {
		if strings.TrimSpace(r.Content) == "" {
			continue
		}
		st := todo.Status(strings.ToLower(strings.TrimSpace(r.Status)))
		switch st {
		case todo.StatusPending, todo.StatusInProgress, todo.StatusCompleted:
			// ok
		default:
			st = todo.StatusPending
		}
		items = append(items, todo.TodoItem{
			ID:         r.ID,
			Content:    strings.TrimSpace(r.Content),
			Status:     st,
			ActiveForm: strings.TrimSpace(r.ActiveForm),
		})
	}

	saved := t.store.Replace(sessionID, items)

	// Render a compact summary the model can read on the next turn without
	// needing another tool call. Symbols mirror common UIs: ☐ ▶ ✓
	var sb strings.Builder
	pending, active, done, total := t.store.Counts(sessionID)
	fmt.Fprintf(&sb, "Todo list updated (%d pending · %d in progress · %d done · %d total)\n",
		pending, active, done, total)
	for i, it := range saved {
		marker := "☐"
		switch it.Status {
		case todo.StatusInProgress:
			marker = "▶"
		case todo.StatusCompleted:
			marker = "✓"
		}
		label := it.Content
		if it.Status == todo.StatusInProgress && it.ActiveForm != "" {
			label = it.ActiveForm
		}
		fmt.Fprintf(&sb, "  %d. %s %s\n", i+1, marker, label)
	}

	return &types.ToolResult{Success: true, Content: strings.TrimRight(sb.String(), "\n")}, nil
}
