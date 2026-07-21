// Package checkpoint provides shadow-git rewrite checkpoint system.
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

type Store struct {
	mu        sync.Mutex
	sessionID string
	root      string
	gitDir    string
	workDir   string
}

func Open(sessionID string) (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("checkpoint: %w", err)
	}
	root := filepath.Join(home, ".icode", "checkpoints", sanitize(sessionID))
	gitDir := filepath.Join(root, ".git")
	workDir := root
	s := &Store{sessionID: sessionID, root: root, gitDir: gitDir, workDir: workDir}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("checkpoint mkdir: %w", err)
	}
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		if _, e := s.gitCmd(context.Background(), "init"); e != nil {
			return nil, fmt.Errorf("checkpoint init: %w", e)
		}
		_, _ = s.gitCmd(context.Background(), "config", "user.name", "iCode")
		_, _ = s.gitCmd(context.Background(), "config", "user.email", "icode@local")
	}
	return s, nil
}

func (s *Store) Snapshot(ctx context.Context, msg string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.gitCmd(ctx, "add", "-A"); err != nil {
		return "", fmt.Errorf("checkpoint add: %w", err)
	}
	_, err := s.gitCmd(ctx, "diff", "--cached", "--quiet")
	if err == nil {
		return "", nil
	}
	out, err := s.gitCmd(ctx, "commit", "--allow-empty", "-m", msg)
	if err != nil {
		return "", fmt.Errorf("checkpoint commit: %w\n%s", err, out)
	}
	hash, _ := s.gitCmd(ctx, "rev-parse", "HEAD")
	return strings.TrimSpace(hash), nil
}

type Entry struct {
	Hash    string
	Message string
	When    time.Time
}

func (s *Store) List(ctx context.Context) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.gitCmd(ctx, "log", "--oneline", "--format=%H|%ct|%s")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var entries []Entry
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		ts := parseUnix(parts[1])
		entries = append(entries, Entry{Hash: parts[0], When: time.Unix(ts, 0), Message: parts[2]})
	}
	return entries, nil
}

func (s *Store) Rewind(ctx context.Context, steps int) ([]string, error) {
	if steps <= 0 {
		return nil, fmt.Errorf("rewind steps must be positive, got %d", steps)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	countOut, err := s.gitCmd(ctx, "rev-list", "--count", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("checkpoint count: %w", err)
	}
	totalN := 0
	fmt.Sscanf(strings.TrimSpace(countOut), "%d", &totalN)
	if steps >= totalN {
		steps = totalN - 1
	}
	if steps <= 0 {
		return nil, fmt.Errorf("not enough checkpoints to rewind")
	}
	filesOut, err := s.gitCmd(ctx, "diff", "--name-only", fmt.Sprintf("HEAD~%d", steps), "HEAD")
	if err != nil {
		return nil, fmt.Errorf("checkpoint diff: %w", err)
	}
	restored := strings.Fields(filesOut)
	if _, err := s.gitCmd(ctx, "reset", "--soft", fmt.Sprintf("HEAD~%d", steps)); err != nil {
		return nil, fmt.Errorf("checkpoint reset: %w", err)
	}
	if _, err := s.gitCmd(ctx, "checkout", "--", "."); err != nil {
		return nil, fmt.Errorf("checkpoint checkout: %w", err)
	}
	return restored, nil
}

func (s *Store) Status(ctx context.Context) string {
	entries, err := s.List(ctx)
	if err != nil || len(entries) == 0 {
		return "没有检查点。模型修改文件后自动创建。"
	}
	recent := 0
	for _, e := range entries {
		if time.Since(e.When) < 5*time.Minute {
			recent++
		}
	}
	return fmt.Sprintf("%d 个检查点 (%d 个最近五分钟) — /rewind N 回滚", len(entries), recent)
}

func (s *Store) gitCmd(ctx context.Context, args ...string) (string, error) {
	cmdArgs := append([]string{"--git-dir", s.gitDir, "--work-tree", s.workDir}, args...)
	cmd := executil.CommandContext(ctx, "git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func sanitize(id string) string {
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, "\\", "_")
	id = strings.ReplaceAll(id, "..", "_")
	return id
}

func parseUnix(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}

var DefaultStores sync.Map

func GetOrOpen(sessionID string) (*Store, error) {
	if v, ok := DefaultStores.Load(sessionID); ok {
		return v.(*Store), nil
	}
	s, err := Open(sessionID)
	if err != nil {
		return nil, err
	}
	DefaultStores.Store(sessionID, s)
	return s, nil
}
