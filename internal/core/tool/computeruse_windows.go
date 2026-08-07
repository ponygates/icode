//go:build windows

package tool

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// ---------------------------------------------------------------------------
// GDI screen capture (BitBlt → GetDIBits → PNG)
// ---------------------------------------------------------------------------

var (
	modUser32                  = syscall.NewLazyDLL("user32.dll")
	modGdi32                   = syscall.NewLazyDLL("gdi32.dll")
	procGetDC                  = modUser32.NewProc("GetDC")
	procReleaseDC              = modUser32.NewProc("ReleaseDC")
	procGetSystemMetrics       = modUser32.NewProc("GetSystemMetrics")
	procCreateCompatibleDC     = modGdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = modGdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = modGdi32.NewProc("SelectObject")
	procDeleteDC               = modGdi32.NewProc("DeleteDC")
	procDeleteObject           = modGdi32.NewProc("DeleteObject")
	procGetDIBits              = modGdi32.NewProc("GetDIBits")
	procBitBlt                 = modGdi32.NewProc("BitBlt")
	procSendInput              = modUser32.NewProc("SendInput")
	procSetCursorPos           = modUser32.NewProc("SetCursorPos")
	procMapVirtualKey          = modUser32.NewProc("MapVirtualKeyW")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
	srcCopy           = 0x00CC0020
	biRGB             = 0
)

// bmiHeader mirrors BITMAPINFOHEADER; pivot fields used for 32bpp top-down.
type bmiHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

func sysMetric(nIndex int) int {
	r, _, _ := procGetSystemMetrics.Call(uintptr(nIndex))
	return int(int32(r))
}

// virtualDesktop returns the bounding box (x,y,w,h) of the virtual desktop.
func virtualDesktop() (x, y, w, h int) {
	return sysMetric(smXVirtualScreen), sysMetric(smYVirtualScreen),
		sysMetric(smCXVirtualScreen), sysMetric(smCYVirtualScreen)
}

// captureScreen returns the requested region (default full virtual desktop)
// as PNG bytes plus its pixel size.
func captureScreen(x0, y0, w, h int) ([]byte, int, int, error) {
	vx, vy, vw, vh := virtualDesktop()
	if w == 0 || h == 0 {
		x0, y0, w, h = vx, vy, vw, vh
	}
	if x0 < vx {
		x0 = vx
	}
	if y0 < vy {
		y0 = vy
	}
	if x0+w > vx+vw {
		w = vx + vw - x0
	}
	if y0+h > vy+vh {
		h = vy + vh - y0
	}
	if w <= 0 || h <= 0 {
		return nil, 0, 0, fmt.Errorf("无效的截图区域")
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, 0, 0, fmt.Errorf("GetDC 失败")
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, 0, 0, fmt.Errorf("CreateCompatibleDC 失败")
	}
	defer procDeleteDC.Call(memDC)

	hbmp, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(w), uintptr(h))
	if hbmp == 0 {
		return nil, 0, 0, fmt.Errorf("CreateCompatibleBitmap 失败")
	}
	defer procDeleteObject.Call(hbmp)

	old, _, _ := procSelectObject.Call(memDC, hbmp)
	defer procSelectObject.Call(memDC, old)

	procBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h),
		screenDC, uintptr(x0), uintptr(y0), srcCopy)

	var info bmiHeader
	info.biSize = uint32(unsafe.Sizeof(info))
	info.biWidth = int32(w)
	info.biHeight = -int32(h) // negative top-down
	info.biPlanes = 1
	info.biBitCount = 32
	info.biCompression = biRGB

	pix := make([]byte, w*h*4)
	procGetDIBits.Call(memDC, hbmp, 0, uintptr(h),
		uintptr(unsafe.Pointer(&pix[0])),
		uintptr(unsafe.Pointer(&info)),
		uintptr(biRGB))

	// Convert BGRX → RGBA.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			o := x * 4
			row[o+0] = pix[i+2]
			row[o+1] = pix[i+1]
			row[o+2] = pix[i+0]
			row[o+3] = 255
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), w, h, nil
}

// ---------------------------------------------------------------------------
// SendInput mouse/keyboard control
// ---------------------------------------------------------------------------

type point struct {
	x int32
	y int32
}

const (
	inputMouse = 0
	inputKeybd = 1

	mfMove         = 0x0001
	mfLeftDown     = 0x0002
	mfLeftUp       = 0x0004
	mfRightDown    = 0x0008
	mfRightUp      = 0x0010
	mfMiddleDown   = 0x0020
	mfMiddleUp     = 0x0040
	mfWheel        = 0x0800
	mfHWheel       = 0x1000
	mfAbsolute     = 0x8000

	kfKeyUp   = 0x0002
	kfUnicode = 0x0004
	kfScanCode = 0x0008
)

type mouseInput_t struct {
	dx          int32
	dy          int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type keybdInput_t struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type inputUnion struct {
	mi mouseInput_t
	ki keybdInput_t
}

type inputBlock struct {
	typ   uint32
	union inputUnion
}

// sendMouseEvent injects a single mouse INPUT.
func sendMouseEvent(flags, data uint32, dx, dy int32) {
	var b inputBlock
	b.typ = inputMouse
	b.union.mi.dwFlags = flags
	b.union.mi.mouseData = data
	b.union.mi.dx = dx
	b.union.mi.dy = dy
	procSendInput.Call(1, uintptr(unsafe.Pointer(&b)), unsafe.Sizeof(b))
}

// sendKeyEvent posts one keybd INPUT (down unless up=true).
func sendKeyEvent(vk uint16, scan uint16, up bool, unicodeFlag bool) {
	var b inputBlock
	b.typ = inputKeybd
	b.union.ki.wVk = vk
	b.union.ki.wScan = scan
	if up {
		b.union.ki.dwFlags |= kfKeyUp
	}
	if unicodeFlag {
		b.union.ki.dwFlags |= kfUnicode
	}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&b)), unsafe.Sizeof(b))
}

