package tool

// Background task support — Claude Code / opencode parity.
//
// bash accepts run_in_background=true and immediately returns a task id;
// the process keeps running detached from the agent loop. The model can
// then poll `task_output` to fetch accumulated stdout/stderr, check status,
// or kill the task.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/types"
	"github.com/ponygates/icode/internal/xgo"
)

// bgTask is one background shell process.
type bgTask struct {
	id      string
	command string
	start   time.Time

	mu     sync.Mutex
	buf    bytes.Buffer
	done   bool
	errMsg string
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

func (t *bgTask) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Cap the buffer at 1 MiB to bound memory; keep the tail.
	const maxBuf = 1 << 20
	if t.buf.Len()+len(p) > maxBuf {
		data := t.buf.Bytes()
		keep := maxBuf / 2
		if len(data) > keep {
			trimmed := make([]byte, keep)
			copy(trimmed, data[len(data)-keep:])
			t.buf.Reset()
			t.buf.WriteString("[...earlier output trimmed...]\n")
			t.buf.Write(trimmed)
		}
	}
	return t.buf.Write(p)
}

func (t *bgTask) snapshot() (output string, done bool, errMsg string, elapsed time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String(), t.done, t.errMsg, time.Since(t.start)
}

// bgTaskManager tracks all background tasks for the process lifetime.
type bgTaskManager struct {
	mu    sync.Mutex
	seq   int
	tasks map[string]*bgTask
}

var bgTasks = &bgTaskManager{tasks: map[string]*bgTask{}}

func KillAllBgTasks() {
	bgTasks.KillAll()
}

// Start launches cmdStr in the background and returns its task id.
func (m *bgTaskManager) Start(cmdStr, workDir string) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())

	var cmd *exec.Cmd
	if strings.Contains(os.Getenv("OS"), "Windows") {
		cmd = executil.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = executil.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	if workDir != "" {
		if absDir, err := filepath.Abs(workDir); err == nil {
			if info, err := os.Stat(absDir); err == nil && info.IsDir() {
				cmd.Dir = absDir
			}
		}
	}

	m.mu.Lock()
	m.seq++
	id := fmt.Sprintf("bg-%d", m.seq)
	task := &bgTask{id: id, command: cmdStr, start: time.Now(), cancel: cancel, cmd: cmd}
	m.tasks[id] = task
	m.mu.Unlock()

	cmd.Stdout = task
	cmd.Stderr = task

	if err := cmd.Start(); err != nil {
		cancel()
		m.mu.Lock()
		delete(m.tasks, id)
		m.mu.Unlock()
		return "", err
	}

	xgo.GoSafe("bgtask.wait", func() {
		err := cmd.Wait()
		task.mu.Lock()
		task.done = true
		if err != nil {
			task.errMsg = err.Error()
		}
		task.mu.Unlock()
		cancel()
	})

	return id, nil
}

// Get returns the task with the given id, or nil.
func (m *bgTaskManager) Get(id string) *bgTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
}

// List returns a status line for every known task, newest first.
func (m *bgTaskManager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.tasks))
	for _, t := range m.tasks {
		_, done, errMsg, elapsed := t.snapshot()
		status := "running"
		if done {
			status = "finished"
			if errMsg != "" {
				status = "failed"
			}
		}
		out = append(out, fmt.Sprintf("%s  [%s, %s]  %s", t.id, status, elapsed.Round(time.Second), truncN(t.command, 80)))
	}
	return out
}

func (m *bgTaskManager) KillAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		t.mu.Lock()
		if !t.done && t.cmd != nil && t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		t.mu.Unlock()
		t.cancel()
	}
}

// ============================================================================
// TaskOutputTool — poll background task output / status, optionally kill.
// ============================================================================

type TaskOutputTool struct{}

func (t *TaskOutputTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "task_output",
		Description: "Get the output and status of a background task started by bash with run_in_background=true. Omit task_id to list all background tasks. Set kill=true to terminate the task.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The background task id (e.g. bg-1). Omit to list all tasks.",
				},
				"kill": map[string]any{
					"type":        "boolean",
					"description": "Terminate the task before returning its output.",
				},
			},
		},
	}
}

func (t *TaskOutputTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	id := parseStrArg(args, "task_id", "")
	if id == "" {
		lines := bgTasks.List()
		if len(lines) == 0 {
			return &types.ToolResult{Success: true, Content: "No background tasks."}, nil
		}
		return &types.ToolResult{Success: true, Content: "Background tasks:\n" + strings.Join(lines, "\n")}, nil
	}
	task := bgTasks.Get(id)
	if task == nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("no such task: %s", id)}, nil
	}
	if parseBoolArgWithDefault(args, "kill", false) {
		task.cancel()
	}
	output, done, errMsg, elapsed := task.snapshot()
	status := "running"
	if done {
		status = "finished"
		if errMsg != "" {
			status = "failed (" + errMsg + ")"
		}
	}
	head := fmt.Sprintf("[%s] status=%s elapsed=%s command=%s\n---\n", id, status, elapsed.Round(time.Second), truncN(task.command, 120))
	return &types.ToolResult{Success: true, Content: head + output}, nil
}

// truncN returns at most n bytes of s, appending an ellipsis when trimmed.
func truncN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
