package tui

import (
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/config"
)

// configCommand shows the current configuration, or sets a key when given
// `set <key> <value>` (Claude Code's /config opens an interactive menu; here
// we expose the most useful keys directly).
// modelsCommand implements /models with optional add/rm subcommands so the TUI
// can manage user-defined models without dropping to the CLI:
//
//	/models                     → list custom models
//	/models add <p> <id> [name] → persist + live-register a custom model
//	/models rm <id>             → remove a custom model (id = provider/model_id)
func (t *TUI) modelsCommand(args []string) {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "add":
			if len(args) < 3 {
				t.add(RoleSystem, "用法: /models add <provider> <model_id> [name]")
				return
			}
			name := args[2]
			if len(args) >= 4 {
				name = strings.Join(args[3:], " ")
			}
			msg := t.callback.OnAddCustomModel(args[1], args[2], name)
			t.add(RoleSystem, msg)
			return
		case "rm", "remove", "del", "delete":
			if len(args) < 2 {
				t.add(RoleSystem, "用法: /models rm <id>（id 形如 provider/model_id）")
				return
			}
			t.add(RoleSystem, t.callback.OnRemoveCustomModel(args[1]))
			return
		}
	}

	cfg, err := config.Load()
	if err != nil || len(cfg.Models) == 0 {
		t.add(RoleSystem, "暂无自定义模型。\n用 `/models add <provider> <model_id> [name]` 或 `icode config model add <provider> <model_id> [name]` 新增。")
		return
	}
	var b strings.Builder
	b.WriteString("自定义模型：\n")
	for _, m := range cfg.Models {
		name := m.Name
		if name == "" {
			name = m.ModelID
		}
		b.WriteString(fmt.Sprintf("  %-26s %s / %s\n", m.ID, m.Provider, name))
	}
	t.add(RoleSystem, b.String())
}

// showModelPicker lists the available models with a 1-based index and marks
// the active one, so the user can switch via `/model <n>` or `/model <id>`.
// It works in both raw and line mode (it just appends a system message).
func (t *TUI) showModelPicker() {
	if len(t.models) == 0 {
		t.add(RoleSystem, "暂无可用模型列表。\n  请先配置 API Key：icode auth set --provider <provider> --key <YOUR_KEY>\n  或直接切换：/model <模型ID>（如 /model openrouter/free）")
		return
	}
	t.openModelPicker()
}

// buildModelPickerList renders a static /model list (used in line mode, where
// the interactive overlay is unavailable). The row at highlightIdx is marked
// with ▶ (pass -1 for no highlight). The current model is tagged "(当前)".
func (t *TUI) buildModelPickerList(highlightIdx int) string {
	var b strings.Builder
	b.WriteString("选择模型（↑/↓ 移动，Enter 确认，Esc 取消；也可直接输入编号）：\n")
	for i, m := range t.models {
		mark := "  "
		if i == highlightIdx {
			mark = "▶ "
		} else if m == t.model {
			mark = "  " // current but not highlighted
		}
		tag := ""
		if m == t.model {
			tag = "  (当前)"
		}
		b.WriteString(fmt.Sprintf("  %s%-3d %s%s\n", mark, i+1, m, tag))
	}
	if highlightIdx >= 0 && highlightIdx < len(t.models) {
		b.WriteString(fmt.Sprintf("\n  当前高亮：%s\n", t.models[highlightIdx]))
	}
	return b.String()
}

