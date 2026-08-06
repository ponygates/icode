//go:build !windows

package tui

import (
	"bytes"
	"os/exec"
	"runtime"
)

// readClipboard returns the current clipboard text. On macOS it uses pbpaste;
// on Linux it uses xclip (assumed installed). Only UTF-8/plain text is ever
// read; binary formats are returned as-is by the shell tool.
func readClipboard() (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbpaste")
	default:
		cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return buf.String(), nil
}