// moveMouse moves the cursor to absolute screen coordinates.
func moveMouse(x, y int) error {
	procSetCursorPos.Call(uintptr(x), uintptr(y))
	return nil
}

// mouseDownUpMap resolves the down/up flag pair for a button name.
func mouseFlags(button string) (down, up uint32) {
	switch button {
	case "right":
		return mfRightDown, mfRightUp
	case "middle":
		return mfMiddleDown, mfMiddleUp
	default:
		return mfLeftDown, mfLeftUp
	}
}

func clickMouse(button string, double bool, x, y int) error {
	if x != 0 || y != 0 {
		if err := moveMouse(x, y); err != nil {
			return err
		}
	}
	down, up := mouseFlags(button)
	sendMouseEvent(down, 0, 0, 0)
	sendMouseEvent(up, 0, 0, 0)
	if double {
		sendMouseEvent(down, 0, 0, 0)
		sendMouseEvent(up, 0, 0, 0)
	}
	return nil
}

func scrollMouse(dx, dy int) error {
	// Wheel data carries the delta in the high word of mouseData.
	if dy != 0 {
		data := uint32(int32(dy) << 16)
		sendMouseEvent(mfWheel, data, 0, 0)
	}
	if dx != 0 {
		data := uint32(int32(dx) << 16)
		sendMouseEvent(mfHWheel, data, 0, 0)
	}
	return nil
}

// typeText injects Unicode text character-by-character via KEYEVENTF_UNICODE.
func typeText(text string) error {
	for _, r := range []rune(text) {
		switch r {
		case '\n', '\r':
			tapVK(vk_Enter)
			continue
		case '\t':
			tapVK(vk_Tab)
			continue
		}
		units := utf16.Encode([]rune{r})
		for _, u := range units {
			sendUnicode(u, false)
			sendUnicode(u, true)
		}
	}
	return nil
}

// pressKey resolves a key or shortcut (modifiers joined by '+') and presses.
func pressKey(key string) error {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(key)), "+")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return fmt.Errorf("empty key")
	}
	var mods []uint16
	var named uint16
	for _, p := range parts {
		s := strings.TrimSpace(p)
		switch s {
		case "ctrl", "control":
			mods = append(mods, vk_Control)
		case "alt":
			mods = append(mods, vk_Alt)
		case "shift":
			mods = append(mods, vk_Shift)
		default:
			named = resolveKey(s)
		}
	}
	if named == 0 {
		return fmt.Errorf("无法识别的按键: %s", key)
	}

	for _, m := range mods {
		sendVK(m, false)
	}
	sendVK(named, false)
	sendVK(named, true)
	for i := len(mods) - 1; i >= 0; i-- {
		sendVK(mods[i], true)
	}
	return nil
}

func sendVK(vk uint16, up bool) {
	sendKeyEvent(vk, 0, up, false)
}

func tapVK(vk uint16) {
	sendVK(vk, false)
	sendVK(vk, true)
}

func sendUnicode(u uint16, up bool) {
	sendKeyEvent(0, u, up, true)
}

// resolveKey maps a human key name to a virtual-key code, falling back to a
// literal letter/digit. Returns 0 if unrecognised.
func resolveKey(name string) uint16 {
	switch name {
	case "enter", "return":
		return vk_Enter
	case "tab":
		return vk_Tab
	case "escape", "esc":
		return vk_Escape
	case "backspace", "bs":
		return vk_Back
	case "delete", "del":
		return vk_Delete
	case "space":
		return vk_Space
	case "up":
		return vk_Up
	case "down":
		return vk_Down
	case "left":
		return vk_Left
	case "right":
		return vk_Right
	case "home":
		return vk_Home
	case "end":
		return vk_End
	case "pageup", "pagedown":
		if name == "pageup" {
			return vk_PageUp
		}
		return vk_PageDown
	case "ctrl", "control":
		return vk_Control
	case "alt":
		return vk_Alt
	case "shift":
		return vk_Shift
	}
	// Function keys.
	if len(name) == 2 && name[0] == 'f' && name[1] >= '1' && name[1] <= '9' {
		return vk_F1 + uint16(name[1]-'1')
	}
	if len(name) == 3 && name[0] == 'f' && name[1] >= '1' && name[2] >= '0' && name[2] <= '9' {
		n := (name[1]-'0')*10 + (name[2] - '0')
		if n >= 10 && n <= 24 {
			return vk_F1 + uint16(n-1)
		}
	}
	// Single letters/digits → VK == uppercase ASCII.
	if len(name) == 1 {
		r := name[0]
		if r >= 'a' && r <= 'z' {
			return uint16(r - 'a' + 'A')
		}
		if r >= '0' && r <= '9' {
			return uint16(r)
		}
	}
	return 0
}

// Virtual-key codes used by computer use.
const (
	vk_Back  = 0x08
	vk_Tab   = 0x09
	vk_Enter = 0x0D
	vk_Escape = 0x1B
	vk_Space = 0x20
	vk_PageUp = 0x21
	vk_PageDown = 0x22
	vk_End   = 0x23
	vk_Home  = 0x24
	vk_Left  = 0x25
	vk_Up    = 0x26
	vk_Right = 0x27
	vk_Down  = 0x28
	vk_Delete = 0x2E
	vk_Shift = 0x10
	vk_Control = 0x11
	vk_Alt   = 0x12
	vk_F1    = 0x70
)