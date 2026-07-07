package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/permissions"
)

type Config struct {
	WorkspaceRoot string
	Permissions   *permissions.Manager
	Timeout       time.Duration
}

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, argsJSON string) (string, error)
}

type BaseTool struct {
	name        string
	description string
	parameters  map[string]any
	config      Config
}

func (t *BaseTool) Name() string                { return t.name }
func (t *BaseTool) Description() string          { return t.description }
func (t *BaseTool) Parameters() map[string]any   { return t.parameters }

var stringParam = func(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

var stringArrayParam = func(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

type ReadTool struct{ BaseTool }

func NewReadTool(cfg Config) *ReadTool {
	return &ReadTool{BaseTool{
		name:        "read",
		description: "Read the contents of a file at the given path",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": stringParam("The absolute or relative path to the file to read"),
			},
			"required": []string{"path"},
		},
		config: cfg,
	}}
}

func (t *ReadTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &req); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	absPath := filepath.Join(t.config.WorkspaceRoot, req.Path)
	if !t.config.Permissions.CanRead(absPath) {
		return "", fmt.Errorf("permission denied: reading %s", absPath)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type WriteTool struct{ BaseTool }

func NewWriteTool(cfg Config) *WriteTool {
	return &WriteTool{BaseTool{
		name:        "write",
		description: "Write content to a file at the given path",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    stringParam("The absolute or relative path to the file to write"),
				"content": stringParam("The content to write to the file"),
			},
			"required": []string{"path", "content"},
		},
		config: cfg,
	}}
}

func (t *WriteTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &req); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	absPath := filepath.Join(t.config.WorkspaceRoot, req.Path)
	if !t.config.Permissions.CanWrite(absPath) {
		return "", fmt.Errorf("permission denied: writing %s", absPath)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(absPath, []byte(req.Content), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Written %d bytes to %s", len(req.Content), req.Path), nil
}

type EditTool struct{ BaseTool }

func NewEditTool(cfg Config) *EditTool {
	return &EditTool{BaseTool{
		name:        "edit",
		description: "Replace exact text in a file. Use this to make targeted changes without rewriting the entire file.",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": stringParam("The path to the file to edit"),
				"old":       stringParam("The exact text to find and replace"),
				"new":       stringParam("The new text to replace it with"),
			},
			"required": []string{"file_path", "old", "new"},
		},
		config: cfg,
	}}
}

func (t *EditTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var req struct {
		Path string `json:"file_path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &req); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	absPath := filepath.Join(t.config.WorkspaceRoot, req.Path)
	if !t.config.Permissions.CanWrite(absPath) {
		return "", fmt.Errorf("permission denied: editing %s", absPath)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	content := string(data)
	if !strings.Contains(content, req.Old) {
		return "", fmt.Errorf("old string not found in %s", req.Path)
	}
	newContent := strings.Replace(content, req.Old, req.New, 1)
	if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Edited %s: replaced match in file", req.Path), nil
}

type BashTool struct{ BaseTool }

func NewBashTool(cfg Config) *BashTool {
	return &BashTool{BaseTool{
		name:        "bash",
		description: "Execute a shell command. Use for building, testing, running git, or any command-line task.",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": stringParam("The shell command to execute"),
				"timeout": map[string]any{"type": "integer", "description": "Timeout in milliseconds", "default": 120000},
			},
			"required": []string{"command"},
		},
		config: cfg,
	}}
}

func (t *BashTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var req struct {
		Command   string `json:"command"`
		TimeoutMs int    `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &req); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if !t.config.Permissions.CanExecute(req.Command) {
		return "", fmt.Errorf("permission denied: command not allowed")
	}
	execTimeout := t.config.Timeout
	if req.TimeoutMs > 0 {
		execTimeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}
	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	cmd := exec.CommandContext(execCtx, "powershell", "-Command", req.Command)
	cmd.Dir = t.config.WorkspaceRoot
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		if output == "" {
			return "", fmt.Errorf("command failed: %w", err)
		}
		return output, fmt.Errorf("command failed: %w\nOutput:\n%s", err, output)
	}
	return output, nil
}

type GrepTool struct{ BaseTool }

func NewGrepTool(cfg Config) *GrepTool {
	return &GrepTool{BaseTool{
		name:        "grep",
		description: "Search for a regex pattern in file contents across the codebase",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": stringParam("The regex pattern to search for"),
				"include": stringParam("Optional file glob to filter (e.g. '*.go', '*.{ts,tsx}')"),
			},
			"required": []string{"pattern"},
		},
		config: cfg,
	}}
}

func (t *GrepTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var req struct {
		Pattern string `json:"pattern"`
		Include string `json:"include,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &req); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	args := []string{"-n", req.Pattern, t.config.WorkspaceRoot}
	if req.Include != "" {
		args = append(args, "-g", req.Include)
	}
	cmd := exec.CommandContext(ctx, "rg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) == 0 {
			return "", fmt.Errorf("search failed: %w", err)
		}
	}
	return string(out), nil
}

type GlobTool struct{ BaseTool }

func NewGlobTool(cfg Config) *GlobTool {
	return &GlobTool{BaseTool{
		name:        "glob",
		description: "Find files matching a glob pattern. Use when you need to discover files by name pattern.",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": stringParam("The glob pattern to match (e.g. '**/*.go', 'src/**/*.ts')"),
			},
			"required": []string{"pattern"},
		},
		config: cfg,
	}}
}

func (t *GlobTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var req struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &req); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	matches, err := filepath.Glob(filepath.Join(t.config.WorkspaceRoot, req.Pattern))
	if err != nil {
		return "", err
	}
	return strings.Join(matches, "\n"), nil
}
