//go:build windows && !nogui
// +build windows,!nogui

package cmd

import (
	"unsafe"

	"github.com/spf13/cobra"
	"golang.org/x/sys/windows"
)

// desktopCmd starts iCode in desktop mode — a native WebView2 window
// connecting to the embedded HTTP backend.
var desktopCmd = &cobra.Command{
	Use:   "desktop",
	Short: "启动桌面版（原生窗口）",
	Long: `启动 iCode 桌面版 — 打开一个原生 Windows 窗口。

桌面版使用系统内置的 WebView2 控件，无需浏览器。
数据不会离开本机。

双击 icode.exe 会自动进入桌面模式。
（macOS / Linux 使用系统托盘 + 默认浏览器方案，见同命令的非 Windows 实现。）`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDesktop()
	},
}

// runDesktop boots the backend and opens the native desktop window.
func runDesktop() error {
	// Note: the binary is linked with -H windowsgui, so a standalone launch
	// (double-click / shortcut) never allocates a console window in the first
	// place — there is nothing to hide here.

	boot, err := bootDesktopBackend()
	if err != nil {
		return err
	}
	// Expose the cancel func so the tray's "退出" can terminate the backend
	// loop from its own exit callback.
	trayCancel = boot.cancel
	openDesktopWindow(boot.url)

	// openDesktopWindow 会阻塞直到托盘退出；此时用全新 context 关闭后端，
	// 避免复用已被托盘退出取消的 ctx。
	boot.shutdown()
	return nil
}

func openDesktopWindow(url string) {
	// 原生 WebView2 窗口在独立 goroutine 中运行其消息泵（见 tray_windows.go
	// 的 runWebView）；当前（主）goroutine 运行系统托盘消息泵（runTray）。
	// 两个消息泵分处不同线程。关闭按钮经子类化窗口过程改为"隐藏到托盘"，
	// 只有托盘菜单的"退出"才会真正销毁窗口并结束进程。
	go runWebView(url)
	runTray()
}

func showDesktopError(title, text string) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	msgBox := user32.NewProc("MessageBoxW")
	t, _ := windows.UTF16PtrFromString(text)
	ti, _ := windows.UTF16PtrFromString(title)
	msgBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(ti)), 0x10)
}
