//go:build windows && !nogui

package cmd

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Native folder-picker for the desktop workspace "add local directory"
// feature. Bound into the WebView2 page as window.pickDirectory() so the React
// UI can choose a real local directory instead of hand-typing a path.
var (
	shell32DLL               = windows.NewLazySystemDLL("shell32.dll")
	ole32DLL                 = windows.NewLazySystemDLL("ole32.dll")
	procSHBrowseForFolder    = shell32DLL.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32DLL.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree        = ole32DLL.NewProc("CoTaskMemFree")
	procOleInitialize        = ole32DLL.NewProc("OleInitialize")
	procOleUninitialize      = ole32DLL.NewProc("OleUninitialize")
	procGetShellWindow       = windows.NewLazySystemDLL("user32.dll").NewProc("GetShellWindow")
)

const (
	bifReturnFSAncestors = 0x0008
	bifEditBox           = 0x0010
	bifNewDialogStyle    = 0x0040
)

type browseInfo struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName uintptr
	lpszTitle      uintptr
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

// pickDirectory shows a native folder-selection dialog and returns the chosen
// absolute path. Returns "" when the user cancels.
//
// Runs on the WebView2 UI thread (via the bound JS bridge). A modal dialog
// pumps its own message loop, so WebView2 keeps rendering behind it and the
// promise resolves only after the dialog closes.
func pickDirectory() (string, error) {
	procOleInitialize.Call(0)
	defer procOleUninitialize.Call()

	var display [windows.MAX_PATH]uint16
	title, _ := syscall.UTF16PtrFromString("选择工作区目录")

	var bi browseInfo
	// Owner: the desktop window when present, else the shell window so the
	// dialog still appears modal in normal/restored states.
	if desktopHWND != 0 {
		bi.hwndOwner = desktopHWND
	} else if h, _, _ := procGetShellWindow.Call(); h != 0 {
		bi.hwndOwner = h
	}
	bi.pszDisplayName = uintptr(unsafe.Pointer(&display[0]))
	bi.lpszTitle = uintptr(unsafe.Pointer(title))
	bi.ulFlags = bifReturnFSAncestors | bifEditBox | bifNewDialogStyle

	pidl, _, _ := procSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", nil // user cancelled
	}
	defer procCoTaskMemFree.Call(pidl)

	var path [windows.MAX_PATH]uint16
	r, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if r == 0 {
		return "", nil
	}
	return syscall.UTF16ToString(path[:]), nil
}
