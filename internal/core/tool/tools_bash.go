package tool

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/types"
)

// ============================================================================
// BashTool — execute shell commands
// ============================================================================

type BashTool struct{}

// Bash timeout / output-cap policy. bashDefaultTimeoutSec keeps the
// historically hardcoded 120s; a per-call "timeout" argument (seconds) may
// override it up to bashMaxTimeoutSec. bashOutputCapChars is the TOOL-side
// hard ceiling applied before the engine's Level-4 budget enforcer (30K for
// bash) sees the result, so `cat` of a huge file cannot balloon the tool
// result in the first place; the cut reuses tokenopt's head+tail elision
// helper ("[... N chars omitted ...]").
const (
	bashDefaultTimeoutSec = 120
	bashMaxTimeoutSec     = 600
	bashOutputCapChars    = 200_000
)

// resolveBashTimeoutSec extracts the optional "timeout" (seconds) argument.
// Missing/invalid/non-positive values keep the default; anything above the
// cap is clamped to it.
func resolveBashTimeoutSec(args string) int {
	sec := bashDefaultTimeoutSec
	if s, err := parseArg(args, "timeout"); err == nil {
		if v, cerr := strconv.Atoi(strings.TrimSpace(s)); cerr == nil && v > 0 {
			sec = v
		}
	}
	if sec > bashMaxTimeoutSec {
		sec = bashMaxTimeoutSec
	}
	return sec
}

// capBashOutput applies the tool-side head+tail output ceiling.
func capBashOutput(s string) string {
	return tokenopt.CompressToolResult(s, bashOutputCapChars)
}

// resolveBashShell picks the shell for a command. On Windows the model
// almost always emits POSIX sh syntax, so — like the industry does — prefer
// $SHELL when it points at a real shell binary, then probe for Git Bash
// (WSL is deliberately out of scope: it adds path-translation failures),
// and only then fall back to cmd. Non-Windows keeps the historic "sh -c".
// The choice is internal plumbing; it is not surfaced in tool output (the
// file has no metadata convention for shell selection).
func resolveBashShell() (string, []string) {
	if runtime.GOOS != "windows" {
		return "sh", []string{"-c"}
	}
	return pickWindowsShell(os.Getenv, shellBinaryExists)
}

func shellBinaryExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// pickWindowsShell is split out (env/stat injectable) so tests can cover
// every branch without a real Git installation.
func pickWindowsShell(getenv func(string) string, exists func(string) bool) (string, []string) {
	if sh := getenv("SHELL"); sh != "" && exists(sh) {
		return sh, []string{"-c"}
	}
	for _, p := range gitBashCandidates(getenv) {
		if exists(p) {
			return p, []string{"-c"}
		}
	}
	return "cmd", []string{"/C"}
}

func gitBashCandidates(getenv func(string) string) []string {
	var out []string
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if root := getenv(key); root != "" {
			out = append(out, filepath.Join(root, "Git", "bin", "bash.exe"))
		}
	}
	out = append(out, `C:\Program Files\Git\bin\bash.exe`)
	return out
}

func (t *BashTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "bash",
		Description: "Execute a shell command and return its output. The command runs in the project directory by default. Use 'cwd' to change the working directory for the command.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for the command. Defaults to project root. Accepts absolute paths like C:\\Users\\... or relative paths.",
				},
				"run_in_background": map[string]any{
					"type":        "boolean",
					"description": "Run the command in the background and return a task id immediately. Use the task_output tool to poll its output/status later. Use for long-running commands (builds, servers, installs).",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Optional command timeout in seconds. Defaults to 120, capped at 600.",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	cmdStr, err := parseArg(args, "command")
	if err != nil {
		return nil, err
	}
	workDir, _ := parseArg(args, "cwd")

	// Background mode: start detached, return the task id immediately.
	if parseBoolArgWithDefault(args, "run_in_background", false) {
		id, err := bgTasks.Start(cmdStr, workDir)
		if err != nil {
			return &types.ToolResult{Success: false, Error: "failed to start background task: " + err.Error()}, nil
		}
		return &types.ToolResult{
			Success: true,
			Content: fmt.Sprintf("Started background task %s. Use task_output with task_id=%q to check its output and status.", id, id),
		}, nil
	}

	timeoutSec := resolveBashTimeoutSec(args)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	shellName, shellArgs := resolveBashShell()
	cmd = executil.CommandContext(ctx, shellName, append(shellArgs, cmdStr)...)

	// Apply working directory
	if workDir != "" {
		if absDir, err := filepath.Abs(workDir); err == nil {
			if info, err := os.Stat(absDir); err == nil && info.IsDir() {
				cmd.Dir = absDir
			}
		}
	}

	// Live streaming: when the engine attached a progress callback (TUI /
	// desktop), forward stdout/stderr incrementally so the user sees output
	// as the command runs instead of one dump at the end. The full output is
	// still accumulated and returned for the model. cmd.Wait drains the
	// exec-managed pipes into these writers before returning, so the
	// accumulated content is complete when Wait returns.
	progress := ProgressFromContext(ctx)
	if progress != nil {
		type progressWriter struct {
			mu  sync.Mutex
			buf strings.Builder
		}
		newPW := func() *progressWriter {
			w := &progressWriter{}
			return w
		}
		attach := func(w *progressWriter) io.Writer {
			return writerFunc(func(p []byte) (int, error) {
				w.mu.Lock()
				w.buf.Write(p)
				w.mu.Unlock()
				progress(string(p))
				return len(p), nil
			})
		}
		stdoutPW := newPW()
		stderrPW := newPW()
		cmd.Stdout = attach(stdoutPW)
		cmd.Stderr = attach(stderrPW)
		if err := cmd.Start(); err != nil {
			return &types.ToolResult{Success: false, Error: err.Error()}, nil
		}
		waitErr := cmd.Wait()
		var content strings.Builder
		stdoutPW.mu.Lock()
		content.WriteString(stdoutPW.buf.String())
		stdoutPW.mu.Unlock()
		stderrPW.mu.Lock()
		content.WriteString(stderrPW.buf.String())
		stderrPW.mu.Unlock()
		if waitErr != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return &types.ToolResult{Success: false, Content: capBashOutput(content.String()), Error: fmt.Sprintf("command timed out after %d seconds", timeoutSec)}, nil
			}
			return &types.ToolResult{Success: false, Content: capBashOutput(content.String()), Error: waitErr.Error()}, nil
		}
		return &types.ToolResult{Success: true, Content: capBashOutput(content.String())}, nil
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &types.ToolResult{
				Success: false,
				Content: capBashOutput(string(output)),
				Error:   fmt.Sprintf("command timed out after %d seconds", timeoutSec),
			}, nil
		}
		return &types.ToolResult{
			Success: false,
			Content: capBashOutput(string(output)),
			Error:   err.Error(),
		}, nil
	}

	return &types.ToolResult{Success: true, Content: capBashOutput(string(output))}, nil
}
