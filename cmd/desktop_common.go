//go:build !nogui

package cmd

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/embedded"
	"github.com/ponygates/icode/internal/server"
	"os/exec"
)

// desktopBoot holds a running desktop backend (embedded HTTP server + app) so
// the platform-specific window / tray layer can later shut it down cleanly.
type desktopBoot struct {
	app     *app.App
	srv     *server.Server
	port    int
	url     string
	cancel  context.CancelFunc
	logFile *os.File
}

// shutdown stops the HTTP server and releases the app resources. It uses a
// fresh context (not the one that may have been cancelled by the tray exit)
// so the server can finish in-flight requests during teardown. The redirected
// log file is closed here (not in bootDesktopBackend) so backend logs stay
// captured for the whole desktop session.
func (b *desktopBoot) shutdown() {
	if b.srv != nil {
		_ = b.srv.Shutdown(context.Background())
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.app != nil {
		b.app.Close()
	}
	if b.logFile != nil {
		_ = b.logFile.Close()
	}
}

// bootDesktopBackend starts the embedded iCode HTTP backend and blocks until
// it reports healthy. It is shared by every platform's desktop entry point:
// Windows opens a native WebView2 window, while macOS / Linux run a system
// tray and open the default browser.
func bootDesktopBackend() (*desktopBoot, error) {
	homeDir, _ := os.UserHomeDir()
	logDir := filepath.Join(homeDir, ".icode")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "desktop.log")
	logFile, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if logErr == nil {
		log.SetOutput(logFile)
		// Redirect os.Stderr into the same log file. Under -H windowsgui the
		// process has no console, so Go panics / fatal errors (which write to
		// os.Stderr and bypass the log package) would otherwise be lost
		// silently. Capturing them here makes the next crash diagnosable from
		// desktop.log instead of producing an unexplained "闪退".
		os.Stderr = logFile
		log.Printf("[desktop] === iCode desktop starting (log redirected to %s) ===", logPath)
	} else {
		logFile = nil
	}

	exePath, _ := os.Executable()
	rootDir := filepath.Dir(exePath)
	_ = os.Chdir(rootDir)

	a, err := app.Bootstrap()
	if err != nil {
		showDesktopError("iCode", "启动失败: "+err.Error())
		return nil, err
	}

	// Log startup diagnostics — these help identify desktop-only failures
	// (e.g. missing API key, proxy env vars, wrong config path).
	log.Printf("[desktop] config path: %s", filepath.Join(logDir, "config.yaml"))
	for _, pn := range a.Reg.List() {
		p, _ := a.Reg.Get(pn)
		hasKey := false
		if p != nil {
			hErr := p.Health(context.Background())
			hasKey = hErr == nil
		}
		log.Printf("[desktop] provider: %s (health=%v)", pn, hasKey)
	}
	if proxy := os.Getenv("HTTPS_PROXY"); proxy != "" {
		log.Printf("[desktop] WARNING: HTTPS_PROXY=%s (desktop uses system env, CLI uses terminal env)", proxy)
	}
	if proxy := os.Getenv("HTTP_PROXY"); proxy != "" {
		log.Printf("[desktop] WARNING: HTTP_PROXY=%s", proxy)
	}

	if f := embedded.Frontend(); f != nil {
		server.SetEmbeddedFrontend(f)
	}

	// Prefer a user-configured fixed port (settings → Server.Port); fall
	// back to an ephemeral port only when none is configured (0 = auto).
	port := a.Cfg.Server.Port
	if port == 0 {
		port = findFreePort()
	}
	if port == 0 {
		showDesktopError("iCode", "没有可用端口")
		return nil, fmt.Errorf("no free port")
	}

	srv := server.New(server.ServerConfig{
		Config:   a.Cfg,
		Registry: a.Reg,
		Store:    a.SessStore,
		DB:       a.DB,
		Engine:   a.Engine,
		Gate:     a.Gate,
		Updater:  a.Updater,
		Version:  appVersion,
		Port:     port,
	})

	ctx, cancel := context.WithCancel(context.Background())
	actualPort, err := srv.Start(ctx)
	if err != nil {
		showDesktopError("iCode", "服务启动失败: "+err.Error())
		cancel()
		a.Close()
		return nil, err
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
		cancel()
		a.Close()
		return nil, fmt.Errorf("server not ready")
	}

	appURL := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	return &desktopBoot{app: a, srv: srv, port: actualPort, url: appURL, cancel: cancel, logFile: logFile}, nil
}

// findFreePort grabs an ephemeral localhost port for the embedded backend.
func findFreePort() int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// openBrowser opens url in the platform's default browser. It is used by the
// macOS / Linux desktop path (which has no native window); on Windows the
// desktop uses a native WebView2 control instead.
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default: // linux, freebsd, etc.
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}

// drawIcodeImage renders the shared 64×64 blue "i" icon as an RGBA image.
// The platform-specific tray code encodes it to ICO (Windows) or PNG
// (macOS / Linux) as required by each systray backend.
func drawIcodeImage() *image.RGBA {
	const s = 64
	img := image.NewRGBA(image.Rect(0, 0, s, s))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.Transparent), image.Point{}, draw.Src)
	// blue rounded-ish square (solid fill)
	draw.Draw(img, image.Rect(6, 6, s-6, s-6),
		image.NewUniform(color.RGBA{0x25, 0x63, 0xeb, 255}), image.Point{}, draw.Over)
	// white "i": stem + dot
	draw.Draw(img, image.Rect(s/2-4, 20, s/2+4, 46),
		image.NewUniform(color.White), image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(s/2-4, 12, s/2+4, 20),
		image.NewUniform(color.White), image.Point{}, draw.Over)
	return img
}
