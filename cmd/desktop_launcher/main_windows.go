//go:build windows
// +build windows

package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func main() {
	// Ensure we're in the right directory (desktop subfolder)
	exePath, _ := os.Executable()
	rootDir := filepath.Dir(exePath)
	if err := os.Chdir(rootDir); err != nil {
		// Try parent directory
		parentDir := filepath.Dir(rootDir)
		_ = os.Chdir(parentDir)
	}

	// Find icode.exe in the root directory
	icodeExe := filepath.Join(rootDir, "icode.exe")
	if _, err := os.Stat(icodeExe); os.IsNotExist(err) {
		// Try parent
		icodeExe = filepath.Join(filepath.Dir(rootDir), "icode.exe")
		if _, err := os.Stat(icodeExe); os.IsNotExist(err) {
			showErrorBox("iCode 桌面端", "iCode 后端未找到，请确认 icode.exe 与本程序在同一目录。")
			return
		}
	}

	// Find a free port
	port := findFreePort()
	if port == 0 {
		showErrorBox("iCode 桌面端", "没有可用端口，无法启动。")
		return
	}

	// Start the iCode server (backend). Kill it when the window closes.
	cmd := exec.Command(icodeExe, "server", "--port", fmt.Sprintf("%d", port))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		showErrorBox("iCode 桌面端", fmt.Sprintf("启动后端失败: %v", err))
		return
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// Wait for server to be ready
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/api/health", port)
	ready := false
	for i := 0; i < 60; i++ {
		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
	}

	if !ready {
		showErrorBox("iCode 桌面端", "后端未能在预期时间内就绪。")
		return
	}

	appURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	openAppWindow(appURL)
}

// openAppWindow hosts the UI inside a real native WebView2 window — it is NOT
// a browser tab/window. WebView2 is a system control (the same engine Edge
// uses) embedded directly into our own window, so there is no address bar, no
// tabs, and no browser involved.
func openAppWindow(url string) {
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
		// WebView2 runtime is not installed. Never fall back to a browser —
		// just tell the user what's missing.
		showErrorBox("iCode 桌面端",
			"无法初始化原生窗口（WebView2 运行时未安装）。\n\n"+
				"iCode 桌面端使用 Windows 原生 WebView2 控件渲染界面，不依赖任何浏览器。\n"+
				"请安装 Microsoft Edge WebView2 运行时后重试（Windows 10/11 通常已内置）：\n\n"+
				"https://developer.microsoft.com/zh-cn/microsoft-edge/webview2/")
		return
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
	// w.Run() returns when the window is closed; the deferred backend kill above
	// then terminates the server process.
}

func findFreePort() int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// showErrorBox displays a native Windows message box (pure Go, no CGO, no
// browser). Used for fatal startup errors.
func showErrorBox(title, text string) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	msgBox := user32.NewProc("MessageBoxW")
	t, _ := windows.UTF16PtrFromString(text)
	ti, _ := windows.UTF16PtrFromString(title)
	// MB_OK = 0, MB_ICONERROR = 0x10
	msgBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(ti)), 0x10)
}
