// Package executil wraps os/exec so that child processes never pop a black
// console (CMD) window when iCode runs in desktop mode.
//
// Background: iCode's desktop binary is linked as a GUI-subsystem app
// (-H windowsgui) so double-clicking it does not allocate a console. However,
// whenever the app spawns an external tool — git, powershell, cmd /C, an MCP
// or LSP server, etc. — a console-subsystem child would still get its own
// console window flashed on screen by Windows. Setting
// SysProcAttr.HideWindow (plus CREATE_NO_WINDOW) suppresses that window so the
// desktop experience stays console-free.
//
// On non-Windows platforms these helpers are identical to the standard
// os/exec constructors.
package executil

import (
	"context"
	"os/exec"
	"runtime"
	"syscall"
)

// CommandContext is like exec.CommandContext but hides the child's console
// window on Windows.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hide(cmd)
	return cmd
}

// Command is like exec.Command but hides the child's console window on Windows.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	hide(cmd)
	return cmd
}

func hide(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		}
	}
}
