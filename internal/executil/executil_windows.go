//go:build windows

package executil

import (
	"os/exec"
	"syscall"
)

// hide suppresses the child process's console window on Windows by setting
// the appropriate SysProcAttr fields. It is only compiled on Windows because
// syscall.SysProcAttr.HideWindow / CreationFlags do not exist elsewhere.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
