//go:build !windows

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ApplyAutostart registers or removes iCode from the current user's
// login-time autostart on macOS / Linux. It is pure file I/O (no CGO):
//   - macOS:   ~/Library/LaunchAgents/com.ponygates.icode.plist (LaunchAgent)
//   - Linux:   ~/.config/autostart/icode.desktop (XDG autostart)
//
// Other POSIX systems (freebsd, etc.) are silently ignored. Failures are
// returned to the caller, which logs them without failing the config save.
func ApplyAutostart(enabled bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	switch runtime.GOOS {
	case "darwin":
		dir := filepath.Join(home, "Library", "LaunchAgents")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		p := filepath.Join(dir, "com.ponygates.icode.plist")
		if !enabled {
			_ = os.Remove(p)
			return nil
		}
		return os.WriteFile(p, []byte(launchAgentPlist(exe)), 0o644)

	case "linux":
		dir := filepath.Join(home, ".config", "autostart")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		p := filepath.Join(dir, "icode.desktop")
		if !enabled {
			_ = os.Remove(p)
			return nil
		}
		return os.WriteFile(p, []byte(autostartDesktop(exe)), 0o644)

	default:
		// Unsupported POSIX platform — nothing to do.
		return nil
	}
}

// launchAgentPlist renders a LaunchAgent plist that starts iCode in desktop
// mode on login, for the current user only (RunAtLoad, no KeepAlive so it
// does not respawn after the user quits).
func launchAgentPlist(exe string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.ponygates.icode</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>desktop</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`, exe)
}

// autostartDesktop renders an XDG autostart .desktop entry that launches
// iCode in desktop mode on login.
func autostartDesktop(exe string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=iCode
Comment=iCode AI Coding Agent
Exec=%s desktop
Terminal=false
X-GNOME-Autostart-enabled=true
`, exe)
}
