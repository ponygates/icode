//go:build windows

package executil

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// hide suppresses the child process's console window on Windows by setting
// the appropriate SysProcAttr fields. It is only compiled on Windows because
// syscall.SysProcAttr.HideWindow / CreationFlags do not exist elsewhere.
//
// It also makes context cancellation kill the WHOLE process tree. Windows
// specifics: a `cmd /C long-task` child spawns grandchildren (node, go build,
// powershell, …) that INHERIT the stdout/stderr pipe handles. Context
// cancellation only kills cmd.exe itself, but exec.Cmd.Wait waits for the
// pipes to hit EOF — the still-running grandchildren keep them open, so Wait
// blocks until the grandchildren exit on their own. Symptom in the TUI: Esc
// prints「⏹ 正在中断…」but the tool call (and the whole turn) never unwinds.
// Fix on both layers: Cancel runs `taskkill /T /F` (kills the tree, pipes
// close, EOF fires), and WaitDelay bounds Wait even if a grandchild somehow
// survives the kill.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		tk := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		tk.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: 0x08000000,
		}
		if err := tk.Run(); err != nil {
			// taskkill can lose a race with an already-exiting process —
			// fall back to killing the direct child only.
			_ = cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
}

// fixCmdQuote rewrites `cmd /C <line>` invocations to pass the RAW command
// line via SysProcAttr.CmdLine. cmd.exe does NOT follow the
// CommandLineToArgvW quoting rules Go's EscapeArg uses: `type "a b.txt"`
// gets re-encoded as `"type \"a b.txt\""` and cmd then treats the whole
// mangled string as one garbage filename — "The filename, directory name, or
// volume label syntax is incorrect", exit 1. Every command with embedded
// quotes (paths with spaces, nested quoting from the model) fails this way
// on machines without Git Bash, where the bash tool falls back to cmd.
// With /S, cmd strips exactly the first and last quote of the /C argument,
// so the line between them reaches cmd verbatim.
func fixCmdQuote(cmd *exec.Cmd, name string, args []string) {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(name)), ".exe")
	if base != "cmd" || len(args) < 2 || !strings.EqualFold(args[0], "/C") {
		return
	}
	line := strings.Join(args[1:], " ")
	cmd.SysProcAttr.CmdLine = `"` + name + `" /S /C "` + line + `"`
}
