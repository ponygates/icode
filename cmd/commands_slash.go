package cmd

import (
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/tui"
)

// OnSlashCommand dispatches the slash commands the CLI layer owns (the TUI
// forwards everything it does not handle itself).
//
// The commands live in three disjoint domain groups — engine/config, session
// store, staged edits — each of which reports whether it claimed the command.
// The original single switch had no code after it, so "claimed" is equivalent
// to the old end-of-switch: nothing downstream ever ran for a matched name.
// The unknown-command fallback keeps the ORIGINAL (non-lowercased) spelling of
// cmd, exactly as before.
func (c *chatCallback) OnSlashCommand(cmd string, args []string) {
	name := strings.ToLower(cmd)
	if c.slashEngineConfig(name, args) {
		return
	}
	if c.slashSession(name, args) {
		return
	}
	if c.slashEdits(name, args) {
		return
	}
	c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Unknown command: %s", cmd))
}

// slashEngineConfig handles the model / mode / thinking / voice / config
// commands (engine-facing settings, nothing session-scoped).
func (c *chatCallback) slashEngineConfig(cmd string, args []string) bool {
	switch cmd {
	case "/model":
		if len(args) > 0 {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Switched model to: %s", args[0]))
		}
		return true

	case "/mode":
		if len(args) > 0 {
			if c.app != nil && c.app.Gate != nil {
				c.app.Gate.SetMode(permission.Mode(strings.ToLower(args[0])))
			}
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Mode set to: %s", args[0]))
		}
		return true

	case "/thinking":
		c.slashThinking(args)
		return true

	case "/voice":
		c.slashVoice()
		return true

	case "/config":
		if cfg, cerr := config.LoadOrCreate(); cerr == nil {
			c.tui.AddMessage(tui.RoleSystem, renderConfigPanel(cfg))
		} else {
			c.tui.AddMessage(tui.RoleSystem, "Config unavailable: "+cerr.Error())
		}
		return true
	}
	return false
}
