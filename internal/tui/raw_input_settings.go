package tui

import (
	"github.com/ponygates/icode/internal/config"
	i18n "github.com/ponygates/icode/internal/config/i18n"
)

// openSettings opens the settings overlay panel (Ctrl+P / Ctrl+,), refreshing
// its config snapshot from disk once on open so the render loop never hits
// the filesystem per frame. No-op while streaming or already open.
func (t *TUI) openSettings() {
	if t.streaming || t.settingsOpen {
		return
	}
	t.settingsOpen = true
	t.settingsCursor = 0
	t.mu.Lock()
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Default()
	}
	t.settingsCfg = cfg
	t.mu.Unlock()
	t.render()
}

// handleSettingsKey routes keypresses while the settings overlay is active.
// The row bound comes from the same settingsPanelItems list the renderer
// draws, so keyboard navigation and the visible rows can never drift apart.
// opencode-style: ↑↓ move, ←→ change the value IN PLACE (panel stays open),
// Enter acts on the row, Esc closes.
func (t *TUI) handleSettingsKey(r rune) bool {
	t.mu.Lock()
	cfg := t.settingsCfg
	t.mu.Unlock()
	if cfg == nil {
		cfg = config.Default()
	}
	n := len(t.settingsPanelItems(cfg))
	switch r {
	case 0x1b, 0x03: // Esc or Ctrl+C — close settings
		t.settingsOpen = false
		t.mu.Lock()
		t.settingsCfg = nil
		t.mu.Unlock()
		t.render()
		return true
	case 0x0e, 0x1b5b42: // Ctrl+N or Down arrow
		if t.settingsCursor < n-1 {
			t.settingsCursor++
			t.render()
		}
		return true
	case 0x10, 0x1b5b41: // Ctrl+P or Up arrow
		if t.settingsCursor > 0 {
			t.settingsCursor--
			t.render()
		}
		return true
	case 0x1b5b44, 0x1b5b43: // ← / → — change value in place, keep panel open
		items := t.settingsPanelItems(cfg)
		if t.settingsCursor >= 0 && t.settingsCursor < len(items) {
			t.settingsAdjust(items[t.settingsCursor].kind, r == 0x1b5b43)
		}
		return true
	case '\r', '\n': // Enter — act on the selected row
		items := t.settingsPanelItems(cfg)
		if t.settingsCursor < 0 || t.settingsCursor >= len(items) {
			return true
		}
		switch items[t.settingsCursor].kind {
		case "model": // open the interactive model picker
			t.settingsOpen = false
			t.settingsCfg = nil
			t.showModelPicker()
		case "mode", "lang", "theme": // cycle forward, panel stays open
			t.settingsAdjust(items[t.settingsCursor].kind, true)
		case "advanced": // full config dump + /config set cheatsheet
			t.settingsOpen = false
			t.settingsCfg = nil
			t.configDump()
		default: // provider & voice credentials → config dump
			t.settingsOpen = false
			t.settingsCfg = nil
			t.configDump()
		}
		t.render()
		return true
	}
	return true
}

// settingsAdjust cycles the given settings row's value by one step
// (forward=true → next, false → previous), applies it live (TUI field +
// backend callback when relevant + persisted config) and refreshes the
// in-panel snapshot so the drawn value updates without closing the panel.
func (t *TUI) settingsAdjust(kind string, forward bool) {
	t.mu.Lock()
	cfg := t.settingsCfg
	t.mu.Unlock()
	if cfg == nil {
		cfg = config.Default()
	}
	step := 1
	if !forward {
		step = -1
	}
	switch kind {
	case "mode":
		order := []Mode{ModeAuto, ModePlan, ModeAgent, ModeYOLO}
		idx := 0
		for i, m := range order {
			if m == t.mode {
				idx = i
				break
			}
		}
		next := order[(idx+step+len(order))%len(order)]
		t.mu.Lock()
		t.mode = next
		cfg.Defaults.Mode = string(next) // keep the panel snapshot in sync
		t.mu.Unlock()
		if t.callback != nil {
			if msg := t.callback.OnSetMode(next); msg != "" {
				t.notice(msg)
			}
		}
		t.persistSetting(func(c *config.Config) { c.Defaults.Mode = string(next) })
		t.notice("模式: " + t.modeLabel(string(next)))
	case "lang":
		order := []string{langZhCN, langZhTW, langEn}
		cur := t.lang
		if cur == "" {
			cur = langZhCN
		}
		idx := 0
		for i, l := range order {
			if l == cur {
				idx = i
				break
			}
		}
		next := order[(idx+step+len(order))%len(order)]
		t.mu.Lock()
		t.lang = next
		cfg.Language = next // keep the panel snapshot in sync
		t.mu.Unlock()
		// Sync the global translator so cmd-layer i18n.Tr strings follow.
		i18n.T.SetLanguage(i18n.Lang(next))
		t.persistSetting(func(c *config.Config) { c.Language = next })
		t.notice("语言: " + t.langName(next))
	case "theme":
		order := []string{"dark", "light"}
		cur := t.theme
		if cur != "light" {
			cur = "dark"
		}
		idx := 0
		for i, th := range order {
			if th == cur {
				idx = i
				break
			}
		}
		next := order[(idx+step+len(order))%len(order)]
		t.mu.Lock()
		t.theme = next
		cfg.TUI.Theme = next // keep the panel snapshot in sync
		t.mu.Unlock()
		t.persistSetting(func(c *config.Config) { c.TUI.Theme = next })
		t.notice("主题: " + t.themeName(next))
	default:
		return // non-cyclable row — no-op
	}
	t.render()
}
