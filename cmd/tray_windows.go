//go:build windows && !nogui

package cmd

import (
	"bytes"
	"context"
	"encoding/binary"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getlantern/systray"
	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

// ── 全局托盘 / 窗口状态 ─────────────────────────────────────
var (
	desktopWV      webview2.WebView // 原生 WebView2 实例（供托盘"退出"时销毁）
	desktopHWND    uintptr          // 原生窗口句柄（供 ShowWindow 控制显隐）
	desktopVisible atomic.Bool      // 窗口当前是否可见
	origWndProc    uintptr          // 子类化前的原始窗口过程
	trayCancel     context.CancelFunc
)

// Win32 常量
const (
	wmHotkey    = 0x0312
	wmClose     = 0x0010
	gwlpWndProc = ^uintptr(3) // GWL_WNDPROC = -4 的 32 位补码形式，供 SetWindowLongPtrW 使用
	swHide      = 0
	swShow      = 5
	swRestore   = 9
	modControl  = 0x0002
	modShift    = 0x0004
	vkSpace     = 0x20
	hotkeyID    = 1
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc   = user32.NewProc("CallWindowProcW")
)

// runWebView 在独立 goroutine 中创建并运行原生 WebView2 窗口，
// 同时子类化其窗口过程并注册全局热键。调用方（主 goroutine）应随后
// 运行 runTray 以托管系统托盘消息泵。
func runWebView(url string) {
	// Recover from any panic inside WebView2 init / message pump so a single
	// Edge/Win32 hiccup cannot silently kill the whole desktop process (the
	// backend would otherwise die too, leaving the UI "frozen"). The stack is
	// logged to desktop.log for diagnosis.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[desktop] runWebView panic: %v\n%s", r, debug.Stack())
		}
	}()
	// go-webview2 requires WebView2 creation AND its message pump (w.Run) to
	// run on ONE OS thread: New captures the creating thread as mainthread
	// and Dispatch posts WMApp to it, while Run pumps the *current* thread's
	// queue. If the pump migrates to another thread (Go goroutines are
	// free-running), the window's messages are never dispatched — the window
	// freezes and Chromium's ~60s watchdog terminates the host ("桌面闪退").
	// Lock this goroutine so creation and pump always share a single thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cache, _ := os.UserCacheDir()
	dataPath := filepath.Join(cache, "icode", "webview")

	// Kill zombie WebView2 processes left by a previous crashed run BEFORE
	// creating the runtime — they hold the data-dir lock and would deadlock
	// NewWithOptions below (the "无窗口启动卡死" real-world cause).
	killStaleWebViewProcesses(dataPath)
	// Wipe the data dir pre-emptively when it has bloated past the size cap
	// or is unreadable — a corrupt/oversized dir makes NewWithOptions hang,
	// so repairing it here keeps boot fast instead of paying the timeout.
	healWebViewDataDir(dataPath)

	// Watchdog: creation is synchronous on this locked thread (WebView2 is
	// thread-affine, so it can't be moved into a timeout goroutine). The heal
	// above makes hangs unlikely; if one still happens, this logs where boot
	// stalled without freezing the tray.
	initDone := make(chan struct{})
	go func() {
		select {
		case <-initDone:
		case <-time.After(15 * time.Second):
			log.Printf("[desktop] WARNING: WebView2 creation still in progress after 15s")
		}
	}()

	w := tryInitWebView(dataPath, url, 1200, 820)
	if w == nil {
		// Primary data path is corrupt/locked: wipe it and retry a FRESH
		// primary before resorting to the fallback dir, so the broken dir is
		// repaired instead of failing every boot.
		log.Printf("[desktop] WebView2 failed on primary, wiping and retrying %s", dataPath)
		resetWebViewDataDir(dataPath)
		w = tryInitWebView(dataPath, url, 1200, 820)
	}
	if w == nil {
		// Primary still unavailable — try a clean fallback directory.
		fallback := dataPath + "-R"
		_ = os.RemoveAll(fallback)
		log.Printf("[desktop] WebView2 failed on primary again, retrying with %s", fallback)
		killStaleWebViewProcesses(fallback)
		w = tryInitWebView(fallback, url, 1200, 820)
	}
	if w == nil {
		// Last resort: ephemeral in-memory profile. No disk file, no
		// lock possible, always succeeds on a healthy WebView2 runtime.
		// Downside: no persistent cookies / login state — acceptable
		// fallback when the disk-based data paths are stuck.
		log.Printf("[desktop] WebView2 failed on fallback, retrying with ephemeral profile")
		w = tryInitWebView("", url, 1200, 820)
	}
	close(initDone)
	if w == nil {
		showDesktopError("iCode",
			"WebView2 窗口创建失败。\n\n"+
				"iCode 桌面端使用 Windows 原生 WebView2 控件渲染界面。\n"+
				"请安装或修复 Microsoft Edge WebView2 运行时后重试\n"+
				"（Windows 10/11 通常已内置）：\n\n"+
				"https://developer.microsoft.com/zh-cn/microsoft-edge/webview2/")
		return
	}
	desktopWV = w
	desktopHWND = uintptr(w.Window())

	// 子类化窗口过程：把 WM_CLOSE（关闭按钮）改为"隐藏到托盘"，
	// 并把 WM_HOTKEY（全局热键）路由到显隐切换。其余消息交给原过程。
	ret, _, _ := procSetWindowLongPtr.Call(desktopHWND, gwlpWndProc,
		uintptr(syscall.NewCallback(newWndProc)))
	origWndProc = ret

	// 注册全局热键 Ctrl+Shift+Space。RegisterHotKey 在调用线程注册，
	// 而本 goroutine 正好跑 webview 消息泵，因此 WM_HOTKEY 会在此线程
	// 经 newWndProc 分发。
	procRegisterHotKey.Call(desktopHWND, hotkeyID, modControl|modShift, vkSpace)

	desktopVisible.Store(true)
	w.Navigate(url)
	w.Run()

	// w.Run 返回即代表应当退出（托盘"退出"调用了 Terminate）。
	procUnregisterHotKey.Call(desktopHWND, hotkeyID)
}

