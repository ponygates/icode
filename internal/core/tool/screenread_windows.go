//go:build windows

package tool

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	procGetForegroundWindow = modUser32.NewProc("GetForegroundWindow")
	procGetWindowTextW      = modUser32.NewProc("GetWindowTextW")
	procGetWindowThreadPID  = modUser32.NewProc("GetWindowThreadProcessId")
)

// foregroundWindow returns the title and owning process name of the current
// foreground window. Best-effort: empty strings when anything fails.
func foregroundWindow() (title, proc string) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", ""
	}

	// Title: GetWindowTextW into a fixed UTF-16 buffer.
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n > 0 {
		title = syscall.UTF16ToString(buf[:n])
	}

	// Owning process: PID → name via tasklist (avoids PSAPI plumbing).
	var pid uint32
	procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != 0 {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
		if err == nil {
			// CSV row: "name","pid",...
			line := strings.TrimSpace(string(out))
			if i := strings.IndexByte(line, ','); i > 0 && strings.HasPrefix(line, `"`) {
				proc = strings.Trim(line[:i], `"`)
			}
		}
	}
	return title, proc
}
