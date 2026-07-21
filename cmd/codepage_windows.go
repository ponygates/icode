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
	stdOutputHandle                 = ^uintptr(0) - 10 // -11
)

// isDoubleClicked returns true when the binary was launched by double-clicking
// in Windows Explorer. It uses GetConsoleProcessList: when launched from a
// terminal (cmd/powershell) the console has 2+ processes attached; when
// double-clicked the console is brand-new with only this process (count = 1).
func isDoubleClicked() bool {
	// If there are command-line args, definitely not a double-click
	if len(os.Args) > 1 {
		return false
	}

	// Check via Windows API
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getProcList := kernel32.NewProc("GetConsoleProcessList")
	if getProcList.Find() != nil {
		return false
	}
	var pids [4]uint32
	ret, _, _ := getProcList.Call(uintptr(unsafe.Pointer(&pids[0])), 4)
	return ret == 1
}

// hideOwnConsole detaches this process from its console when the console is
// NOT shared with a parent terminal. That is exactly the case when the binary
// is launched by double-clicking in Explorer (or "icode desktop" from a
// shortcut): Windows allocates a brand-new console that would otherwise show
// as a black CMD window behind the desktop GUI. Calling FreeConsole() makes
// that window disappear. If the process was started from an existing
// cmd/PowerShell (console shared, process count > 1) we keep the console so
// CLI usage and logs are unaffected.
func hideOwnConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getProcList := kernel32.NewProc("GetConsoleProcessList")
	freeConsole := kernel32.NewProc("FreeConsole")
	if getProcList.Find() != nil || freeConsole.Find() != nil {
		return
	}
	var pids [4]uint32
	ret, _, _ := getProcList.Call(uintptr(unsafe.Pointer(&pids[0])), 4)
	if ret == 1 {
		freeConsole.Call()
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
