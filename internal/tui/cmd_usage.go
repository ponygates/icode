package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Command-usage persistence for C8 (Claude Code style "recommend commands
// from history"): each dispatched slash command bumps a counter, stored as
// ~/.icode/command_usage.json. Autocomplete ranks frequently-used commands
// first, so the suggestion list adapts to how the user actually works instead
// of always showing the same definition order. Best-effort I/O: a missing or
// corrupt file simply means "no history yet".

// cmdUsagePath returns the command-usage stats file location.
func cmdUsagePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".icode/command_usage.json"
	}
	return filepath.Join(home, ".icode", "command_usage.json")
}

// loadCmdUsage reads the persisted usage counters; empty map on any failure.
func loadCmdUsage() map[string]int {
	m := map[string]int{}
	data, err := os.ReadFile(cmdUsagePath())
	if err != nil {
		return m
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]int{}
	}
	return m
}

// saveCmdUsage persists the usage counters, creating the .icode dir if needed.
// Errors are swallowed — stats are a nice-to-have, never a hard failure.
func saveCmdUsage(m map[string]int) {
	path := cmdUsagePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}
