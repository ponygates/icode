// Worktree isolation — Claude Code parity. A sub-agent flagged
// isolation:"worktree" runs against a temporary git worktree (an independent
// checkout on its own branch), so its edits and shell commands can never
// touch the user's checkout. If the agent changed nothing the worktree is
// discarded automatically; otherwise a patch is surfaced to the caller, which
// decides whether to apply it.
package checkpoint

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	executil "github.com/ponygates/icode/internal/executil"
)

// Worktree is one isolated checkout of a repository.
type Worktree struct {
	RepoDir string // the user's original repository
	Dir     string // temporary worktree path
	Branch  string // dedicated branch created for this worktree
	BaseRef string // ref the branch started from
}

// gitOut runs a git command in dir and returns trimmed stdout.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := executil.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s: %v %s", strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateWorktree adds a temporary worktree for repoDir on a fresh branch.
// repoDir must be inside a git repository; the current HEAD is used as the
// base ref.
func CreateWorktree(repoDir string) (*Worktree, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	top, err := gitOut(ctx, repoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	base, err := gitOut(ctx, top, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD: %w", err)
	}

	tmp, err := os.MkdirTemp("", "icode-wt-")
	if err != nil {
		return nil, err
	}
	branch := fmt.Sprintf("icode/wt-%d", time.Now().UnixNano())
	if _, err := gitOut(ctx, top, "worktree", "add", tmp, "-b", branch); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	return &Worktree{RepoDir: top, Dir: tmp, Branch: branch, BaseRef: base}, nil
}

// HasChanges reports whether any file differs from the worktree's starting
// point (staged, unstaged, or untracked).
func (w *Worktree) HasChanges() bool {
	out, err := gitOut(context.Background(), w.Dir, "status", "--porcelain")
	return err == nil && out != ""
}

// Diff returns the unified diff of all changes in the worktree relative to
// its base commit, including untracked files staged for visibility.
func (w *Worktree) Diff() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Stage everything so untracked files appear in `diff --cached`.
	if _, err := gitOut(ctx, w.Dir, "add", "-A"); err != nil {
		return "", err
	}
	return gitOut(ctx, w.Dir, "diff", "--cached", w.BaseRef)
}

// Commit records all worktree changes on the isolation branch.
func (w *Worktree) Commit(message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if message == "" {
		message = "iCode worktree changes"
	}
	if _, err := gitOut(ctx, w.Dir, "add", "-A"); err != nil {
		return err
	}
	_, err := gitOut(ctx, w.Dir, "commit", "-m", message,
		"--author=iCode <icode@local>", "-q")
	return err
}

// Discard removes the worktree directory and deletes its branch. Safe to
// call twice; uncommitted changes are destroyed by design ("no changes → no
// trace" semantics).
func (w *Worktree) Discard() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var firstErr error
	if _, err := gitOut(ctx, w.RepoDir, "worktree", "remove", "--force", w.Dir); err != nil {
		firstErr = err
		_ = os.RemoveAll(w.Dir) // best-effort fallback
	}
	_, _ = gitOut(ctx, w.RepoDir, "branch", "-D", w.Branch)
	return firstErr
}

// CleanupWorktrees prunes stale iCode worktrees/branches left behind by
// crashed sessions. Called at app bootstrap.
func CleanupWorktrees(repoDir string) {
	top, err := gitOut(context.Background(), repoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = gitOut(ctx, top, "worktree", "prune")
	branches, err := gitOut(ctx, top, "branch", "--list", "icode/wt-*")
	if err != nil || branches == "" {
		return
	}
	for _, b := range strings.Split(branches, "\n") {
		b = strings.TrimPrefix(strings.TrimSpace(b), "* ")
		if b == "" {
			continue
		}
		_, _ = gitOut(ctx, top, "branch", "-D", b)
	}
}
