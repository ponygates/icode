//go:build windows && !nogui

package cmd

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Single-instance guard for the windowed modes (desktop + simple UI). Two
// windowed instances racing to create WebView2 runtimes on the SAME user-data
// dir is a fast path to the dir-lock hang that froze startup; a named mutex
// makes the second instance exit cleanly with a message instead of fighting
// over the lock.
var (
	siKernel32      = windows.NewLazySystemDLL("kernel32.dll")
	siCreateMutex   = siKernel32.NewProc("CreateMutexW")
	siWaitForSingle = siKernel32.NewProc("WaitForSingleObject")
	siReleaseMutex  = siKernel32.NewProc("ReleaseMutex")
	siCloseHandle   = siKernel32.NewProc("CloseHandle")

	siMutex uintptr
)

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