// newWndProc 是被子类化安装的窗口过程。
func newWndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmClose:
		// 关闭按钮 → 隐藏到托盘，而非退出程序。
		procShowWindow.Call(hwnd, swHide)
		desktopVisible.Store(false)
		return 0
	case wmHotkey:
		toggleDesktopVisibility()
		return 0
	}
	ret, _, _ := procCallWindowProc.Call(origWndProc, hwnd, uintptr(msg), wparam, lparam)
	return ret
}

// toggleDesktopVisibility 在显示/隐藏之间切换主窗口。
func toggleDesktopVisibility() {
	if desktopHWND == 0 {
		return
	}
	if desktopVisible.Load() {
		procShowWindow.Call(desktopHWND, swHide)
		desktopVisible.Store(false)
	} else {
		procShowWindow.Call(desktopHWND, swRestore)
		procSetForeground.Call(desktopHWND)
		desktopVisible.Store(true)
	}
}

func showDesktop() {
	if desktopHWND == 0 {
		return
	}
	procShowWindow.Call(desktopHWND, swRestore)
	procSetForeground.Call(desktopHWND)
	desktopVisible.Store(true)
}

func hideDesktop() {
	if desktopHWND == 0 {
		return
	}
	procShowWindow.Call(desktopHWND, swHide)
	desktopVisible.Store(false)
}

// onTrayQuit 由托盘"退出"菜单触发：销毁 webview 并退出托盘循环。
func onTrayQuit() {
	if desktopWV != nil {
		desktopWV.Terminate()
	}
	systray.Quit()
}

