// Package tool — CodeSearchTool: 基于 CodeGraph 的符号检索.
//
// Claude Code parity: 对标 claude code 的符号/语义检索。索引在首次调用时懒构建
// （全仓库扫描 Go/TS/JS/Python 等符号），之后常驻内存；传 rebuild=true 可强制重建。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/ponygates/icode/internal/core/codegraph"
	"github.com/ponygates/icode/internal/types"
)

// CodeSearchTool searches source symbols (functions, types, classes…) via
// the in-memory CodeGraph index.
type CodeSearchTool struct {
	mu    sync.Mutex
	graph *codegraph.Graph
	root  string
}

// NewCodeSearchTool creates a code search tool rooted at the current
// working directory.
func NewCodeSearchTool() *CodeSearchTool {
	wd, _ := os.Getwd()
	return &CodeSearchTool{root: wd}
}

func (t *CodeSearchTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "code_search",
		Description: "Search for symbol definitions (functions, types, structs, classes, methods) across the codebase using a pre-built code index. Much faster and more precise than grep for questions like 'where is X defined'. The index is built lazily on first use.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Symbol name to search (case-insensitive substring match, e.g. 'NewEngine', 'Registry')",
				},
				"file": map[string]any{
					"type":        "string",
					"description": "Optional: list all symbols defined in this file instead of searching by name",
				},
				"rebuild": map[string]any{
					"type":        "boolean",
					"description": "Force rebuilding the index (use after large refactors). Default false.",
				},
			},
			"required": []string{},
		},
	}
}

func (t *CodeSearchTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in struct {
		Query   string `json:"query"`
		File    string `json:"file"`
		Rebuild bool   `json:"rebuild"`
	}
	if args != "" {
		if err := json.Unmarshal([]byte(args), &in); err != nil {
			return &types.ToolResult{Success: false, Error: "invalid arguments: " + err.Error()}, nil
		}
	}
	if in.Query == "" && in.File == "" {
		return &types.ToolResult{Success: false, Error: "provide 'query' (symbol name) or 'file'"}, nil
	}

	g, err := t.getGraph(in.Rebuild)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "index build failed: " + err.Error()}, nil
	}

	var syms []codegraph.Symbol
	if in.File != "" {
		syms = g.SymbolsInFile(in.File)
	} else {
		syms = g.Search(in.Query)
	}

	if len(syms) == 0 {
		return &types.ToolResult{Success: true, Content: fmt.Sprintf("No symbols found (index: %d symbols). Try grep for non-definition matches.", g.Count())}, nil
	}

	const maxResults = 50
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d symbol(s) (index: %d total):\n", len(syms), g.Count())
	for i, s := range syms {
		if i >= maxResults {
			fmt.Fprintf(&b, "… and %d more (narrow your query)\n", len(syms)-maxResults)
			break
		}
		fmt.Fprintf(&b, "  %-9s %-40s %s:%d\n", s.Kind.String(), s.Name, s.FilePath, s.Line)
	}
	return &types.ToolResult{Success: true, Content: b.String()}, nil
}

// getGraph lazily builds (or rebuilds) the code index.
func (t *CodeSearchTool) getGraph(rebuild bool) (*codegraph.Graph, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.graph != nil && !rebuild {
		return t.graph, nil
	}
	g := codegraph.New()
	if err := g.Build(t.root); err != nil {
		return nil, err
	}
	t.graph = g
	return g, nil
}
