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
	"path/filepath"
	"strings"
	"sync"
	"time"

	executil "github.com/ponygates/icode/internal/executil"
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
		// init/config are one-shot; bound them so a broken git install
		// doesn't hang first-use of undo indefinitely.
		initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer initCancel()
		if _, e := fs.gitCmd(initCtx, "init"); e != nil {
			return nil, fmt.Errorf("undo: init: %w", e)
		}
		// Set local git config so commits work
		_, _ = fs.gitCmd(initCtx, "config", "user.name", "iCode-Undo")
		_, _ = fs.gitCmd(initCtx, "config", "user.email", "undo@icode.local")
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

	// The snapshot to restore to. Each staged snapshot records the "before"
	// state of a tool call, so restoring N steps back means reading the
	// content captured HEAD~(steps-1) (steps=1 → HEAD, the most recent
	// snapshot).
	target := fmt.Sprintf("HEAD~%d", steps-1)

	// Get the file list that changed between the step before the target and
	// HEAD — i.e. the files touched by the last `steps` snapshots.
	diffFrom := fmt.Sprintf("HEAD~%d", steps)
	out, err := fs.gitCmd(ctx, "diff", "--name-only", diffFrom, "HEAD")
	if err != nil {
		// Try with fewer steps
		target = "HEAD"
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

	// Restore each file from the TARGET commit straight out of git. Reading
	// the shadow worktree would be wrong: mirrorTree always holds the LATEST
	// mirror, so /undo N (N≥2) must consult git objects, not the worktree.
	var restored []string
	for _, relPath := range changedFiles {
		if relPath == "" {
			continue
		}
		dstPath := filepath.Join(fs.projectRoot, relPath)

		content, cerr := fs.gitCmd(ctx, "cat-file", "blob", target+":"+relPath)
		if cerr != nil {
			// File did not exist at the target snapshot — it was created in
			// the meantime. Remove it so /undo fully reverts the tree.
			_ = os.Remove(dstPath)
			restored = append(restored, relPath)
			continue
		}

		// Preserve the executable bit recorded by git for this path.
		mode := os.FileMode(0644)
		if modeOut, me := fs.gitCmd(ctx, "ls-tree", target, "--", relPath); me == nil && strings.HasPrefix(modeOut, "100755") {
			mode = 0755
		}

		dstDir := filepath.Dir(dstPath)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return nil, fmt.Errorf("undo: mkdir %s: %w", dstDir, err)
		}
		if err := os.WriteFile(dstPath, []byte(content), mode); err != nil {
			continue
		}
		restored = append(restored, relPath)
	}

	// Soft-reset the shadow git to forget the undone commits
	_, _ = fs.gitCmd(ctx, "reset", "--soft", target)

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
	cmd := executil.CommandContext(ctx, "git", cmdArgs...)
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

// SnapshotProject captures the entire project tree BEFORE a bash command runs
// (bash can modify any file, so per-file snapshots are insufficient). It
// mirrors the working tree into the shadow repo and commits. Returns the
// commit hash. Files that are already identical to the last snapshot still
// get a commit (with --allow-empty) so Undo steps stay aligned 1:1 with
// tool executions.
func (fs *FileSnapshot) SnapshotProject(ctx context.Context) (string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Mirror the project tree into the shadow worktree, skipping the undo
	// repo itself and VCS internals (cheap copy — only changed files land in
	// the git object store via the single add below).
	if err := fs.mirrorTree(ctx); err != nil {
		return "", err
	}

	if _, err := fs.gitCmd(ctx, "add", "-A"); err != nil {
		return "", fmt.Errorf("undo: add: %w", err)
	}
	if _, err := fs.gitCmd(ctx, "commit", "--allow-empty", "-m",
		fmt.Sprintf("snapshot project before bash @ %s", time.Now().Format("2006-01-02 15:04:05"))); err != nil {
		return "", fmt.Errorf("undo: commit: %w", err)
	}
	hash, _ := fs.gitCmd(ctx, "rev-parse", "HEAD")
	return strings.TrimSpace(hash), nil
}

// mirrorTree copies the project's regular files into the shadow worktree,
// preserving relative paths, and prunes shadow paths that no longer exist in
// the project. Large/binary blobs are still copied by reference into git's
// object store on add; the working-tree copy keeps Undo's restore simple.
func (fs *FileSnapshot) mirrorTree(ctx context.Context) error {
	// Prune stale shadow entries (deleted project files).
	shadowRel := func(abs string) (string, bool) {
		rel, err := filepath.Rel(fs.undoDir, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", false
		}
		return rel, true
	}
	_ = filepath.Walk(fs.undoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, ok := shadowRel(path)
		if !ok {
			return nil
		}
		// Never prune inside the shadow repo's own .git.
		if info.IsDir() && (rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator))) {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return nil
		}
		src := filepath.Join(fs.projectRoot, rel)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			_ = os.Remove(path)
		}
		return nil
	})

	// Copy the project tree over (skip the undo repo itself).
	skipPrefix := fs.undoDir + string(filepath.Separator)
	return filepath.Walk(fs.projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path == fs.undoDir || strings.HasPrefix(path, skipPrefix) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(fs.projectRoot, path)
		if err != nil {
			return nil
		}
		// Skip the shadow repo itself and the user's git internals.
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return nil
		}
		if rel == ".icode" || strings.HasPrefix(rel, ".icode"+string(filepath.Separator)) {
			return nil
		}
		dest := filepath.Join(fs.undoDir, rel)
		// Skip the copy when the shadow file already matches (same size and
		// mtime) — consecutive/bash snapshots rarely touch every file, so this
		// turns the per-bash full-tree mirror into mostly a no-op.
		if di, derr := os.Stat(dest); derr == nil && di.Size() == info.Size() && di.ModTime().Equal(info.ModTime()) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		return copyFile(path, dest)
	})
}

// copyFile copies a file (preserving mode) — plain copy, no overwrite checks.
func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, fi.Mode().Perm())
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

// BeforeBash snapshots the whole project before a shell command runs, so
// /undo can restore files modified by bash. Call right before executing the
// bash tool (best-effort; a failure leaves the undo chain unbroken).
func BeforeBash(ctx context.Context) {
	if DefaultUndo == nil {
		return
	}
	_, _ = DefaultUndo.SnapshotProject(ctx)
}
