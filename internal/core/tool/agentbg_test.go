package tool

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner simulates the engine's SubAgentRunner.
type fakeRunner struct {
	mu       sync.Mutex
	delay    time.Duration
	failWith error
	calls    []string
	canceled int
}

func (f *fakeRunner) RunSubAgent(ctx context.Context, name, prompt string) (string, int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	select {
	case <-time.After(f.delay):
		if f.failWith != nil {
			return "", 0, f.failWith
		}
		return "done: " + prompt, 42, nil
	case <-ctx.Done():
		f.mu.Lock()
		f.canceled++
		f.mu.Unlock()
		return "", 0, ctx.Err()
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestBackgroundAgentLifecycle(t *testing.T) {
	fr := &fakeRunner{delay: 30 * time.Millisecond}
	id, err := launchBackgroundAgent(fr, "explore", "find permission code")
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.HasPrefix(id, "agt-") {
		t.Fatalf("id = %q, want agt-N", id)
	}

	task := agentBGTasks.get(id)
	if task == nil {
		t.Fatal("task not registered")
	}
	// Immediately after launch it must be running with a cancel handle.
	_, _, done, _, _ := task.snapshot()
	if done {
		t.Fatal("task finished too fast")
	}

	waitFor(t, 3*time.Second, func() bool {
		_, _, d, _, _ := task.snapshot()
		return d
	})
	output, tokens, done, errMsg, _ := task.snapshot()
	if !done || errMsg != "" || tokens != 42 || output != "done: find permission code" {
		t.Errorf("snapshot = out:%q tok:%d done:%v err:%q", output, tokens, done, errMsg)
	}

	// Listing includes the finished task with token info.
	list := agentBGTasks.list()
	if len(list) == 0 || !strings.Contains(strings.Join(list, "\n"), id) {
		t.Errorf("list missing %s: %v", id, list)
	}
}

func TestBackgroundAgentFailureCaptured(t *testing.T) {
	fr := &fakeRunner{delay: 5 * time.Millisecond, failWith: errors.New("boom")}
	id, _ := launchBackgroundAgent(fr, "plan", "break things")
	task := agentBGTasks.get(id)
	waitFor(t, 2*time.Second, func() bool {
		_, _, d, _, _ := task.snapshot()
		return d
	})
	_, _, _, errMsg, _ := task.snapshot()
	if errMsg == "" || !strings.Contains(errMsg, "boom") {
		t.Errorf("errMsg = %q, want boom", errMsg)
	}
}

func TestTaskOutputToolListsAgentTasks(t *testing.T) {
	fr := &fakeRunner{delay: 1 * time.Millisecond}
	id, _ := launchBackgroundAgent(fr, "general", "quick job")
	task := agentBGTasks.get(id)
	waitFor(t, 2*time.Second, func() bool {
		_, _, d, _, _ := task.snapshot()
		return d
	})

	out := &TaskOutputTool{}
	res, err := out.Execute(context.Background(), `{}`)
	if err != nil || !res.Success {
		t.Fatalf("Execute: %v / %+v", err, res)
	}
	if !strings.Contains(res.Content, id) || !strings.Contains(res.Content, "general") {
		t.Errorf("listing missing agent task: %s", res.Content)
	}

	// Direct lookup by id returns status + output.
	res2, _ := out.Execute(context.Background(), `{"task_id":"`+id+`"}`)
	if res2 == nil || !strings.Contains(res2.Content, "finished") || !strings.Contains(res2.Content, "quick job") {
		t.Errorf("lookup content = %+v", res2)
	}

	// Unknown agt- id reports a clean miss.
	res3, _ := out.Execute(context.Background(), `{"task_id":"agt-99999"}`)
	if res3.Success || !strings.Contains(res3.Error, "no such task") {
		t.Errorf("missing id result = %+v", res3)
	}
}

func TestKillAllAgentTasks(t *testing.T) {
	slow := &fakeRunner{delay: 5 * time.Second}
	id, _ := launchBackgroundAgent(slow, "explore", "long running")
	KillAllAgentTasks()
	task := agentBGTasks.get(id)
	waitFor(t, 2*time.Second, func() bool {
		slow.mu.Lock()
		defer slow.mu.Unlock()
		return slow.canceled > 0
	})
	if task == nil {
		t.Fatal("task vanished after kill")
	}
}
