package tui

import (
	"context"
	"fmt"
	"runtime"

	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/sandbox"
)

// shellArgv builds the argv used to run a command string. Both the confined
// and unconfined paths share it so they cannot drift apart.
func shellArgv(cmdStr string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", cmdStr}
	}
	return []string{"sh", "-c", cmdStr}
}

// runUserShell executes a command the user typed after "!".
//
// This is the third of the three entry points that reach a shell, after the
// bash tool and background tasks. It is deliberately treated differently:
// these commands are keyed in by the person who already owns the machine, so
// confining them is opt-in (sandbox.confine_user_shell). The default keeps
// `! git push` and `! code .` working, which confinement would break — no
// network, no access outside the project.
//
// When the setting is on, the command gets exactly the boundary a model-issued
// command gets: no wider, no narrower. A start failure is returned rather than
// retried unconfined, for the same reason as elsewhere — a boundary that
// quietly steps aside is not a boundary.
//
// dir is the working directory, passed in rather than read here so a caller
// that has already resolved it (and tests, which must not grant access to the
// source tree) control it.
func runUserShell(ctx context.Context, dir, cmdStr string) (string, error) {
	argv := shellArgv(cmdStr)
	runner := sandbox.Active()
	if runner == nil || !sandbox.ConfineUserShell() {
		out, err := executil.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}

	if dir == "" {
		// An empty Dir would make the backend inherit the parent's working
		// directory, which the container token has no rights to.
		return "", fmt.Errorf("sandbox: 无法确定当前目录")
	}
	h, err := runner.Start(ctx, argv, sandbox.Spec{Dir: dir})
	if err != nil {
		return "", fmt.Errorf("沙箱拒绝执行该命令: %w", err)
	}
	res, waitErr := h.Wait()
	if waitErr != nil {
		return string(res.Output), waitErr
	}
	if res.ExitCode != 0 {
		// Same text os/exec produces, so callers that print err.Error() do
		// not need to know which path ran.
		return string(res.Output), fmt.Errorf("exit status %d", res.ExitCode)
	}
	return string(res.Output), nil
}

// userShellNotice explains the gap when the sandbox is on but "!" is not. It
// is shown once per session: repeating it before every command would train the
// user to ignore it.
func (t *TUI) userShellNotice() {
	t.mu.Lock()
	shown := t.noticedShellScope
	t.noticedShellScope = true
	t.mu.Unlock()
	if shown {
		return
	}
	if sandbox.Active() == nil || sandbox.ConfineUserShell() {
		return
	}
	t.add(RoleSystem, "ⓘ 沙箱已启用，但 ! 命令以本机权限运行（你自己的命令与模型发起的命令不同）。\n   如需一并隔离：设置 sandbox.confine_user_shell: true，或在桌面端「安全沙箱」里打开「用户命令同样受限」。")
}
