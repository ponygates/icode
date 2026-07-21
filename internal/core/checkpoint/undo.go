// Package checkpoint — Git Snapshot / Undo System.
//
// OpenCode 风格的独立 .git 快照仓库，支持多级 /undo 回退。
// 与用户仓库完全隔离，不干扰用户自身的 git 操作。
//
// 工作原理：
//  1. 在项目根目录创建 .icode/undo/ 作为独立 git 仓库
//  2. 每次写操作前快照被修改的文件
//  3. 快照仓库使用 git write-tree / git commit 追踪
//  4. /undo N 恢复到 N 步前的状态
//
// 此系统与现有的 checkpoint/store.go 互补：
//  - checkpoint/store.go: 对话级别回溯
//  - undo.go: 文件级别回退（OpenCode parity）

package checkpoint

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// FileSnapshot manages undo snapshots of workspace files.
type FileSnapshot struct {
	mu sync.Mutex

	projectRoot string // absolute path to the project root
	undoDir     string // .icode/undo/ — the shadow git repo
	gitDir      string // .icode/undo/.git
}

// NewFileSnapshot creates or opens an undo repository for the given project.
// The project root is auto-detected from the current working directory by
// looking for a .git directory, or falls back to cwd.
func NewFileSnapshot(projectRoot string) (*FileSnapshot, error) {
	if projectRoot == "" {
		var err error
		projectRoot, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("undo: cannot determine project root: %w", err)
		}
	}

	// Use absolute path
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("undo: absolute path: %w", err)
	}

	undoDir := filepath.Join(absRoot, ".icode", "undo")
	gitDir := filepath.Join(undoDir, ".git")

	fs := &FileSnapshot{
		projectRoot: absRoot,
		undoDir:     undoDir,
		gitDir:      gitDir,
	}

	// Initialize shadow git repo if needed
	if err := os.MkdirAll(undoDir, 0755); err != nil {
		return nil, fmt.Errorf("undo: mkdir: %w", err)
	}

	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		if _, e := fs.gitCmd(context.Background(), "init"); e != nil {
			return nil, fmt.Errorf("undo: init: %w", e)
		}
		// Set local git config so commits work
		_, _ = fs.gitCmd(context.Background(), "config", "user.name", "iCode-Undo")
		_, _ = fs.gitCmd(context.Background(), "config", "user.email", "undo@icode.local")
	}

	return fs, nil
}

// SnapshotFile captures a file's current state BEFORE it is modified.
// Returns the commit hash, or empty if the file doesn't exist yet.
func (fs *FileSnapshot) SnapshotFile(ctx context.Context, filePath string) (string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("undo: abs: %w", err)
	}

	// Ensure the file is within the project root
	if !strings.HasPrefix(absPath, fs.projectRoot) {
		return "", fmt.Errorf("undo: file %s is outside project root %s", absPath, fs.projectRoot)
	}

	// Check if file exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return "", nil // new file, nothing to snapshot
	}

	// Compute relative path from project root
	relPath, err := filepath.Rel(fs.projectRoot, absPath)
	if err != nil {
		return "", fmt.Errorf("undo: rel: %w", err)
	}

	// Copy the original file into the shadow git worktree
	destPath := filepath.Join(fs.undoDir, relPath)
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("undo: mkdir dest: %w", err)
	}

	// Copy file content
	input, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("undo: read: %w", err)
	}
	if err := os.WriteFile(destPath, input, 0644); err != nil {
		return "", fmt.Errorf("undo: write: %w", err)
	}

	// Git add & commit
	if _, err := fs.gitCmd(ctx, "add", relPath); err != nil {
		return "", fmt.Errorf("undo: add: %w", err)
	}

	// Check if there's actually a change
	diffOut, _ := fs.gitCmd(ctx, "diff", "--cached", "--quiet")
	if diffOut == "" {
		// No changes, but still might need to commit if this is the first commit
		out, err := fs.gitCmd(ctx, "commit", "--allow-empty", "-m",
			fmt.Sprintf("snapshot %s before modification", relPath))
		if err != nil {
			return "", fmt.Errorf("undo: commit: %w\n%s", err, out)
		}
	} else {
		out, err := fs.gitCmd(ctx, "commit", "-m",
			fmt.Sprintf("snapshot %s before modification", relPath))
		if err != nil {
			return "", fmt.Errorf("undo: commit: %w\n%s", err, out)
		}
		_ = out
	}

	hash, _ := fs.gitCmd(ctx, "rev-parse", "HEAD")
	return strings.TrimSpace(hash), nil
}

