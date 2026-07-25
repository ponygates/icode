//go:build !windows && !nogui

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// desktopCmd starts iCode in desktop mode on macOS / Linux — a system tray
// plus the default browser, since a native WebView2 window is Windows-only.
var desktopCmd = &cobra.Command{
	Use:   "desktop",
	Short: "启动桌面版（系统托盘 + 浏览器）",
	Long: `启动 iCode 桌面版 — 在系统托盘中运行，并通过默认浏览器打开界面。

原生窗口目前仅 Windows 支持（WebView2）。在 macOS / Linux 上，iCode 桌面版
使用系统托盘（show/hide/退出）+ 默认浏览器访问本机前端，后端数据不会离开本机。

提示：托盘菜单「在浏览器中打开」可随时唤回界面；全局热键（Ctrl+Shift+Space）
目前为 Windows 专属，非 Windows 平台请用托盘菜单操作。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDesktop()
	},
}

// runDesktop boots the backend and runs the platform tray + browser. It blocks
// until the user quits from the tray menu, then shuts the backend down.
func runDesktop() error {
	boot, err := bootDesktopBackend()
	if err != nil {
		return err
	}
	openDesktopWindowPOSIX(boot)
	boot.shutdown()
	return nil
}

// showDesktopError reports a fatal desktop error on non-Windows platforms.
// There is no GUI message box here (that would need a C-dependency), so we
// print to stderr — POSIX desktop is normally launched from a terminal.
func showDesktopError(title, text string) {
	fmt.Fprintf(os.Stderr, "[%s] %s\n", title, text)
}
