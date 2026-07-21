//go:build windows

package cmd

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	enableVirtualTerminalProcessing = 0x0004
	stdInputHandle                  = ^uintptr(0) - 9  // -10
	stdOutputHandle                 = ^uintptr(0) - 10 // -11
	stdErrorHandle                  = ^uintptr(0) - 11 // -12
	// attachParentProcess is the special (DWORD)-1 argument to AttachConsole
	// meaning "attach to the console of the parent process".
	attachParentProcess = ^uintptr(0)
)

// setupConsoleIO wires up standard I/O for the GUI-subsystem binary.
//
// iCode is now linked with -H windowsgui so that double-clicking the exe from
// Explorer does NOT create a black console window (previously a console flashed
// on screen and was hidden after the fact via FreeConsole). The trade-off of a
// GUI-subsystem binary is that when it IS launched from a terminal the process
// no longer automatically inherits the parent's console, so CLI output would go
// nowhere. This function restores that:
//
//   - If Go already has valid standard handles (output was redirected to a file
//     or pipe, e.g. `icode ... > out.txt`), keep them untouched.
//   - Otherwise try to attach to the parent process's console (the cmd/
//     PowerShell that launched us). On success, rebind Go's os.Stdin/Stdout/
//     Stderr to that console so the TUI and all CLI output work normally.
//   - If there is no parent console (a genuine Explorer double-click), return
//     false — the caller then starts desktop mode.
//
// Returns true when a usable console/stdio is available (CLI context), false
// when the process has no console at all (double-click / GUI-launch context).
func setupConsoleIO() bool {
	// Case 1: stdout was inherited (redirect/pipe) — nothing to do.
	if h := os.Stdout.Fd(); h != 0 && h != uintptr(syscall.InvalidHandle) {
		return true
	}

	// Case 2: launched from a terminal — attach to the parent's console.
	if attachParentConsole() {
		rebindStdHandles()
		return true
	}

	// Case 3: no console (double-clicked in Explorer).
	return false
}

// attachParentConsole attaches this GUI-subsystem process to the console of its
// parent process (the cmd/PowerShell that started it). Returns true on success.
func attachParentConsole() bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	attachConsole := kernel32.NewProc("AttachConsole")
	if attachConsole.Find() != nil {
		return false
	}
	r, _, _ := attachConsole.Call(attachParentProcess)
	return r != 0
}

// rebindStdHandles reopens the freshly-attached console's I/O devices
// (CONIN$/CONOUT$) and routes both the process-level standard handles and Go's
// os.Stdin/Stdout/Stderr to them, so subsequent fmt.* and the raw-mode TUI read
// and write to the real console.
func rebindStdHandles() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setStdHandle := kernel32.NewProc("SetStdHandle")

	if out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		setStdHandle.Call(stdOutputHandle, out.Fd())
		setStdHandle.Call(stdErrorHandle, out.Fd())
		os.Stdout = out
		os.Stderr = out
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		setStdHandle.Call(stdInputHandle, in.Fd())
		os.Stdin = in
	}
}

// fixConsoleCodepage configures the Windows console for iCode:
//  1. Sets input/output code pages to UTF-8 (65001) so Unicode UI glyphs
//     and Chinese user input render correctly.
//  2. Enables virtual-terminal processing so ANSI escape sequences used
//     by the full-screen TUI render correctly.
func fixConsoleCodepage() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setCP := kernel32.NewProc("SetConsoleCP")
	setOutCP := kernel32.NewProc("SetConsoleOutputCP")
	if setCP.Find() == nil {
		setCP.Call(65001)
	}
	if setOutCP.Find() == nil {
		setOutCP.Call(65001)
	}

	getStdHandle := kernel32.NewProc("GetStdHandle")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")

	hOut, _, _ := getStdHandle.Call(stdOutputHandle)
	if hOut == 0 || hOut == ^uintptr(0) {
		return
	}
	var mode uint32
	r, _, _ := getConsoleMode.Call(hOut, uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return
	}
	mode |= enableVirtualTerminalProcessing
	setConsoleMode.Call(hOut, uintptr(mode))
}

// showCLIMessage displays a native message box telling the user this is a
// command-line tool, then waits for a key press before exiting.
func showCLIMessage() {
	windows.MessageBox(windows.HWND(0),
		windows.StringToUTF16Ptr("This is a command line tool.\r\n\r\n"+
			"You need to open cmd.exe / PowerShell and run it from there:\r\n\r\n"+
			"  cd \\path\\to\\icode\r\n"+
			"  icode.exe\r\n\r\n"+
			"双击桌面版请使用 iCode.exe（桌面应用程序）。"),
		windows.StringToUTF16Ptr("iCode — 命令行工具"),
		windows.MB_OK|windows.MB_ICONINFORMATION|windows.MB_TOPMOST)
	// Also print to console in case it's visible
	fmt.Println("\nThis is a command line tool.")
	fmt.Println("You need to open cmd.exe / PowerShell and run icode.exe from there.")
	fmt.Println("For the desktop app, double-click iCode.exe instead.")
	fmt.Print("\n按 Enter 键退出...")
	fmt.Scanln()
	os.Exit(0)
}
