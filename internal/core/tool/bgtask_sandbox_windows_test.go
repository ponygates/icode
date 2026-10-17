//go:build windows

package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startBackgroundTask drives the real tool entry point (bash with
// run_in_background) and returns the task id. Going through Execute is the
// point: a manager method that works while the tool calls something else would
// prove nothing.
func startBackgroundTask(t *testing.T, command, dir string) string {
	t.Helper()
	args := fmt.Sprintf(`{"command":%q,"cwd":%q,"run_in_background":true}`, command, dir)
	res, err := (&BashTool{}).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("bash run_in_background: %v", err)
	}
	if !res.Success {
		t.Fatalf("background launch failed: %s", res.Error)
	}
	for _, f := range strings.Fields(res.Content) {
		if strings.HasPrefix(f, "bg-") {
			return strings.TrimSuffix(f, ".")
		}
	}
	t.Fatalf("no task id in response: %s", res.Content)
	return ""
}

// awaitTask polls task_output until the task stops running or the budget runs
// out, returning the last rendered status block.
func awaitTask(t *testing.T, id string, budget time.Duration) string {
	t.Helper()
	to := &TaskOutputTool{}
	deadline := time.Now().Add(budget)
	var last string
	for {
		out, err := to.Execute(context.Background(), fmt.Sprintf(`{"task_id":%q}`, id))
		if err != nil {
			t.Fatalf("task_output: %v", err)
		}
		if !out.Success {
			t.Fatalf("task_output failed for %s: %s", id, out.Error)
		}
		last = out.Content
		if !strings.Contains(last, "status=running") {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s still running after %s:\n%s", id, budget, last)
		}
		time.Sleep(80 * time.Millisecond)
	}
}

// TestBackgroundTaskUsesSandbox asserts the process really was created inside
// the boundary. The distinction matters because a background task that falls
// back to os/exec would look identical from the outside until it escapes.
func TestBackgroundTaskUsesSandbox(t *testing.T) {
	installSandbox(t)
	work := t.TempDir()

	id := startBackgroundTask(t, "echo bg-confined", work)
	defer KillAllBgTasks()

	task := bgTasks.Get(id)
	if task == nil {
		t.Fatalf("task %s not registered", id)
	}
	task.mu.Lock()
	confined, direct := task.h != nil, task.cmd != nil
	task.mu.Unlock()
	if !confined {
		t.Fatalf("background task ran through os/exec even though a backend is installed")
	}
	if direct {
		t.Fatalf("task holds both a sandbox handle and an exec.Cmd")
	}

	out := awaitTask(t, id, 20*time.Second)
	if !strings.Contains(out, "status=finished") {
		t.Fatalf("expected a clean finish, got:\n%s", out)
	}
	if !strings.Contains(out, "bg-confined") {
		t.Fatalf("output not streamed into the task buffer:\n%s", out)
	}
}

// TestBackgroundTaskCannotReadOutsideWorkspace is the reason this batch
// exists: run_in_background was the one model-reachable path that outlived the
// agent loop with the user's full privileges.
func TestBackgroundTaskCannotReadOutsideWorkspace(t *testing.T) {
	installSandbox(t)
	home := os.Getenv("USERPROFILE")
	if home == "" {
		t.Skip("USERPROFILE unset")
	}
	probe := filepath.Join(home, fmt.Sprintf("icode-bg-escape-%d.txt", os.Getpid()))
	if err := os.WriteFile(probe, []byte("SECRET-BG-LEAK"), 0o600); err != nil {
		t.Skipf("cannot create probe: %v", err)
	}
	defer os.Remove(probe)

	id := startBackgroundTask(t, "type "+probe, t.TempDir())
	defer KillAllBgTasks()

	out := awaitTask(t, id, 20*time.Second)
	if strings.Contains(out, "SECRET-BG-LEAK") {
		t.Fatalf("background task read a file outside the workspace:\n%s", out)
	}
	if !strings.Contains(out, "status=failed") {
		t.Fatalf("expected the read to fail, got:\n%s", out)
	}
}

// TestBackgroundTaskWritesInsideWorkspace keeps the sandbox honest in the other
// direction — confinement that breaks ordinary work is not usable.
func TestBackgroundTaskWritesInsideWorkspace(t *testing.T) {
	installSandbox(t)
	work := t.TempDir()

	id := startBackgroundTask(t, "echo artifact> built.txt && type built.txt", work)
	defer KillAllBgTasks()

	out := awaitTask(t, id, 20*time.Second)
	if !strings.Contains(out, "status=finished") {
		t.Fatalf("write inside the workspace failed:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(work, "built.txt")); err != nil {
		t.Fatalf("workspace file missing: %v", err)
	}
}

// TestKillStopsConfinedBackgroundTask covers the path that matters for a
// runaway process: task_output with kill=true must actually terminate the
// child the backend created, not just flip a flag.
func TestKillStopsConfinedBackgroundTask(t *testing.T) {
	installSandbox(t)
	work := t.TempDir()

	// A pure cmd.exe busy loop: no network, no external binary, nothing the
	// boundary could legitimately block — it can only stop by being killed.
	id := startBackgroundTask(t, "for /L %i in (1,1,5000000) do @echo x>NUL", work)
	defer KillAllBgTasks()

	time.Sleep(400 * time.Millisecond)

	res, err := (&TaskOutputTool{}).Execute(context.Background(),
		fmt.Sprintf(`{"task_id":%q,"kill":true}`, id))
	if err != nil || !res.Success {
		t.Fatalf("kill request failed: %v / %+v", err, res)
	}

	out := awaitTask(t, id, 15*time.Second)
	if !strings.Contains(out, "status=cancelled") {
		t.Fatalf("expected a cancelled task after kill, got:\n%s", out)
	}
}
