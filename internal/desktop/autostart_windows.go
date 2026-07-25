//go:build windows

// Package desktop holds platform-specific desktop-integration helpers that
// are safe to import from the server (no GUI / no CGO dependency).
package desktop

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// ApplyAutostart registers or removes iCode from the current user's
// login-time autostart. On Windows this writes / deletes the
// "iCode" value under HKCU\Software\Microsoft\Windows\CurrentVersion\Run,
// pointing at the running executable with the "desktop" subcommand.
//
// The operation is pure-Go (no CGO) and best-effort: failures are returned
// to the caller, which logs them without failing the config save.
func ApplyAutostart(enabled bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	key, _, err := registry.CreateKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`,
		registry.SET_VALUE,
	)
	if err != nil {
		return err
	}
	defer key.Close()

	if enabled {
		// Quote the exe path (it may contain spaces) and launch desktop mode.
		cmd := `"` + exe + `" desktop`
		return key.SetStringValue("iCode", cmd)
	}

	// Disabled: remove the value (ignore "not found").
	_ = key.DeleteValue("iCode")
	return nil
}
