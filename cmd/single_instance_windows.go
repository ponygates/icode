//go:build windows && !nogui

package cmd

import (
	"fmt"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ponygates/icode/internal/xgo"
	"golang.org/x/sys/windows"
)

// Single-instance guard for the windowed desktop mode. Two windowed
// instances racing to create WebView2 runtimes on the SAME user-data dir is a
// fast path to the dir-lock hang that froze startup; a named mutex makes the
// second instance exit cleanly with a message instead of fighting over the
// lock.
var (
	siKernel32      = windows.NewLazySystemDLL("kernel32.dll")
	siCreateMutex   = siKernel32.NewProc("CreateMutexW")
	siWaitForSingle = siKernel32.NewProc("WaitForSingleObject")
	siReleaseMutex  = siKernel32.NewProc("ReleaseMutex")
	siCloseHandle   = siKernel32.NewProc("CloseHandle")
	siCreateEvent   = siKernel32.NewProc("CreateEventW")
	siOpenEvent     = siKernel32.NewProc("OpenEventW")
	siSetEvent      = siKernel32.NewProc("SetEvent")

	siMutex uintptr
)

// showEventName is the named auto-reset event the FIRST desktop instance
// creates and the SECOND signals. The old flow showed a dead-end "iCode 已在
// running" error box while the real window sat hidden in the tray (✕ hides
// instead of quitting) — the source of the "每次使用都最小化" experience.
// Now the second launch surfaces the existing window instead.
const showEventName = "iCode.ShowWindow"

// acquireSingleInstance tries to become the sole windowed iCode instance.
// It returns a release func on success, or an error when another instance is
// already running (or the OS guard could not be created). Because a crashed
// run leaves the named mutex object behind (abandoned but existing),
// CreateMutex's ERROR_ALREADY_EXISTS alone cannot distinguish "another live
// instance" from "previous owner crashed" — so we probe ownership with a
// zero-timeout WaitForSingleObject: WAIT_TIMEOUT means a live owner still
// holds it, while WAIT_ABANDONED means the previous owner died and we take
// over cleanly.
func acquireSingleInstance() (func(), error) {
	name, err := windows.UTF16PtrFromString("iCode.SingleInstance")
	if err != nil {
		return nil, err
	}
	h, _, _ := siCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, fmt.Errorf("create single-instance mutex failed")
	}

	r, _, _ := siWaitForSingle.Call(h, 0)
	switch r {
	case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
		// We own it now — hold it for the whole process lifetime.
		siMutex = h
		return func() {
			siReleaseMutex.Call(h)
			siCloseHandle.Call(h)
			siMutex = 0
		}, nil
	default:
		// WAIT_TIMEOUT — another live instance holds the mutex.
		siCloseHandle.Call(h)
		return nil, fmt.Errorf("another iCode instance is already running")
	}
}

// startShowSignalWatcher creates the named show event and watches it for the
// process lifetime. Called by the FIRST instance right after it wins the
// single-instance mutex.
func startShowSignalWatcher() {
	name, err := windows.UTF16PtrFromString(showEventName)
	if err != nil {
		return
	}
	// Auto-reset (manualReset=0), initially unset: every SetEvent wakes the
	// watcher exactly once, no sticky state to clear.
	h, _, _ := siCreateEvent.Call(0, 0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return
	}
	xgo.GoSafe("desktop.showWatcher", func() {
		for {
			r, _, _ := siWaitForSingle.Call(h, windows.INFINITE)
			if r != uintptr(windows.WAIT_OBJECT_0) {
				return
			}
			// The signal can race window creation (a second launch fired
			// before the first window exists): poll up to 10s for the HWND.
			for i := 0; i < 50; i++ {
				if hwnd := atomic.LoadUintptr(&desktopHWND); hwnd != 0 {
					procShowWindow.Call(hwnd, swRestore)
					procSetForeground.Call(hwnd)
					desktopVisible.Store(true)
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		}
	})
}

// signalShowExisting asks the already-running instance to surface its
// (possibly tray-hidden) window. Returns false when the event cannot be
// opened — e.g. the first instance predates this mechanism — so the caller
// can fall back to the old error box.
func signalShowExisting() bool {
	name, err := windows.UTF16PtrFromString(showEventName)
	if err != nil {
		return false
	}
	h, _, _ := siOpenEvent.Call(windows.EVENT_MODIFY_STATE, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return false
	}
	defer siCloseHandle.Call(h)
	siSetEvent.Call(h)
	return true
}
