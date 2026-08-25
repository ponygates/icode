package knowledge

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// Tool is the search_knowledge tool the model can call to query the KB.
type Tool struct {
	mgr  *Manager
	topK int
}

// NewTool creates a search_knowledge tool over the given manager.
func NewTool(mgr *Manager, topK int) *Tool {
	if topK <= 0 {
		topK = 5
	}
	return &Tool{mgr: mgr, topK: topK}
}

func (t *Tool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "search_knowledge",
		Description: "Search the local knowledge base for relevant passages. " +
			"Use this to answer questions grounded in the user's own documents " +
			"(产品说明、保险条款、会议纪要、学习笔记等). Returns the top matching " +
			"passages with their source files. Only call when a question may be " +
			"answered by the user's documents.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural-language search query.",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t *Tool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	if t.mgr == nil {
		return &types.ToolResult{Success: false, Error: "知识库未初始化（请配置 knowledge.dirs）"}, nil
	}
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{Success: false, Error: "invalid arguments: " + err.Error()}, nil
	}
	if strings.TrimSpace(in.Query) == "" {
		return &types.ToolResult{Success: false, Error: "query 不能为空"}, nil
	}
	if t.mgr.ChunkCount() == 0 {
		if _, err := t.mgr.Index(ctx); err != nil {
			return &types.ToolResult{Success: false, Error: "索引失败: " + err.Error()}, nil
		}
	}
	results := t.mgr.Search(in.Query, t.topK)
	return &types.ToolResult{Success: true, Content: Format(results)}, nil
}
