//go:build !windows && !nogui

package cmd

import (
	"bytes"
	"image/png"
	"log"

	"github.com/getlantern/systray"
	"golang.design/x/hotkey"
)

// openDesktopWindowPOSIX opens the default browser to the local frontend and
// runs the system tray message pump (which blocks until the user quits).
func openDesktopWindowPOSIX(boot *desktopBoot) {
	// Open the browser once so the user sees the UI immediately.
	_ = openBrowser(boot.url)
	runTrayPOSIX(boot)
}

// runTrayPOSIX runs the systray message loop on macOS / Linux. The tray offers
// "在浏览器中打开" (re-open / focus the UI) and "退出 iCode" (quit + stop backend).
// A cross-platform global hotkey (Ctrl+Shift+Space) is registered in a
// background goroutine so the UI can be summoned without touching the tray menu.
func runTrayPOSIX(boot *desktopBoot) {
	systray.Run(func() {
		systray.SetTitle("iCode")
		systray.SetTooltip("iCode — 按 Ctrl+Shift+Space 唤起，或点菜单在浏览器中打开")
		if ic, err := makeIconPNG(); err == nil && len(ic) > 0 {
			systray.SetIcon(ic)
		}
		mOpen := systray.AddMenuItem("在浏览器中打开", "用默认浏览器打开 iCode")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出 iCode", "退出并关闭后端服务")

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					_ = openBrowser(boot.url)
				case <-mQuit.ClickedCh:
					systray.Quit()
				}
			}
		}()
		// 注册跨平台全局热键（macOS / Linux；需要 CGO）。
		// 注册失败（如 macOS 未授权辅助功能、Linux Wayland 无全局热键支持）
		// 仅告警，不阻断托盘：菜单仍可唤起。
		go registerGlobalHotkeyPOSIX(boot)
	}, func() {
		// systray exit callback: ensure the backend closes too.
		boot.shutdown()
	})
}

// registerGlobalHotkeyPOSIX registers the cross-platform global hotkey
// Ctrl+Shift+Space. On the POSIX desktop there is no native window to hide, so
// the hotkey simply re-focuses / re-opens the UI in the default browser.
//
// Platform notes:
//   - Linux (X11): works out of the box in a background goroutine.
//   - macOS: hotkey events are delivered via a CGEventTap and require the app
//     to be trusted for Accessibility (System Settings → Privacy & Security →
//     Accessibility). Register() returns an error otherwise and is downgraded
//     to a warning.
//   - Linux (Wayland): global hotkeys are not exposed by the protocol; Register
//     typically fails — fall back to the tray menu.
func registerGlobalHotkeyPOSIX(boot *desktopBoot) {
	hk := hotkey.New([]hotkey.Modifier{hotkey.ModCtrl, hotkey.ModShift}, hotkey.KeySpace)
	if err := hk.Register(); err != nil {
		log.Printf("[desktop] 全局热键注册失败（可改用托盘菜单唤起）：%v", err)
		return
	}
	defer hk.Unregister()
	log.Printf("[desktop] 全局热键已注册：Ctrl+Shift+Space")
	for range hk.Keydown() {
		_ = openBrowser(boot.url)
	}
}

// makeIconPNG renders the shared "i" icon as a PNG byte stream for the
// macOS / Linux systray backends (which expect PNG, not ICO).
func makeIconPNG() ([]byte, error) {
	img := drawIcodeImage()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
