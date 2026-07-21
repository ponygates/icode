// Package tool provides the built-in tool system for iCode.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/executil"
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
	r.Register(&EditTool{})
	r.Register(&GrepTool{})
	r.Register(&GlobTool{})
	r.Register(&LSTool{})
	r.Register(&FetchTool{})
	r.Register(&GitDiffTool{})
	r.Register(&GitCommitTool{})
	r.Register(&GitStatusTool{})
	r.Register(&SearchReplaceTool{})
	r.Register(&WebSearchTool{})
	// Built-in disk management tools (no AI model required)
	r.Register(&DiskUsageTool{})
	r.Register(&DiskCleanupTool{})
	// Sub-agent delegation (Claude Code task tool parity)
	// The runner is injected later via SetTaskRunner when the Engine wires it up.
	r.Register(NewTaskTool(nil))
	// Session-scoped scratchpad tool
	r.Register(NewTodoWriteTool(nil))

	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(t types.Tool) {
	r.tools[t.Def().Name] = t
}

// SetTaskRunner injects the sub-agent runner into the Task tool.
// Called during Engine initialisation once the runner is available.
func (r *Registry) SetTaskRunner(runner SubAgentRunner) {
	if tt, ok := r.tools["task"]; ok {
		if task, ok := tt.(*TaskTool); ok {
			task.runner = runner
		}
	}
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (types.Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Unregister removes a tool from the registry by name.
func (r *Registry) Unregister(name string) {
	delete(r.tools, name)
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
		Description: "Execute a shell command and return its output. The command runs in the project directory by default. Use 'cwd' to change the working directory for the command.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for the command. Defaults to project root. Accepts absolute paths like C:\\Users\\... or relative paths.",
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
	workDir, _ := parseArg(args, "cwd")

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if strings.Contains(os.Getenv("OS"), "Windows") {
		cmd = executil.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = executil.CommandContext(ctx, "sh", "-c", cmdStr)
	}

	// Apply working directory
	if workDir != "" {
		if absDir, err := filepath.Abs(workDir); err == nil {
			if info, err := os.Stat(absDir); err == nil && info.IsDir() {
				cmd.Dir = absDir
			}
		}
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
		// Default to current directory
		searchPath = "."
	}

	// Use Go-native grep (cross-platform, no external dependency)
	var results []string
	filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			// Skip common directories
			if info != nil && info.IsDir() {
				name := info.Name()
				if name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == "release" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Skip binary files
		if isBinaryFile(path) {
			return nil
		}

		// Read file and search
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if strings.Contains(line, pattern) {
				results = append(results, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})

	if len(results) == 0 {
		return &types.ToolResult{Success: true, Content: "No matches found."}, nil
	}

	// Limit results
	if len(results) > 100 {
		results = results[:100]
		results = append(results, fmt.Sprintf("\n... (%d more matches truncated)", len(results)-100))
	}

	return &types.ToolResult{Success: true, Content: strings.Join(results, "\n")}, nil
}

func isBinaryFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	binaryExts := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".bmp": true, ".ico": true, ".woff": true, ".woff2": true,
		".ttf": true, ".eot": true, ".zip": true, ".gz": true,
		".tar": true, ".7z": true, ".rar": true, ".pdf": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true,
	}
	return binaryExts[ext]
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
// EditTool — precise in-place string replacement with MultiEdit support
// ============================================================================

type EditTool struct{}

type editOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

type editInput struct {
	FilePath string   `json:"file_path"`
	OldStr   string   `json:"old_string"`
	NewStr   string   `json:"new_string"`
	Replace  bool     `json:"replace_all"`
	Edits    []editOp `json:"edits"`
}

func (t *EditTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "edit",
		Description: "Edit a file by replacing exact string matches. Preserves indentation. " +
			"Two modes: (1) single edit via file_path+old_string+new_string, or " +
			"(2) MultiEdit via file_path+edits[] for multiple replacements on one file.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Path to the file to edit",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "[single mode] Exact string to find and replace",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "[single mode] Replacement string",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "[single mode] Replace all occurrences (default: false, fails on multiple matches)",
				},
				"edits": map[string]any{
					"type":        "array",
					"description": "[MultiEdit mode] Ordered list of replacements to apply to the same file",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string": map[string]any{
								"type":        "string",
								"description": "Exact text to find",
							},
							"new_string": map[string]any{
								"type":        "string",
								"description": "Replacement text",
							},
							"replace_all": map[string]any{
								"type":        "boolean",
								"description": "Replace all occurrences of old_string",
							},
						},
						"required": []string{"old_string", "new_string"},
					},
				},
			},
			"oneOf": []any{
				map[string]any{"required": []string{"file_path", "old_string", "new_string"}},
				map[string]any{"required": []string{"file_path", "edits"}},
			},
		},
	}
}

