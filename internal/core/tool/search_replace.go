package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/core/searchreplace"
	"github.com/ponygates/icode/internal/types"
)

// SearchReplaceTool — proposes edits via SEARCH/REPLACE blocks.
// Unlike EditTool which applies immediately, this tool stages edits
// for user review. The user runs /review, /apply, or /reject to act.
type SearchReplaceTool struct{}

type searchReplaceInput struct {
	FilePath string `json:"file_path"`
	Search   string `json:"search"`
	Replace  string `json:"replace"`
}

func (t *SearchReplaceTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "search_replace",
		Description: "Propose a SEARCH/REPLACE edit to a file. The edit is staged for review — it is NOT applied immediately. Run /review to see staged edits, /apply to apply them, or /reject to discard.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Path to the file to edit",
				},
				"search": map[string]any{
					"type":        "string",
					"description": "Exact text to find in the file (SEARCH block)",
				},
				"replace": map[string]any{
					"type":        "string",
					"description": "Replacement text (REPLACE block)",
				},
			},
			"required": []string{"file_path", "search", "replace"},
		},
	}
}

func (t *SearchReplaceTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in searchReplaceInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{
			Success: false, Error: fmt.Sprintf("invalid args: %v", err),
		}, nil
	}
	if in.FilePath == "" {
		return &types.ToolResult{Success: false, Error: "file_path is required"}, nil
	}
	if in.Search == "" {
		return &types.ToolResult{Success: false, Error: "search is required"}, nil
	}

	idx, valid, reason := searchreplace.StageAdd(in.FilePath, in.Search, in.Replace)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Staged edit #%d to %s\n", idx, in.FilePath))
	b.WriteString(reason)
	if !valid {
		b.WriteString("\nThis edit cannot be applied until the search text is corrected.")
	}
	b.WriteString(fmt.Sprintf("\n\n/review  — review all staged edits"))
	b.WriteString(fmt.Sprintf("\n/apply   — apply all valid staged edits"))
	b.WriteString(fmt.Sprintf("\n/reject  — discard staged edits"))

	return &types.ToolResult{
		Success: true,
		Content: b.String(),
	}, nil
}
