// Package tool provides the built-in tool system for iCode.
package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// Registry holds all available tools.
type Registry struct {
	tools map[string]types.Tool
}

// NewRegistry creates a tool registry with all built-in tools.
func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]types.Tool)}

	// Register built-in tools
	r.Register(&BashTool{})
	r.Register(&ReadFileTool{})
	r.Register(&WriteFileTool{})
	r.Register(&GrepTool{})
	r.Register(&GlobTool{})
	r.Register(&LSTool{})
	r.Register(&FetchTool{})

	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(t types.Tool) {
	r.tools[t.Def().Name] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (types.Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// ListDefs returns tool definitions for all registered tools.
func (r *Registry) ListDefs() []types.ToolDef {
	defs := make([]types.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Def())
	}
	return defs
}

// Execute runs a named tool with arguments.
func (r *Registry) Execute(ctx context.Context, name, args string) (*types.ToolResult, error) {
	t, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return t.Execute(ctx, args)
}

// ============================================================================
// BashTool — execute shell commands
// ============================================================================

type BashTool struct{}

func (t *BashTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "bash",
		Description: "Execute a shell command and return its output. The command runs in a sandboxed environment.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	cmdStr, err := parseArg(args, "command")
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if strings.Contains(os.Getenv("OS"), "Windows") {
		cmd = exec.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &types.ToolResult{
				Success: false,
				Content: string(output),
				Error:   "command timed out after 120 seconds",
			}, nil
		}
		return &types.ToolResult{
			Success: false,
			Content: string(output),
			Error:   err.Error(),
		}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

// ============================================================================
// ReadFileTool — read file contents
// ============================================================================

type ReadFileTool struct{}

func (t *ReadFileTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "read_file",
		Description: "Read the contents of a file at a given path.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute or relative path to the file",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	path, err := parseArg(args, "path")
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return &types.ToolResult{Success: false, Content: "", Error: err.Error()}, nil
	}

	return &types.ToolResult{Success: true, Content: string(data)}, nil
}

// ============================================================================
// WriteFileTool — write content to a file
// ============================================================================

type WriteFileTool struct{}

func (t *WriteFileTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "write_file",
		Description: "Write content to a file, creating parent directories as needed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute or relative path to the file",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write to the file",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	path, err := parseArg(args, "path")
	if err != nil {
		return nil, err
	}
	content, err := parseArg(args, "content")
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	return &types.ToolResult{Success: true, Content: fmt.Sprintf("Wrote %d bytes to %s", len(content), path)}, nil
}

// ============================================================================
// GrepTool — search text in files
// ============================================================================

type GrepTool struct{}

func (t *GrepTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "grep",
		Description: "Search for a pattern in files. Returns matching lines with file paths and line numbers.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "The regex pattern to search for",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search in",
				},
			},
			"required": []string{"pattern", "path"},
		},
	}
}

func (t *GrepTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	pattern, err := parseArg(args, "pattern")
	if err != nil {
		return nil, err
	}
	searchPath, err := parseArg(args, "path")
	if err != nil {
		return nil, err
	}

	// Use grep command for reliability
	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx2, "grep", "-rn", "-I", pattern, searchPath)
	output, _ := cmd.CombinedOutput()

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

// ============================================================================
// GlobTool — find files by pattern
// ============================================================================

type GlobTool struct{}

func (t *GlobTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "glob",
		Description: "Find files matching a glob pattern (e.g., '**/*.go', 'src/*.ts').",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern to match files against",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GlobTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	pattern, err := parseArg(args, "pattern")
	if err != nil {
		return nil, err
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	var sb strings.Builder
	for _, m := range matches {
		sb.WriteString(m)
		sb.WriteString("\n")
	}
	return &types.ToolResult{Success: true, Content: sb.String()}, nil
}

// ============================================================================
// LSTool — list directory contents
// ============================================================================

type LSTool struct{}

func (t *LSTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "ls",
		Description: "List files and directories at a given path.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path to list",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *LSTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	dir, err := parseArg(args, "path")
	if err != nil {
		dir = "."
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	var sb strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			sb.WriteString(fmt.Sprintf("DIR  %s\n", e.Name()))
		} else {
			info, _ := e.Info()
			sb.WriteString(fmt.Sprintf("FILE %-30s %d bytes\n", e.Name(), info.Size()))
		}
	}
	return &types.ToolResult{Success: true, Content: sb.String()}, nil
}

// ============================================================================
// FetchTool — HTTP GET a URL
// ============================================================================

type FetchTool struct{}

func (t *FetchTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "fetch",
		Description: "Fetch content from a URL (HTTP GET).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (t *FetchTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	url, err := parseArg(args, "url")
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	httpreq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	httpreq.Header.Set("User-Agent", "iCode/0.1.0")

	resp, err := client.Do(httpreq)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	maxSize := int64(256 * 1024) // 256KB limit
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	content := string(body)
	if int64(len(body)) >= maxSize {
		content += "\n\n[Response truncated at 256KB]"
	}

	return &types.ToolResult{
		Success: true,
		Content: content,
	}, nil
}

// ============================================================================
// Helpers
// ============================================================================

func parseArg(rawJSON, key string) (string, error) {
	// Simple argument parsing — Full JSON parser in Phase 2.
	// Expects: {"key": "value"}
	raw := strings.TrimSpace(rawJSON)
	search := fmt.Sprintf(`"%s":`, key)

	idx := strings.Index(raw, search)
	if idx < 0 {
		return "", fmt.Errorf("missing required argument: %s", key)
	}

	start := idx + len(search)
	rest := strings.TrimSpace(raw[start:])
	if !strings.HasPrefix(rest, `"`) {
		return "", fmt.Errorf("argument %s must be a string", key)
	}

	end := strings.IndexByte(rest[1:], '"')
	if end < 0 {
		return rest[1:], nil
	}

	return rest[1 : end+1], nil
}