func (t *EditTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in editInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{
			Success: false, Error: fmt.Sprintf("invalid args: %v", err),
		}, nil
	}
	if in.FilePath == "" {
		return &types.ToolResult{Success: false, Error: "file_path is required"}, nil
	}

	content, err := os.ReadFile(in.FilePath)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("read %s: %v", in.FilePath, err)}, nil
	}
	text := string(content)
	original := text

	totalReplacements := 0

	if len(in.Edits) > 0 {
		// MultiEdit mode: apply edits sequentially
		for _, ed := range in.Edits {
			if ed.OldString == "" {
				continue
			}
			c := strings.Count(text, ed.OldString)
			if c == 0 {
				// Let the model know which edit failed but keep partial progress
				return &types.ToolResult{
					Success: false,
					Content: fmt.Sprintf("after %d replacements, failed on: old_string %q not found.",
						totalReplacements, truncateStr(ed.OldString, 60)),
				}, nil
			}
			if c > 1 && !ed.ReplaceAll {
				return &types.ToolResult{
					Success: false,
					Content: fmt.Sprintf("after %d replacements: old_string %q appears %d times. Use replace_all.",
						totalReplacements, truncateStr(ed.OldString, 60), c),
				}, nil
			}
			if ed.ReplaceAll {
				text = strings.ReplaceAll(text, ed.OldString, ed.NewString)
			} else {
				text = strings.Replace(text, ed.OldString, ed.NewString, 1)
			}
			totalReplacements++
		}
	} else {
		// Single edit mode (backward compatible)
		if in.OldStr == "" {
			return &types.ToolResult{Success: false, Error: "old_string is required"}, nil
		}
		c := strings.Count(text, in.OldStr)
		if c == 0 {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("old_string not found in %s.", in.FilePath),
			}, nil
		}
		if c > 1 && !in.Replace {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("old_string appears %d times. Use replace_all or add context.", c),
			}, nil
		}
		if in.Replace {
			text = strings.ReplaceAll(text, in.OldStr, in.NewStr)
			totalReplacements = c
		} else {
			text = strings.Replace(text, in.OldStr, in.NewStr, 1)
			totalReplacements = 1
		}
	}

	if err := os.WriteFile(in.FilePath, []byte(text), 0644); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("write: %v", err)}, nil
	}

	diffOut := unifiedDiff(in.FilePath, original, text)
	return &types.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Edited %s (%d replacements)\n%s", in.FilePath, totalReplacements, diffOut),
	}, nil
}

