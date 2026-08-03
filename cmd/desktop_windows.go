//go:build windows && !nogui
// +build windows,!nogui

package cmd

import (
	"os"
	"unsafe"

	"github.com/ponygates/icode/internal/xgo"
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
	release, err := acquireSingleInstance()
	if err != nil {
		showDesktopError("iCode", "iCode 已在运行。\n\n本机已有一个 iCode 窗口，请勿重复启动（避免 WebView2 数据目录被占用导致卡死）。")
		return nil
	}
	defer release()

	boot, err := bootDesktopBackend()
	if err != nil {
		return err
	}
	trayCancel = boot.cancel
	openDesktopWindow(boot.url)

	boot.shutdown()
	os.Exit(0)
	return nil
}

func openDesktopWindow(url string) {
	// 原生 WebView2 窗口在独立 goroutine 中运行其消息泵（见 tray_windows.go
	// 的 runWebView）；当前（主）goroutine 运行系统托盘消息泵（runTray）。
	// 两个消息泵分处不同线程。关闭按钮经子类化窗口过程改为"隐藏到托盘"，
	// 只有托盘菜单的"退出"才会真正销毁窗口并结束进程。
	xgo.GoSafe("desktop.runWebView", func() { runWebView(url) })
	runTray()
}

func showDesktopError(title, text string) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	msgBox := user32.NewProc("MessageBoxW")
	t, _ := windows.UTF16PtrFromString(text)
	ti, _ := windows.UTF16PtrFromString(title)
	msgBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(ti)), 0x10)
}
