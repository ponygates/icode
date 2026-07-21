//go:build windows
// +build windows

package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/embedded"
	"github.com/ponygates/icode/internal/server"
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

双击 icode.exe 会自动进入桌面模式。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDesktop()
	},
}

// runDesktop boots the backend and opens the native desktop window.
func runDesktop() error {
	// Note: the binary is linked with -H windowsgui, so a standalone launch
	// (double-click / shortcut) never allocates a console window in the first
	// place — there is nothing to hide here.
	exePath, _ := os.Executable()
	rootDir := filepath.Dir(exePath)
	_ = os.Chdir(rootDir)

	a, err := app.Bootstrap()
	if err != nil {
		showDesktopError("iCode", "启动失败: "+err.Error())
		return err
	}
	defer a.Close()

	if f := embedded.Frontend(); f != nil {
		server.SetEmbeddedFrontend(f)
	}

	port := findFreePort()
	if port == 0 {
		showDesktopError("iCode", "没有可用端口")
		return fmt.Errorf("no free port")
	}

	srv := server.New(server.ServerConfig{
		Config:   a.Cfg,
		Registry: a.Reg,
		Store:    a.SessStore,
		DB:       a.DB,
		Engine:   a.Engine,
		Gate:     a.Gate,
		Updater:  a.Updater,
		Port:     port,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	actualPort, err := srv.Start(ctx)
	if err != nil {
		showDesktopError("iCode", "服务启动失败: "+err.Error())
		return err
	}

	healthURL := fmt.Sprintf("http://127.0.0.1:%d/api/health", actualPort)
	client := &http.Client{Timeout: 2 * time.Second}
	ready := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}

	if !ready {
		showDesktopError("iCode", "后端未能在预期时间内就绪")
		return fmt.Errorf("server not ready")
	}

	appURL := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	openDesktopWindow(appURL)

	_ = srv.Shutdown(ctx)
	return nil
}

func openDesktopWindow(url string) {
	cache, _ := os.UserCacheDir()
	dataPath := filepath.Join(cache, "icode", "webview")

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:    false,
		DataPath: dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "iCode",
			Width:  1200,
			Height: 820,
			Center: true,
		},
	})
	if w == nil {
		showDesktopError("iCode",
			"无法初始化原生窗口（WebView2 运行时未安装）。\n\n"+
				"iCode 桌面端使用 Windows 原生 WebView2 控件渲染界面。\n"+
				"请安装 Microsoft Edge WebView2 运行时后重试\n"+
				"（Windows 10/11 通常已内置）：\n\n"+
				"https://developer.microsoft.com/zh-cn/microsoft-edge/webview2/")
		return
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
}

func findFreePort() int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func showDesktopError(title, text string) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	msgBox := user32.NewProc("MessageBoxW")
	t, _ := windows.UTF16PtrFromString(text)
	ti, _ := windows.UTF16PtrFromString(title)
	msgBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(ti)), 0x10)
}