// modelPickerOverlay renders the interactive /model panel as a FIXED overlay
// (like helpBox / permLines). It is always fully on screen regardless of the
// conversation scroll position or the number of models. When the list is taller
// than the viewport, an internal top-index (modelPickerTop) scrolls the window
// so the highlighted row (modelPickerIdx) is always visible — this is what
// fixes the "highlight scrolls out of view" symptom that the old
// message-appended panel had.
func (t *TUI) modelPickerOverlay(W, bodyH int) []string {
	title := "选择模型（↑/↓ 移动，Enter 确认，Esc 取消；也可直接输入编号）："
	const titleRows = 1
	maxRows := bodyH - titleRows
	if maxRows < 1 {
		maxRows = 1
	}
	n := len(t.models)
	if n == 0 {
		return []string{title, t.paint("dim", "  （暂无可用模型）")}
	}
	// When the list is taller than the viewport we also need a scroll-hint
	// line, so reserve one row for it to keep the whole overlay within bodyH.
	if n > maxRows {
		maxRows = bodyH - titleRows - 1
		if maxRows < 1 {
			maxRows = 1
		}
	}
	// Keep modelPickerIdx inside the visible window [top, top+maxRows).
	if t.modelPickerTop < 0 {
		t.modelPickerTop = 0
	}
	if t.modelPickerIdx < t.modelPickerTop {
		t.modelPickerTop = t.modelPickerIdx
	}
	if t.modelPickerIdx >= t.modelPickerTop+maxRows {
		t.modelPickerTop = t.modelPickerIdx - maxRows + 1
	}
	if t.modelPickerTop > n-maxRows {
		t.modelPickerTop = n - maxRows
	}
	if t.modelPickerTop < 0 {
		t.modelPickerTop = 0
	}
	var lines []string
	lines = append(lines, t.paint("bold", title))
	for i := t.modelPickerTop; i < t.modelPickerTop+maxRows && i < n; i++ {
		num := fmt.Sprintf("%-3d", i+1)
		var row string
		if i == t.modelPickerIdx {
			row = "  " + t.paint("green", "▶ ") + " " + num + t.paint("green", t.models[i])
		} else {
			row = "  " + "  " + num + t.models[i]
		}
		if t.models[i] == t.model {
			row += t.paint("dim", "  (当前)")
		}
		lines = append(lines, row)
	}
	if n > maxRows {
		above := t.modelPickerTop
		below := n - (t.modelPickerTop + maxRows)
		lines = append(lines, t.paint("dim",
			fmt.Sprintf("  ↑ %d 更多  ·  ↓ %d 更多  (共 %d)", above, below, n)))
	}
	return lines
}

// openModelPicker enters selection mode. In raw mode it opens the interactive
// overlay; in line mode it prints a static list (no overlay available).
func (t *TUI) openModelPicker() {
	if len(t.models) == 0 {
		t.add(RoleSystem, "暂无可用模型列表。\n  请先配置 API Key：icode auth set --provider <provider> --key <YOUR_KEY>\n  或直接切换：/model <模型ID>（如 /model openrouter/free）")
		return
	}
	if !t.rawMode {
		t.add(RoleSystem, t.buildModelPickerList(-1))
		return
	}
	t.modelPickerOpen = true
	if t.modelIdx < 0 || t.modelIdx >= len(t.models) {
		t.modelIdx = 0
	}
	t.modelPickerIdx = t.modelIdx
	t.modelPickerTop = 0
	t.render()
}

// updateModelPicker refreshes the overlay after navigation.
func (t *TUI) updateModelPicker() {
	if t.modelPickerOpen && t.rawMode {
		t.render()
	}
}

// movePicker shifts the highlight by delta and refreshes the panel.
func (t *TUI) movePicker(delta int) {
	if len(t.models) == 0 {
		return
	}
	t.modelPickerIdx += delta
	if t.modelPickerIdx < 0 {
		t.modelPickerIdx = 0
	}
	if t.modelPickerIdx >= len(t.models) {
		t.modelPickerIdx = len(t.models) - 1
	}
	t.updateModelPicker()
}

// selectModelAt confirms the model at index i and closes the picker.
func (t *TUI) selectModelAt(i int) {
	if i < 0 || i >= len(t.models) {
		t.closeModelPicker()
		return
	}
	t.model = t.models[i]
	t.modelIdx = i
	t.closeModelPicker()
	t.add(RoleSystem, t.tstr("mode.set")+" -> "+t.model)
}

// closeModelPicker exits selection mode and removes the overlay.
func (t *TUI) closeModelPicker() {
	if !t.modelPickerOpen {
		return
	}
	t.modelPickerOpen = false
	t.modelPickerTop = 0
	if t.rawMode {
		t.render()
	}
}

// ── /resume interactive session picker ───────────────────────────
