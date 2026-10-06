//go:build !windows

package executil

import (
	"os/exec"
	"syscall"
	"time"
)

// hide is a no-op on non-Windows platforms: those OSes do not allocate a
// console window for child processes the way Windows does.
//
// What POSIX does need is process-group isolation: standard
// exec.CommandContext kills only the direct child when the context expires,
// so `bash -c "sleep 30"` leaves `sleep` orphaned while it still holds the
// stdout pipe — cmd.Wait() then blocks until the orphan exits by itself.
// Placing the child in its own process group (Setpgid) plus a custom Cancel
// that kills the whole group (negative pid) fixes the stall; WaitDelay is
// the belt-and-braces bound for pipes held by anything that escaped the
// group entirely.
func hide(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
}

// fixCmdQuote is a Windows-only concern (cmd.exe's non-standard quoting);
// POSIX shells receive the argv verbatim, so nothing to do here.
func fixCmdQuote(cmd *exec.Cmd, name string, args []string) {}
