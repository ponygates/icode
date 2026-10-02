//go:build windows && !nogui

package cmd

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"unsafe"
)

// windowState is the remembered desktop window geometry, persisted to
// ~/.icode/window.json. Claude Desktop / Cursor both restore the previous
// window position and size — relaunching should put the window back where the
// user left it, not reset to the hard-coded 1200×820 center every time.
type windowState struct {
	X, Y      int
	W, H      int
	Maximized bool
}

var (
	procGetWindowRect   = user32.NewProc("GetWindowRect")
	procSetWindowPos    = user32.NewProc("SetWindowPos")
	procIsZoomed        = user32.NewProc("IsZoomed")
	procMonitorFromRect = user32.NewProc("MonitorFromRect")
)

type winRect struct{ Left, Top, Right, Bottom int32 }

const (
	swMaximize           = 3
	swpNoZOrder          = 0x0004
	swpNoActivate        = 0x0010
	monitorDefaultToNULL = 0
)

// windowStatePath returns the persistence file path, or "" when the home
// directory is unavailable (state saving/restoring is then skipped).
func windowStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".icode", "window.json")
}

// validateWindowState rejects degenerate remembered geometry (a collapsed
// window, or absurd dimensions from a corrupted file). Pure function so the
// guard is unit-testable on headless CI.
func validateWindowState(st windowState) bool {
	return st.W >= 400 && st.H >= 300 && st.W <= 16384 && st.H <= 16384
}

// saveWindowState persists the current window geometry (best-effort: failures
// only log — window memory is a convenience, never a hard requirement).
func saveWindowState(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	var r winRect
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ok == 0 || r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	zoomed, _, _ := procIsZoomed.Call(hwnd)
	st := windowState{
		X:         int(r.Left),
		Y:         int(r.Top),
		W:         int(r.Right - r.Left),
		H:         int(r.Bottom - r.Top),
		Maximized: zoomed != 0,
	}
	p := windowStatePath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		log.Printf("[desktop] save window state: %v", err)
	}
}

// restoreWindowState moves the freshly created window to the remembered
// geometry. The saved rectangle is only applied when it intersects an
// existing monitor (MONITOR_DEFAULTTONULL returns NULL on no intersection):
// after unplugging a monitor or changing resolutions the record may point
// off-screen, and shipping the window somewhere invisible would be worse
// than the default centered placement.
func restoreWindowState(hwnd uintptr) {
	p := windowStatePath()
	if p == "" || hwnd == 0 {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var st windowState
	if json.Unmarshal(b, &st) != nil || !validateWindowState(st) {
		return
	}
	r := winRect{int32(st.X), int32(st.Y), int32(st.X + st.W), int32(st.Y + st.H)}
	mon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), monitorDefaultToNULL)
	if mon == 0 {
		log.Printf("[desktop] saved window rect (%d,%d %dx%d) is off-screen; keeping default placement",
			st.X, st.Y, st.W, st.H)
		return
	}
	procSetWindowPos.Call(hwnd, 0, uintptr(st.X), uintptr(st.Y), uintptr(st.W), uintptr(st.H),
		swpNoZOrder|swpNoActivate)
	if st.Maximized {
		procShowWindow.Call(hwnd, swMaximize)
	}
}
