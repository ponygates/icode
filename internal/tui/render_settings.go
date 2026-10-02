package tui

import (
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/config"
)

// settingItem is one row of the settings overlay panel. kind routes the
// ←→ / Enter behaviour in handleSettingsKey: cyclable rows (mode/lang/theme)
// switch in place; model opens the picker; the rest open the config dump.
type settingItem struct {
	label string
	value string
	kind  string // model|provider|mode|lang|theme|credential|advanced
}

// settingsPanelItems builds the settings-panel rows. Labels go through the
// TUI i18n table (t.tstr) so /lang applies to the overlay; value display is
// localized too (mode → 计划/智能体/…, lang → 简体中文/…, theme → 深色/…).
// The row COUNT is derived from this single list, and handleSettingsKey
// clamps navigation to len(settingsPanelItems(...)) so the drawn rows and the
// keyboard bounds can never drift apart.
func (t *TUI) settingsPanelItems(cfg *config.Config) []settingItem {
	return []settingItem{
		{label: t.tstr("settings.model"), value: cfg.Defaults.Model, kind: "model"},
		{label: t.tstr("settings.provider"), value: cfg.Defaults.Provider, kind: "provider"},
		{label: t.tstr("settings.mode"), value: t.modeLabel(cfg.Defaults.Mode), kind: "mode"},
		{label: t.tstr("settings.lang"), value: t.langName(cfg.Language), kind: "lang"},
		{label: t.tstr("settings.theme"), value: t.themeName(cfg.TUI.Theme), kind: "theme"},
		{label: t.tstr("settings.voiceProv"), value: cfg.Voice.Provider, kind: "credential"},
		{label: t.tstr("settings.baiduKey"), value: maskStringTUI(cfg.Voice.BaiduAPIKey), kind: "credential"},
		{label: t.tstr("settings.xfyunAppID"), value: cfg.Voice.IFlytekAppID, kind: "credential"},
		{label: t.tstr("settings.advanced"), value: "↵", kind: "advanced"},
	}
}

// renderSettingsPanel draws the settings overlay panel.
func (t *TUI) renderSettingsPanel() string {
	t.mu.Lock()
	W := t.width
	H := t.height
	t.mu.Unlock()

	if W < 40 || H < 15 {
		return ""
	}

	// Use the snapshot loaded when the panel opened (see the Ctrl+, handler);
	// fall back to a one-off load so render() itself never hits the disk.
	t.mu.Lock()
	cfg := t.settingsCfg
	t.mu.Unlock()
	if cfg == nil {
		var err error
		cfg, err = config.Load()
		if err != nil {
			cfg = config.Default()
		}
	}

	// Panel dimensions
	panelW := W - 8
	if panelW > 70 {
		panelW = 70
	}
	panelH := H - 6
	if panelH > 30 {
		panelH = 30
	}
	startX := (W - panelW) / 2
	startY := (H - panelH) / 2

	// Build settings items (labels via the TUI i18n table, so /lang applies
	// to the overlay too). settingsPanelItems keeps the row list in ONE place:
	// renderSettingsPanel draws it and handleSettingsKey clamps navigation to
	// its length, so the two can never drift apart.
	items := t.settingsPanelItems(cfg)

	var b strings.Builder
	// Move cursor to panel position
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", startY+1, startX+1))

	// Draw panel border
	borderH := panelH
	borderW := panelW

	// Top border
	b.WriteString("\x1b(0") // Enter line drawing mode
	b.WriteString("l")
	b.WriteString(strings.Repeat("q", borderW-2))
	b.WriteString("k")
	b.WriteString("\x1b(B") // Exit line drawing mode

	// Title
	title := t.tstr("settings.title")
	// visibleWidth, not len: the title is Chinese ("设置" = 4 columns but 6
	// bytes), and byte-based padding pushed the right border off by the
	// difference, skewing the panel frame.
	titlePad := (borderW - 2 - visibleWidth(title)) / 2
	if titlePad < 0 {
		titlePad = 0
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", startY+1, startX+1))
	b.WriteString("\x1b(0l")
	b.WriteString(strings.Repeat("q", titlePad))
	b.WriteString("\x1b(B")
	b.WriteString("\x1b[1m" + title + "\x1b[0m")
	b.WriteString("\x1b(0")
	tailQ := borderW - 2 - titlePad - visibleWidth(title)
	if tailQ < 0 {
		tailQ = 0
	}
	b.WriteString(strings.Repeat("q", tailQ))
	b.WriteString("k")
	b.WriteString("\x1b(B")

	// Content rows
	for i, item := range items {
		rowY := startY + 2 + i
		if rowY >= startY+borderH-1 {
			break
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH", rowY, startX+1))
		b.WriteString("\x1b(0x\x1b(B") // Left border

		// Highlight current row
		if i == t.settingsCursor {
			b.WriteString("\x1b[7m") // Reverse video
		}

		// Format: label + padding + value. Widths are VISIBLE widths —
		// len() would byte-count Chinese values and break the right border.
		label := item.label + ": "
		contentW := visibleWidth(label) + visibleWidth(item.value)
		if contentW > borderW-3 {
			// Over-wide row: clip the value so the row can never wrap and
			// desynchronise the panel frame against the terminal.
			keep := borderW - 3 - visibleWidth(label)
			if keep < 0 {
				keep = 0
			}
			item.value = truncVisible(item.value, keep)
			contentW = visibleWidth(label) + visibleWidth(item.value)
		}
		padding := borderW - 2 - contentW
		if padding < 0 {
			padding = 0
		}
		b.WriteString(" " + label + strings.Repeat(" ", padding) + item.value)

		if i == t.settingsCursor {
			b.WriteString("\x1b[0m") // Reset
		}

		// Pad remaining width and right border
		remaining := borderW - 2 - (1 + contentW + padding)
		if remaining > 0 {
			b.WriteString(strings.Repeat(" ", remaining))
		}
		b.WriteString("\x1b(0\x1b(B") // Right border
	}

	// Empty rows
	for i := len(items); i < panelH-2; i++ {
		rowY := startY + 2 + i
		if rowY >= startY+borderH-1 {
			break
		}
		b.WriteString(fmt.Sprintf("\x1b[%d;%dH", rowY, startX+1))
		b.WriteString("\x1b(0x")
		b.WriteString(strings.Repeat(" ", borderW-2))
		b.WriteString("x\x1b(B")
	}

	// Bottom border
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", startY+borderH-1, startX+1))
	b.WriteString("\x1b(0m")
	b.WriteString(strings.Repeat("q", borderW-2))
	b.WriteString("j")
	b.WriteString("\x1b(B")

	// Hint line
	hintY := startY + borderH
	hint := t.tstr("settings.hint")
	hintX := startX + (panelW-visibleWidth(hint))/2
	if hintX < 1 {
		hintX = 1
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH%s", hintY, hintX, hint))
	// Park the physical cursor INSIDE the panel (on the highlighted row) and
	// hide it: the panel has no text input, and a cursor left on the hint
	// line lets conhost ECHO / IME composition paint typed text into the
	// middle of the conversation area — the classic "text lands above the
	// input box" symptom.
	rowY := startY + 2 + t.settingsCursor
	if rowY > startY+borderH-2 {
		rowY = startY + borderH - 2
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;%dH", rowY, startX+2))
	b.WriteString("\x1b[?25l")

	return b.String()
}

func maskStringTUI(s string) string {
	if s == "" {
		return "(未设置)"
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
