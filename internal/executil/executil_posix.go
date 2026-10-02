//go:build !windows

package executil

import "os/exec"

// hide is a no-op on non-Windows platforms: those OSes do not allocate a
// console window for child processes the way Windows does, so the standard
// os/exec behaviour is already correct.
func hide(cmd *exec.Cmd) {}

// fixCmdQuote is a Windows-only concern (cmd.exe's non-standard quoting);
// POSIX shells receive the argv verbatim, so nothing to do here.
func fixCmdQuote(cmd *exec.Cmd, name string, args []string) {}
