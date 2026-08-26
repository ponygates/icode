package tool

// Background sub-agents — Claude Code parity for the "agents run in the
// background by default" model. The task tool accepts background=true and
// returns an agt-N handle immediately; the sub-agent keeps running detached
// from the main loop, and the model polls task_output / lists all tasks to
// collect results. Completion fires the same toast + host-hook path as
// background shell commands.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/notify"
	"github.com/ponygates/icode/internal/xgo"
)

// agentBgTask is one background sub-agent run.
type agentBgTask struct {
	id     string
	name   string
	brief  string // truncated prompt for listings
	start  time.Time
	cancel context.CancelFunc

	mu      sync.Mutex
	output  string
	tokens  int
	done    bool
	errMsg  string
	elapsed time.Duration
}

func (t *agentBgTask) finish(output string, tokens int, errMsg string) {
	t.mu.Lock()
	t.output = output
	t.tokens = tokens
	t.done = true
	t.errMsg = errMsg
	t.elapsed = time.Since(t.start)
	t.mu.Unlock()
}

func (t *agentBgTask) snapshot() (output string, tokens int, done bool, errMsg string, elapsed time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.done {
		elapsed = time.Since(t.start)
	} else {
		elapsed = t.elapsed
	}
	return t.output, t.tokens, t.done, t.errMsg, elapsed
}

// agentBgManager tracks background sub-agent runs for the process lifetime.
// Completed runs are retained (capped) so late task_output polls still work.
type agentBgManager struct {
	mu    sync.Mutex
	seq   int
	tasks map[string]*agentBgTask

	completeHook func(id, errMsg string)
}

var agentBGTasks = &agentBgManager{tasks: map[string]*agentBgTask{}}

const agentTaskRetain = 50 // keep at most this many finished tasks

// SetAgentTaskCompleteHook installs a callback invoked when a background
// sub-agent finishes (same contract as the shell-task hook).
func SetAgentTaskCompleteHook(fn func(id, errMsg string)) {
	agentBGTasks.mu.Lock()
	agentBGTasks.completeHook = fn
	agentBGTasks.mu.Unlock()
}

// KillAllAgentTasks cancels every running background sub-agent (app shutdown).
func KillAllAgentTasks() {
	agentBGTasks.mu.Lock()
	defer agentBGTasks.mu.Unlock()
	for _, t := range agentBGTasks.tasks {
		t.cancel()
	}
}

func (m *agentBgManager) launch(runner SubAgentRunner, name, prompt string) (string, error) {
	return m.launchCtx(context.Background(), runner, name, prompt)
}

func (m *agentBgManager) launchCtx(parent context.Context, runner SubAgentRunner, name, prompt string) (string, error) {
	ctx, cancel := context.WithCancel(parent)

	m.mu.Lock()
	m.seq++
	id := fmt.Sprintf("agt-%d", m.seq)
	task := &agentBgTask{id: id, name: name, brief: truncN(prompt, 80), start: time.Now(), cancel: cancel}
	m.tasks[id] = task
	m.mu.Unlock()

	xgo.GoSafe("agentbg.run", func() {
		result, tokens, err := runner.RunSubAgent(ctx, name, prompt)
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
			if result == "" {
				result = ""
			}
		}
		task.finish(result, tokens, errMsg)
		cancel() // release resources either way

		status := "子代理任务完成: " + id + " (" + name + ")"
		if errMsg != "" {
			status = "子代理任务失败: " + id + " (" + name + ")"
		}
		notify.Notify("iCode 后台代理", status)

		m.mu.Lock()
		hook := m.completeHook
		m.mu.Unlock()
		if hook != nil {
			xgo.GoSafe("agentbg.notify", func() { hook(id, errMsg) })
		}

		// Retention cap: drop oldest finished tasks beyond the limit.
		m.mu.Lock()
		var finished []string
		for tid, tt := range m.tasks {
			tt.mu.Lock()
			isDone := tt.done
			tt.mu.Unlock()
			if isDone {
				finished = append(finished, tid)
			}
		}
		if len(finished) > agentTaskRetain {
			// IDs are monotonic; sort ascending and drop from the front.
			sortStrings(finished)
			for _, old := range finished[:len(finished)-agentTaskRetain] {
				tt := m.tasks[old]
				tt.mu.Lock()
				doneNow := tt.done
				tt.mu.Unlock()
				if doneNow && tt.id == old {
					delete(m.tasks, old)
				}
			}
		}
		m.mu.Unlock()
	})

	return id, nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (m *agentBgManager) get(id string) *agentBgTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
}

// list renders one status line per task, running ones first then newest.
func (m *agentBgManager) list() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.tasks))
	var running, finished []string
	for _, t := range m.tasks {
		t.mu.Lock()
		done := t.done
		t.mu.Unlock()
		if done {
			finished = append(finished, t.id)
		} else {
			running = append(running, t.id)
		}
	}
	sortStrings(running)
	sortStrings(finished)
	render := func(ids []string) {
		for _, id := range ids {
			t := m.tasks[id]
			output, _, done, errMsg, elapsed := t.snapshot()
			status := "running"
			if done {
				status = "finished"
				if errMsg != "" {
					status = "failed (" + errMsg + ")"
				}
			}
			line := fmt.Sprintf("%s  [%s, %s]  %s: %s", t.id, status, elapsed.Round(time.Second), t.name, t.brief)
			if done && output != "" {
				line += fmt.Sprintf("  (%d tok)", t.tokens)
			}
			out = append(out, line)
		}
	}
	render(running)
	render(finished)
	return out
}

// launchBackgroundAgent starts a detached sub-agent run via the manager.
func launchBackgroundAgent(runner SubAgentRunner, name, prompt string) (string, error) {
	return agentBGTasks.launch(runner, name, prompt)
}

// launchBackgroundAgentCtx is launchBackgroundAgent carrying a caller-supplied
// context (spawn-depth tracking survives the detach).
func launchBackgroundAgentCtx(ctx context.Context, runner SubAgentRunner, name, prompt string) (string, error) {
	return agentBGTasks.launchCtx(ctx, runner, name, prompt)
}

// ListAgentTaskLines renders one status line per background sub-agent run.
// Exported for the /tasks slash panel.
func ListAgentTaskLines() []string { return agentBGTasks.list() }

// RunningAgentTaskCount reports how many detached sub-agent runs are active.
func RunningAgentTaskCount() int { return agentBGTasks.runningCount() }

func (m *agentBgManager) runningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.tasks {
		t.mu.Lock()
		done := t.done
		t.mu.Unlock()
		if !done {
			n++
		}
	}
	return n
}

// funcAdapter turns a run function into a SubAgentRunner so background
// launches can carry fork behaviour.
type funcAdapter func(ctx context.Context, name, prompt string) (string, int, error)

func (f funcAdapter) RunSubAgent(ctx context.Context, name, prompt string) (string, int, error) {
	return f(ctx, name, prompt)
}