// ListSnapshots returns the recent snapshot history.
func (fs *FileSnapshot) ListSnapshots(ctx context.Context, n int) ([]SnapshotEntry, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if n <= 0 {
		n = 20
	}

	out, err := fs.gitCmd(ctx, "log", fmt.Sprintf("-%d", n),
		"--format=%H|%ct|%s", "--diff-filter=AM")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil, fmt.Errorf("no snapshots found")
	}

	var entries []SnapshotEntry
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		var ts int64
		fmt.Sscanf(parts[1], "%d", &ts)
		entries = append(entries, SnapshotEntry{
			Hash:    parts[0],
			Message: parts[2],
			When:    ts,
		})
	}
	return entries, nil
}

// Undo restores files to their state N snapshots ago.
// Returns the list of files that were restored.
func (fs *FileSnapshot) Undo(ctx context.Context, steps int) ([]string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if steps <= 0 {
		steps = 1
	}

	// Get the file list that changed in the last N commits
	out, err := fs.gitCmd(ctx, "diff", "--name-only",
		fmt.Sprintf("HEAD~%d", steps), "HEAD~0")
	if err != nil {
		// Try with fewer steps
		out2, e2 := fs.gitCmd(ctx, "diff", "--name-only", "HEAD~1", "HEAD")
		if e2 != nil {
			return nil, fmt.Errorf("undo: no snapshots available")
		}
		out = out2
	}

	changedFiles := strings.Fields(out)
	if len(changedFiles) == 0 {
		return nil, fmt.Errorf("undo: no files changed in recent snapshots")
	}

	// Restore each file from the shadow git worktree to the actual project
	var restored []string
	for _, relPath := range changedFiles {
		if relPath == "" {
			continue
		}
		srcPath := filepath.Join(fs.undoDir, relPath)
		dstPath := filepath.Join(fs.projectRoot, relPath)

		// Check if the source (snapshot) exists
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			continue
		}

		// Copy from shadow to actual project
		input, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		dstDir := filepath.Dir(dstPath)
		os.MkdirAll(dstDir, 0755)
		if err := os.WriteFile(dstPath, input, 0644); err != nil {
			continue
		}
		restored = append(restored, relPath)
	}

	// Soft-reset the shadow git to forget the undone commits
	_, _ = fs.gitCmd(ctx, "reset", "--soft", fmt.Sprintf("HEAD~%d", steps))

	return restored, nil
}

// SnapshotEntry represents one entry in the undo history.
type SnapshotEntry struct {
	Hash    string `json:"hash"`
	Message string `json:"message"`
	When    int64  `json:"when"` // unix timestamp
}

func (fs *FileSnapshot) gitCmd(ctx context.Context, args ...string) (string, error) {
	cmdArgs := append([]string{
		"--git-dir", fs.gitDir,
		"--work-tree", fs.undoDir,
	}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// DefaultUndo is the global undo manager for the current session.
var DefaultUndo *FileSnapshot

// InitUndo initializes the global undo manager for a project.
func InitUndo(projectRoot string) error {
	fs, err := NewFileSnapshot(projectRoot)
	if err != nil {
		return err
	}
	DefaultUndo = fs
	return nil
}

// BeforeTool snapshots all files that a tool is about to modify.
// Call this before executing write_file, edit, or similar tools.
func BeforeTool(ctx context.Context, toolName, filePath string) {
	if DefaultUndo == nil {
		return
	}
	if filePath == "" {
		return
	}
	mutatingTools := map[string]bool{"write_file": true, "edit": true, "bash": false}
	if !mutatingTools[toolName] {
		return
	}
	_, _ = DefaultUndo.SnapshotFile(ctx, filePath)
}
