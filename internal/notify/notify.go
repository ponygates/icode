// Package notify surfaces system-level desktop notifications (toast/balloon)
// so the user is alerted when a background task finishes even if the app
// window is not focused.
//
// Windows: PowerShell + WinForms NotifyIcon balloon (zero external deps, runs
// in a detached process, never blocks the caller). POSIX: notify-send (Linux)
// or osascript (macOS) — best-effort.
package notify

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

var policyMu sync.Mutex
var policy = struct {
	enabled   bool
	quietFrom string // "HH:MM", 24h
	quietTo   string // "HH:MM", 24h
}{enabled: true}

// SetPolicy configures notification behaviour: whether notifications are
// enabled at all, and an optional daily do-not-disturb window ("HH:MM").
// quietFrom > quietTo expresses a window that wraps midnight.
func SetPolicy(enabled bool, quietFrom, quietTo string) {
	policyMu.Lock()
	defer policyMu.Unlock()
	policy.enabled = enabled
	policy.quietFrom = quietFrom
	policy.quietTo = quietTo
}

// shouldNotify reports whether a notification is allowed right now.
func shouldNotify() bool {
	policyMu.Lock()
	defer policyMu.Unlock()
	if !policy.enabled {
		return false
	}
	if policy.quietFrom == "" || policy.quietTo == "" {
		return true
	}
	now := time.Now().Format("15:04")
	if policy.quietFrom <= policy.quietTo {
		// Normal window within a single day.
		return now < policy.quietFrom || now > policy.quietTo
	}
	// Wraps midnight: quiet from evening to morning.
	return now > policy.quietTo && now < policy.quietFrom
}

// Notify shows a system notification with the given title and body. It is
// best-effort and never blocks the caller — failures are silently ignored.
func Notify(title, body string) {
	if !shouldNotify() {
		return
	}
	switch runtime.GOOS {
	case "windows":
		go notifyWindows(title, body)
	case "darwin":
		go func() {
			cmd := exec.Command("osascript", "-e",
				`display notification "`+body+`" with title "`+title+`"`)
			_ = cmd.Run()
		}()
	case "linux":
		go func() {
			cmd := exec.Command("notify-send", title, body)
			_ = cmd.Run()
		}()
	}
}

// notifyWindows shows a balloon tip via a throwaway NotifyIcon.
func notifyWindows(title, body string) {
	script := `Add-Type -AssemblyName System.Windows.Forms
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.Visible = $true
$n.BalloonTipIcon = 'Info'
$n.BalloonTipTitle = ` + psQuote(title) + `
$n.BalloonTipText = ` + psQuote(body) + `
$n.ShowBalloonTip(5000)
Start-Sleep -Milliseconds 5500
$n.Dispose()`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive",
		"-WindowStyle", "Hidden", "-Command", script)
	_ = cmd.Run()
}

// psQuote wraps s in a PowerShell single-quoted string, escaping embedded
// single quotes (PowerShell uses ” inside single-quoted strings).
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
