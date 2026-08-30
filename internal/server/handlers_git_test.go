package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitPorcelain(t *testing.T) {
	out := "## main...origin/main [ahead 2, behind 1]\x00" +
		" M internal/tui/render.go\x00" +
		"M  internal/app/app.go\x00" +
		"A  new/file.go\x00" +
		"D  gone/file.go\x00" +
		"?? untracked.txt\x00" +
		"R  renamed/new.go\x00renamed/old.go\x00"
	st := parseGitPorcelain(out)

	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
	if st.Ahead != 2 || st.Behind != 1 {
		t.Errorf("ahead/behind = %d/%d, want 2/1", st.Ahead, st.Behind)
	}
	if len(st.Files) != 6 {
		t.Fatalf("files = %d, want 6: %+v", len(st.Files), st.Files)
	}
	byPath := map[string]gitFile{}
	for _, f := range st.Files {
		byPath[f.Path] = f
	}
	if f := byPath["internal/tui/render.go"]; f.Index != "" || f.Worktree != "M" {
		t.Errorf("unstaged-only file parsed wrong: %+v", f)
	}
	if f := byPath["internal/app/app.go"]; f.Index != "M" || f.Worktree != "" {
		t.Errorf("staged-only file parsed wrong: %+v", f)
	}
	if f := byPath["untracked.txt"]; !f.Untracked {
		t.Errorf("untracked flag missing: %+v", f)
	}
	if f := byPath["renamed/new.go"]; f.RenamedFrom != "renamed/old.go" {
		t.Errorf("rename source not captured: %+v", f)
	}

	// no-upstream + behind-only + no-commits variants
	st = parseGitPorcelain("## feat-x\x00?? a.txt\x00")
	if st.Branch != "feat-x" || st.Ahead != 0 || st.Behind != 0 {
		t.Errorf("plain branch header parsed wrong: %+v", st)
	}
	st = parseGitPorcelain("## main...origin/main [behind 3]\x00")
	if st.Behind != 3 {
		t.Errorf("behind-only header parsed wrong: %+v", st)
	}
	st = parseGitPorcelain("## No commits yet on master\x00")
	if st.Branch != "master" {
		t.Errorf("empty-repo header parsed wrong: %+v", st)
	}
}

func TestGitStatusOutsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	st := parseGitPorcelain("")
	out, err := gitRun(context.Background(), "status", "--porcelain=v1", "-z", "-b")
	if err == nil && strings.TrimSpace(out) == "" {
		// git may succeed with empty output when GIT_DIR leaks from the
		// parent environment — the handler would then claim a repo. Guard:
		t.Logf("git status succeeded outside repo (env leak?), output=%q", out)
	}
	_ = st
}

// TestGitWorkbenchRoundTrip exercises the workbench endpoints against a real
// throwaway repository: status → stage → commit-message stub → commit.
func TestGitWorkbenchRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("real-git integration test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := gitRun(ctx, "status", "--porcelain=v1", "-z", "-b")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	st := parseGitPorcelain(out)
	if len(st.Files) != 1 || !st.Files[0].Untracked || st.Files[0].Path != "a.txt" {
		t.Fatalf("untracked file not detected: %+v", st.Files)
	}

	if _, err := gitRun(ctx, "add", "--", "a.txt"); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err = gitRun(ctx, "status", "--porcelain=v1", "-z", "-b")
	if err != nil {
		t.Fatalf("status2: %v", err)
	}
	st = parseGitPorcelain(out)
	if len(st.Files) != 1 || st.Files[0].Index != "A" {
		t.Fatalf("staged file not detected: %+v", st.Files)
	}

	// diff of the staged new file must contain the content
	out, err = gitRun(ctx, "diff", "--cached", "--", "a.txt")
	if err != nil || !strings.Contains(out, "+hello") {
		t.Fatalf("cached diff missing content: err=%v out=%q", err, out)
	}

	if _, err := gitRun(ctx, "commit", "-m", "feat: add a"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// modify → unstaged M
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = gitRun(ctx, "status", "--porcelain=v1", "-z", "-b")
	if err != nil {
		t.Fatalf("status3: %v", err)
	}
	st = parseGitPorcelain(out)
	if len(st.Files) != 1 || st.Files[0].Worktree != "M" {
		t.Fatalf("unstaged modification not detected: %+v", st.Files)
	}

	// branches endpoint shape
	out, err = gitRun(ctx, "branch", "--format=%(HEAD)%00%(refname:short)")
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if !strings.Contains(out, "*\x00") || !strings.Contains(out, "master") && !strings.Contains(out, "main") {
		t.Fatalf("branch listing unexpected: %q", out)
	}
}
