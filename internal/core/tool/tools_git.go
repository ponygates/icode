package tool

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/types"
)

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
		Description: "Stage changes and commit with a message. By default stages ALL changes; pass 'files' to stage specific paths only.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "Commit message",
				},
				"files": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional: paths to stage and commit (relative to repo root). When omitted, all changes are staged.",
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

	// Stage specific files when given; otherwise stage everything. Selective
	// staging keeps unrelated work out of the commit (Claude Code parity).
	var addArgs []string
	if files := parseStringArrayArg(args, "files"); len(files) > 0 {
		addArgs = append(addArgs, files...)
	} else {
		addArgs = append(addArgs, "-A")
	}
	addCmd := executil.CommandContext(ctx2, "git", append([]string{"add"}, addArgs...)...)
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

type GitLogTool struct{}

func (t *GitLogTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_log",
		Description: "Show recent commit history (hash, date, message).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"n": map[string]any{
					"type":        "integer",
					"description": "Number of commits to show. Default: 20.",
				},
			},
		},
	}
}

func (t *GitLogTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	n := 20
	if nStr, err := parseArg(args, "n"); err == nil {
		if v, convErr := strconv.Atoi(strings.TrimSpace(nStr)); convErr == nil && v > 0 && v <= 200 {
			n = v
		}
	}

	ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := executil.CommandContext(ctx2, "git", "log",
		"--pretty=format:%h %ad %s", "--date=short", "-n", strconv.Itoa(n))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git log: %v", err)}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

type GitBranchTool struct{}

func (t *GitBranchTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_branch",
		Description: "List local/remote branches (current marked with *), or switch to an existing branch by name.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch name to switch to. Omit to list branches.",
				},
			},
		},
	}
}

func (t *GitBranchTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	branch, _ := parseArg(args, "branch")

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if branch != "" {
		cmd := executil.CommandContext(ctx2, "git", "checkout", branch)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("git checkout: %v\n%s", err, string(output))}, nil
		}
		return &types.ToolResult{Success: true, Content: string(output)}, nil
	}

	cmd := executil.CommandContext(ctx2, "git", "branch", "-a", "-vv")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git branch: %v", err)}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}
