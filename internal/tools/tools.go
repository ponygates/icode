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

type BaseTool struct {
	name        string
	description string
	config      Config
}

func (t *BaseTool) Name() string        { return t.name }
func (t *BaseTool) Description() string { return t.description }

type ReadTool struct{ BaseTool }

func NewReadTool(cfg Config) *ReadTool {
	return &ReadTool{BaseTool{
		name:        "read",
		description: "Read the contents of a file at the given path",
		config:      cfg,
	}}
}

func (t *ReadTool) Execute(ctx context.Context, args string) (string, error) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
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
		config:      cfg,
	}}
}

func (t *WriteTool) Execute(ctx context.Context, args string) (string, error) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
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

type BashTool struct{ BaseTool }

func NewBashTool(cfg Config) *BashTool {
	return &BashTool{BaseTool{
		name:        "bash",
		description: "Execute a bash command in the shell",
		config:      cfg,
	}}
}

func (t *BashTool) Execute(ctx context.Context, args string) (string, error) {
	var req struct {
		Command     string `json:"command"`
		TimeoutMs   int    `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
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
	if err != nil {
		return string(out), fmt.Errorf("command failed: %w\nOutput: %s", err, string(out))
	}
	return string(out), nil
}

type GrepTool struct{ BaseTool }

func NewGrepTool(cfg Config) *GrepTool {
	return &GrepTool{BaseTool{
		name:        "grep",
		description: "Search for a pattern in file contents",
		config:      cfg,
	}}
}

func (t *GrepTool) Execute(ctx context.Context, args string) (string, error) {
	var req struct {
		Pattern string `json:"pattern"`
		Include string `json:"include,omitempty"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	searchDir := t.config.WorkspaceRoot
	cmd := exec.CommandContext(ctx, "rg", "-n", req.Pattern, searchDir)
	if req.Include != "" {
		cmd.Args = append(cmd.Args, "-g", req.Include)
	}
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
		description: "Find files matching a glob pattern",
		config:      cfg,
	}}
}

func (t *GlobTool) Execute(ctx context.Context, args string) (string, error) {
	var req struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	matches, err := filepath.Glob(filepath.Join(t.config.WorkspaceRoot, req.Pattern))
	if err != nil {
		return "", err
	}
	return strings.Join(matches, "\n"), nil
}
