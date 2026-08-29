package tui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ponygates/icode/internal/config"
)

// Input-history persistence (Claude Code parity): the prompts the user typed
// are kept in ~/.icode/input_history.json so ↑ recalls them across sessions,
// not only within the current one. The file is local-only (0600) and never
// uploaded; `history_persist: false` in config.yml opts out entirely.
//
// All I/O is gated on historyPersistActive, which Run() arms for real
// interactive sessions. Unit tests construct a TUI and call pushHistory
// directly, so without this gate they would overwrite the user's real history
// file — the gate keeps the disk untouched outside a live session.

// maxHistoryEntries caps the persisted prompts (most recent kept).
const maxHistoryEntries = 200

// historyPersistActive gates all history I/O; armed once by Run().
var historyPersistActive bool

// historyPath returns the location of the persisted prompt history.
func historyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".icode/input_history.json"
	}
	return filepath.Join(home, ".icode", "input_history.json")
}

// historyPersistEnabled reports whether prompt history may touch the disk.
// Defaults to true when config can't be read; an explicit
// `history_persist: false` disables it.
func historyPersistEnabled() bool {
	cfg, err := config.Load()
	if err != nil {
		return true
	}
	if cfg.HistoryPersist != nil {
		return *cfg.HistoryPersist
	}
	return true
}

// loadInputHistory reads the persisted prompts (oldest first, most recent
// last). Returns nil when disabled, missing, or corrupt — history is a
// convenience, never a hard failure.
func loadInputHistory() []string {
	if !historyPersistActive {
		return nil
	}
	data, err := os.ReadFile(historyPath())
	if err != nil {
		return nil
	}
	var h []string
	if err := json.Unmarshal(data, &h); err != nil {
		return nil
	}
	if len(h) > maxHistoryEntries {
		h = h[len(h)-maxHistoryEntries:]
	}
	return h
}

// saveInputHistory persists the prompts, capped to the most recent
// maxHistoryEntries entries. Best-effort: errors are swallowed.
func saveInputHistory(h []string) {
	if !historyPersistActive {
		return
	}
	if len(h) > maxHistoryEntries {
		h = h[len(h)-maxHistoryEntries:]
	}
	path := historyPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(h)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}