// runTray 在主 goroutine 运行系统托盘消息泵（systray 会 LockOSThread）。
// 它必须在 runWebView 的 goroutine 之外运行，使两个消息泵分处不同线程。
func runTray() {
	systray.Run(func() {
		systray.SetTitle("iCode")
		systray.SetTooltip("iCode — 按 Ctrl+Shift+Space 唤起 / 隐藏")
		if ic, err := makeIcon(); err == nil && len(ic) > 0 {
			systray.SetIcon(ic)
		}
		mShow := systray.AddMenuItem("显示窗口", "显示 iCode 主窗口")
		mHide := systray.AddMenuItem("隐藏到托盘", "隐藏到系统托盘")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出 iCode", "退出并关闭后端服务")

		// 注：systray v1.2.2 不直接支持托盘图标左键回调（左键仅弹出菜单）。
		// 显隐通过菜单项"显示窗口 / 隐藏到托盘"或全局热键 Ctrl+Shift+Space 完成。

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[desktop] tray menu goroutine panic: %v\n%s", r, debug.Stack())
				}
			}()
			for {
				select {
				case <-mShow.ClickedCh:
					showDesktop()
				case <-mHide.ClickedCh:
					hideDesktop()
				case <-mQuit.ClickedCh:
					onTrayQuit()
				}
			}
		}()
	}, func() {
		// systray 退出回调：确保后端与窗口一起关闭。
		if desktopWV != nil {
			desktopWV.Terminate()
		}
		if trayCancel != nil {
			trayCancel()
		}
	})
}

// tryInitWebView attempts to create a WebView2 window, recovering from any
// panic (a nil-pointer deref inside go-webview2 when the runtime is
// uninitialized or locked). Returns nil on failure so callers can retry.
//
// IMPORTANT: this must be called on the SAME locked OS thread that will run
// w.Run() afterwards — go-webview2 captures the creating thread as mainthread
// and only that thread can pump the window's messages. Running it inside a
// separate goroutine (as an old timeout wrapper did) split creation from the
// message pump: the window froze and Chromium's watchdog killed the process
// ~60s later. Callers therefore lock their thread and call this synchronously.
func tryInitWebView(dataPath, url string, width, height uint) webview2.WebView {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[desktop] WebView2 init panic on %s: %v", dataPath, r)
		}
	}()
	log.Printf("[desktop] stage: creating WebView2 window (dataPath=%s)", dataPath)
	opts := webview2.WebViewOptions{
		Debug:    false,
		DataPath: dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "iCode",
			Width:  width,
			Height: height,
			Center: true,
		},
	}
	// Empty DataPath = ephemeral in-memory profile. When the persistent
	// data dir is locked by a zombie process and our cleanup couldn't
	// kill it, the last resort is a fully transient environment that
	// never touches disk — no lock, no hang.
	return webview2.NewWithOptions(opts)
}

// makeIcon 用标准库绘制一个 64×64 的蓝色 "i" 图标并封装为 ICO
// （PNG-in-ICO，Windows 托盘支持）字节流，避免依赖外部资源文件。
func makeIcon() ([]byte, error) {
	img := drawIcodeImage()

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, err
	}
	pngData := pngBuf.Bytes()

	const size = 64 // matches drawIcodeImage()
	var ico bytes.Buffer
	// ICONDIR
	ico.Write([]byte{0, 0, 1, 0, 1, 0})
	// ICONDIRENTRY
	ico.WriteByte(byte(size))                                     // bWidth
	ico.WriteByte(byte(size))                                     // bHeight
	ico.WriteByte(0)                                              // bColorCount
	ico.WriteByte(0)                                              // bReserved
	binary.Write(&ico, binary.LittleEndian, uint16(1))            // wPlanes
	binary.Write(&ico, binary.LittleEndian, uint16(32))           // wBitCount
	binary.Write(&ico, binary.LittleEndian, uint32(len(pngData))) // dwBytesInRes
	binary.Write(&ico, binary.LittleEndian, uint32(22))           // dwImageOffset
	ico.Write(pngData)
	return ico.Bytes(), nil
}