// unifiedDiff produces a compact unified-diff-format string showing what
// changed. Uses Myers diff internally (simplified inline implementation).
func unifiedDiff(path, oldText, newText string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	// Find changed regions
	type hunk struct{ oldStart, newStart, oldCount, newCount int }
	var hunks []hunk

	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		if oldLines[i] == newLines[j] {
			i++
			j++
			continue
		}
		h := hunk{oldStart: i, newStart: j, oldCount: 0, newCount: 0}
		for i < len(oldLines) && j < len(newLines) && oldLines[i] != newLines[j] {
			h.oldCount++
			h.newCount++
			i++
			j++
		}
		// Drain remaining when one side is exhausted
		for i < len(oldLines) && (j >= len(newLines) || oldLines[i] != newLines[j]) {
			h.oldCount++
			i++
		}
		for j < len(newLines) && (i >= len(oldLines) || oldLines[i] != newLines[j]) {
			h.newCount++
			j++
		}
		if h.oldCount > 0 || h.newCount > 0 {
			hunks = append(hunks, h)
		}
	}
	// Remaining lines after the shorter side exhausted
	if i < len(oldLines) || j < len(newLines) {
		hunks = append(hunks, hunk{
			oldStart: i, newStart: j,
			oldCount: len(oldLines) - i,
			newCount: len(newLines) - j,
		})
	}

	if len(hunks) == 0 {
		return "(no changes)"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- %s\n+++ %s\n", path, path))
	for _, h := range hunks {
		if h.oldCount == 0 {
			h.oldStart-- // context before insertion
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
			h.oldStart+1, h.oldCount,
			h.newStart+1, h.newCount)

		o, n := h.oldStart, h.newStart
		for k := 0; k < h.oldCount || k < h.newCount; k++ {
			switch {
			case k < h.oldCount && k < h.newCount:
				b.WriteString(fmt.Sprintf("-%s\n+%s\n", oldLines[o+k], newLines[n+k]))
			case k < h.oldCount:
				b.WriteString(fmt.Sprintf("-%s\n", oldLines[o+k]))
			case k < h.newCount:
				b.WriteString(fmt.Sprintf("+%s\n", newLines[n+k]))
			}
		}
		_ = n
	}
	return b.String()
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func parseBoolArg(args, key string) (bool, error) {
	parsed, err := parseJSONArgs(args)
	if err != nil {
		return false, nil
	}
	if v, ok := parsed[key]; ok {
		if b, ok := v.(bool); ok {
			return b, nil
		}
	}
	return false, nil
}

// parseJSONArgs parses a JSON argument string into a map.
func parseJSONArgs(rawJSON string) (map[string]any, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ============================================================================
// GitTool — git diff/commit/status
// ============================================================================

type GitDiffTool struct{}

func (t *GitDiffTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_diff",
		Description: "Show git diff for staged or unstaged changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"staged": map[string]any{
					"type":        "boolean",
					"description": "If true, show staged (cached) changes. Default: false.",
				},
			},
		},
	}
}

func (t *GitDiffTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	staged, _ := parseBoolArg(args, "staged")

	cmdArgs := []string{"diff"}
	if staged {
		cmdArgs = append(cmdArgs, "--cached")
	}

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := executil.CommandContext(ctx2, "git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git diff: %v", err)}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

type GitCommitTool struct{}

func (t *GitCommitTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_commit",
		Description: "Stage all changes and commit with a message.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "Commit message",
				},
			},
			"required": []string{"message"},
		},
	}
}

func (t *GitCommitTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	msg, err := parseArg(args, "message")
	if err != nil {
		return nil, err
	}

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// git add -A
	addCmd := executil.CommandContext(ctx2, "git", "add", "-A")
	if err := addCmd.Run(); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git add: %v", err)}, nil
	}

	// git commit -m
	commitCmd := executil.CommandContext(ctx2, "git", "commit", "-m", msg)
	output, err := commitCmd.CombinedOutput()
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git commit: %v\n%s", err, string(output))}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

type GitStatusTool struct{}

func (t *GitStatusTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_status",
		Description: "Show git status (modified, staged, untracked files).",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *GitStatusTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := executil.CommandContext(ctx2, "git", "status", "--short", "--branch")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git status: %v", err)}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
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
	// Primary: strict JSON parsing (handles escaped quotes, nested content).
	if m, err := parseJSONArgs(rawJSON); err == nil {
		v, ok := m[key]
		if !ok {
			return "", fmt.Errorf("missing required argument: %s", key)
		}
		switch val := v.(type) {
		case string:
			return val, nil
		case bool:
			return fmt.Sprintf("%t", val), nil
		case float64:
			// Preserve integer-looking numbers without trailing .0
			if val == float64(int64(val)) {
				return fmt.Sprintf("%d", int64(val)), nil
			}
			return fmt.Sprintf("%v", val), nil
		default:
			return fmt.Sprintf("%v", val), nil
		}
	}

	// Fallback: lenient substring search for loosely-formatted arguments.
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
