package tui

import (
	"strconv"
	"strings"

	"github.com/ponygates/icode/internal/config"
)

// handleSlashModel dispatches model, provider and permission-mode commands
// (including the OAuth-style /login /logout prompts). It reports whether the
// command was claimed.
func (t *TUI) handleSlashModel(cmd string, args []string) bool {
	switch cmd {
	case "/model":
		t.slashModel(args)
	case "/models":
		t.modelsCommand(args)
	case "/mode":
		t.slashMode(args)
	case "/plan", "/ask", "/debug":
		t.slashModeShortcut(cmd)
	case "/provider":
		t.slashProvider(args)
	case "/update":
		t.slashUpdate()
	case "/add-dir":
		t.slashAddDir(args)
	case "/output-style":
		t.slashOutputStyle(args)
	case "/admin":
		t.slashAdmin(args)
	case "/login":
		t.loginCommand(args)
	case "/logout":
		t.logoutCommand(args)
	default:
		return false
	}
	return true
}

func (t *TUI) slashModel(args []string) {
	if len(args) == 0 {
		// No argument → open the interactive model picker (Claude Code
		// style): ↑/↓ move, Enter confirms, Esc cancels, a digit jumps.
		t.openModelPicker()
		return
	}
	// Accept /model <n> (1-based index into the picker) or /model <id>.
	if n, err := strconv.Atoi(args[0]); err == nil && n >= 1 && n <= len(t.models) {
		t.model = t.models[n-1]
	} else {
		id := args[0]
		idx := indexOfString(t.models, id)
		if idx >= 0 {
			t.model = id
			t.modelIdx = idx
		} else {
			// Accept the ID as-is but warn if it isn't in the known list.
			t.model = id
			t.modelIdx = -1
			t.add(RoleSystem, "⚠️ 模型 “"+id+"” 不在可用列表中（仍需手动确认）")
			return
		}
	}
	t.notice("模型 → " + t.model)
	t.add(RoleSystem, t.tstr("mode.set")+" → "+t.model)
}

func (t *TUI) slashMode(args []string) {
	if len(args) > 0 {
		want := strings.ToLower(args[0])
		valid := map[string]bool{"agent": true, "plan": true, "yolo": true, "auto": true, "ask": true}
		if valid[want] {
			t.setMode(want)
			t.add(RoleSystem, "模式 → "+t.modeLabel(want))
		} else {
			t.add(RoleError, "无效模式: "+want+"（可选 agent/plan/yolo/auto/ask）")
		}
		return
	}
	t.add(RoleSystem, "当前模式: "+t.modeLabel(t.mode)+"\n用法: /mode <agent|plan|yolo|auto|ask>")
}

// slashModeShortcut handles the /plan /ask /debug one-word mode switches
// (/debug is the historical alias for agent mode).
func (t *TUI) slashModeShortcut(cmd string) {
	want := strings.TrimPrefix(cmd, "/")
	if want == "debug" {
		want = "agent"
	}
	t.setMode(want)
	t.add(RoleSystem, "模式 → "+t.modeLabel(want))
}

func (t *TUI) slashProvider(args []string) {
	if len(args) > 0 {
		t.provider = args[0]
		t.add(RoleSystem, "服务商 → "+args[0])
	} else {
		t.add(RoleSystem, "当前服务商: "+t.provider+"\n用法: /provider <name>")
	}
}

func (t *TUI) slashUpdate() {
	if t.callback != nil {
		t.add(RoleSystem, t.callback.OnUpdateModels())
	} else {
		t.add(RoleSystem, "引擎未初始化。")
	}
}

func (t *TUI) slashAddDir(args []string) {
	if len(args) == 0 {
		var list []string
		if c, err := config.Load(); err == nil {
			list = c.Defaults.ExtraDirs
		}
		if len(list) == 0 {
			t.add(RoleSystem, "没有额外工作目录。\n用法: /add-dir <路径>")
		} else {
			t.add(RoleSystem, "额外工作目录：\n  "+strings.Join(list, "\n  ")+"\n\n添加: /add-dir <路径>")
		}
		return
	}
	if t.callback != nil {
		t.add(RoleSystem, t.callback.OnAddDir(args[0]))
	} else {
		t.add(RoleSystem, "引擎未初始化。")
	}
}

func (t *TUI) slashOutputStyle(args []string) {
	if len(args) == 0 {
		cur := "normal"
		if c, err := config.Load(); err == nil && c.Defaults.OutputStyle != "" {
			cur = c.Defaults.OutputStyle
		}
		t.add(RoleSystem, "当前输出风格: "+cur+"\n用法: /output-style <concise|normal|verbose>")
		return
	}
	style := strings.ToLower(args[0])
	if style != "concise" && style != "normal" && style != "verbose" {
		t.add(RoleError, "无效风格: "+args[0]+"（可选 concise|normal|verbose）")
		return
	}
	if t.callback != nil {
		t.add(RoleSystem, t.callback.OnOutputStyle(style))
	} else {
		t.persistSetting(func(c *config.Config) { c.Defaults.OutputStyle = style })
		t.add(RoleSystem, "输出风格已设为 "+style+"（已持久化，重启会话后生效）")
	}
}

// slashAdmin toggles unattended yolo mode (/admin off restores ask mode).
func (t *TUI) slashAdmin(args []string) {
	if len(args) == 0 || (len(args) > 0 && strings.ToLower(args[0]) != "off") {
		if t.callback != nil {
			t.callback.OnSlashCommand("/mode", []string{"yolo"})
		}
		t.mode = "yolo"
		t.add(RoleSystem, "管理员模式开启（yolo 模式，不再逐一询问工具权限）")
		return
	}
	if t.callback != nil {
		t.callback.OnSlashCommand("/mode", []string{"ask"})
	}
	t.mode = "ask"
	t.add(RoleSystem, "管理员模式已关闭，恢复 ask 模式")
}
