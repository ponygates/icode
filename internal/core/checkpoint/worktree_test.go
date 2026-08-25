package checkpoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initRepo creates a real git repo with one commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	return dir
}

func TestWorktreeLifecycle(t *testing.T) {
	repo := initRepo(t)

	wt, err := CreateWorktree(repo)
	if err != nil {
		t.Skipf("git worktree unavailable: %v", err)
	}
	defer wt.Discard() // idempotent safety net

	if _, err := os.Stat(filepath.Join(wt.Dir, "hello.txt")); err != nil {
		t.Fatalf("worktree missing tracked file: %v", err)
	}
	if wt.HasChanges() {
		t.Error("fresh worktree should have no changes")
	}

	// Modify a file → HasChanges + Diff must see it.
	if err := os.WriteFile(filepath.Join(wt.Dir, "hello.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !wt.HasChanges() {
		t.Error("HasChanges = false after edit")
	}
	diff, err := wt.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+changed") || !strings.Contains(diff, "hello.txt") {
		t.Errorf("diff = %q", diff)
	}
	if err := wt.Commit("test change"); err != nil {
		t.Errorf("commit: %v", err)
	}

	dir, branch := wt.Dir, wt.Branch
	if err := wt.Discard(); err != nil && !strings.Contains(err.Error(), "not") {
		t.Logf("discard: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("worktree dir still exists after discard")
	}
	_ = branch
	time.Sleep(10 * time.Millisecond)
	CleanupWorktrees(repo)
}

func TestWorktreeDiscardCleansNoChangeRuns(t *testing.T) {
	repo := initRepo(t)
	wt, err := CreateWorktree(repo)
	if err != nil {
		t.Skipf("git worktree unavailable: %v", err)
	}
	dir := wt.Dir
	if wt.HasChanges() {
		t.Fatal("unexpected changes")
	}
	if err := wt.Discard(); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("dir survived discard")
	}
	// Second Discard must not panic/error hard.
	_ = wt.Discard()
}

func TestCreateWorktreeRejectsNonRepo(t *testing.T) {
	if _, err := CreateWorktree(t.TempDir()); err == nil {
		t.Fatal("expected error outside a git repo")
	}
}
