package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ponygates/icode/internal/config"
	i18n "github.com/ponygates/icode/internal/config/i18n"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/permission"
)

// handleSlashConfig dispatches configuration, persistence and the appearance /
// behaviour toggles. It reports whether the command was claimed.
func (t *TUI) handleSlashConfig(cmd string, args []string) bool {
	switch cmd {
	case "/config":
		t.slashConfig(args)
	case "/theme":
		t.slashTheme(args)
	case "/lang":
		t.slashLang(args)
	case "/mouse":
		t.slashMouse()
	case "/multiline":
		t.slashMultiline()
	case "/verbose":
		t.slashVerbose()
	case "/zen":
		t.slashZen()
	case "/vim":
		t.slashVim()
	case "/statusline":
		t.slashStatusline()
	case "/bell":
		t.slashBell()
	case "/security":
		t.slashSecurity(args)
	case "/permissions":
		t.permissionsCommand(args)
	case "/init":
		t.slashInit()
	case "/memory":
		t.memoryCommand(args)
	case "/hooks":
		t.slashHooks(args)
	default:
		return false
	}
	return true
}

func (t *TUI) slashConfig(args []string) {
	if len(args) > 0 {
		t.configCommand(args)
		return
	}
	// opencode-style: bare /config opens the menu panel (↑↓ ←→),
	// same as Ctrl+P / Ctrl+,.
	t.openSettings()
}

func (t *TUI) slashTheme(args []string) {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "auto", "dark", "light":
			t.theme = strings.ToLower(args[0])
			t.persistSetting(func(c *config.Config) { c.TUI.Theme = t.theme })
			t.add(RoleSystem, fmt.Sprintf(t.tstr("theme.set"), t.theme))
		default:
			t.add(RoleSystem, t.tstr("theme.usage"))
		}
		return
	}
	t.add(RoleSystem, t.tstr("theme.usage"))
}

func (t *TUI) slashLang(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "zh-CN", "zh-TW", "en":
			t.lang = args[0]
			// Sync the global translator so cmd-layer i18n.Tr strings
			// (prompts, CLI banners) follow the same locale.
			i18n.T.SetLanguage(i18n.Lang(args[0]))
			t.persistSetting(func(c *config.Config) { c.Language = t.lang })
			t.add(RoleSystem, fmt.Sprintf(t.tstr("lang.set"), t.lang)+
				"\n"+t.tstr("lang.modelNote"))
		default:
			t.add(RoleSystem, t.tstr("lang.usage"))
		}
		return
	}
	cur := t.lang
	if cur == "" {
		cur = "zh-CN"
	}
	t.add(RoleSystem, t.tstr("lang.usage")+"\n"+fmt.Sprintf(t.tstr("lang.current"), cur))
}

func (t *TUI) slashMouse() {
	t.mouseOn = !t.mouseOn
	t.setMouseTracking(t.mouseOn)
	on := t.mouseOn
	t.persistSetting(func(c *config.Config) { c.TUI.Mouse = &on })
	if on {
		t.add(RoleSystem, "🖱 鼠标交互已开启（点击定位/滚轮/滚动条/工具卡折叠/右键粘贴），已记住。Shift+拖动可随时选择文本。")
	} else {
		t.add(RoleSystem, "🖱 鼠标交互已关闭——恢复终端原生拖动选择，已记住。再次 /mouse 开启。")
	}
}

func (t *TUI) slashMultiline() {
	t.multiline = !t.multiline
	if t.multiline {
		t.add(RoleSystem, "✓ 多行输入已开启（Enter=换行，Alt+Enter=发送）")
	} else {
		t.add(RoleSystem, "✓ 多行输入已关闭（Enter=发送）")
	}
}

func (t *TUI) slashVerbose() {
	t.verbose = !t.verbose
	if t.verbose {
		t.add(RoleSystem, "[x] 详细输出已开启（完整工具参数 / 原始 diff）")
	} else {
		t.add(RoleSystem, "[ ] 详细输出已关闭")
	}
}

// slashZen folds all tool cards so only the conversation remains
// (OpenCode parity).
func (t *TUI) slashZen() {
	t.zenMode = !t.zenMode
	t.persistSetting(func(c *config.Config) { c.TUI.Zen = t.zenMode })
	if t.zenMode {
		t.add(RoleSystem, "[x] Zen 模式已开启（工具输出折叠，只留对话；/zen 关闭）")
	} else {
		t.add(RoleSystem, "[ ] Zen 模式已关闭（工具输出完整显示）")
	}
}

func (t *TUI) slashVim() {
	t.vimMode = !t.vimMode
	t.persistSetting(func(c *config.Config) { c.TUI.Vim = t.vimMode })
	if t.vimMode {
		t.vimInsert = true
		t.add(RoleSystem, "[x] Vim 模式已开启（Esc 进入普通模式: h/l/0/$ 移动, x 删字符, dd 删整行, u 撤销, i/a/I/A 插入）")
	} else {
		t.add(RoleSystem, "[ ] Vim 模式已关闭")
	}
}

func (t *TUI) slashStatusline() {
	t.statusVisible = !t.statusVisible
	t.persistSetting(func(c *config.Config) {
		v := t.statusVisible
		c.TUI.ShowStatusLine = &v
	})
	if t.statusVisible {
		t.add(RoleSystem, "[x] 底部状态栏已显示")
	} else {
		t.add(RoleSystem, "[ ] 底部状态栏已隐藏")
	}
}

// slashBell toggles the task-completion bell (Claude Code parity): a turn
// longer than ~30s pings BEL so a backgrounded terminal tab flashes.
func (t *TUI) slashBell() {
	t.bellOn = !t.bellOn
	t.persistSetting(func(c *config.Config) {
		v := t.bellOn
		c.TUI.Bell = &v
	})
	if t.bellOn {
		t.add(RoleSystem, "[x] 任务完成铃声已开启（超过 30s 的长任务结束时响铃提醒；/bell 关闭）")
	} else {
		t.add(RoleSystem, "[ ] 任务完成铃声已关闭")
	}
	t.render()
}

func (t *TUI) slashSecurity(args []string) {
	if len(args) == 0 {
		t.add(RoleSystem, fmt.Sprintf(t.tstr("security.usage"),
			permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
		return
	}
	newLevel := strings.ToLower(args[0])
	valid := map[string]config.SecurityLevel{
		"local":        config.SecLocal,
		"desensitize":  config.SecDesensitize,
		"local-llm":    config.SecLocalLLM,
		"foreign-llm":  config.SecForeignLLM,
		"unrestricted": config.SecUnrestricted,
	}
	level, ok := valid[newLevel]
	if !ok {
		t.add(RoleSystem, fmt.Sprintf(t.tstr("security.usage"),
			permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
		return
	}
	t.securityLevel = newLevel
	t.persistSetting(func(c *config.Config) { c.SecurityLevel = level })
	t.add(RoleSystem, fmt.Sprintf(t.tstr("security.set"),
		permission.SecurityLabel(level)))
}

// slashInit creates the project context file when it is still missing.
func (t *TUI) slashInit() {
	cwd, err := os.Getwd()
	if err != nil {
		t.add(RoleError, err.Error())
		return
	}
	if _, err := os.Stat(filepath.Join(cwd, "ICODE.md")); err == nil {
		t.add(RoleSystem, "ICODE.md 已存在")
		return
	}
	_ = os.WriteFile(filepath.Join(cwd, "ICODE.md"), []byte("# Project Context\n\nEdit this file.\n"), 0o644)
	t.add(RoleSystem, "[x] ICODE.md 已生成")
}

// slashHooks generates (or shows) the user hooks file.
// slashHooks manages the engine's lifecycle hooks (PreToolUse/Stop/…), the
// ones loaded from config.yaml → hooks:. Writes go through persistSetting,
// which fires onConfigChanged — the host callback hot-reloads the engine's
// hook runner (cmd/commands.go), so changes apply without a restart.
func (t *TUI) slashHooks(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "", "list", "ls":
		cfg, err := config.Load()
		if err != nil {
			t.add(RoleError, "配置加载失败: "+err.Error())
			return
		}
		t.add(RoleSystem, hooks.FormatRules(hooks.RulesFromConfig(cfg.Hooks)))
	case "events":
		t.add(RoleSystem, hooks.EventsHelp())
	case "add":
		ev, rule, err := hooks.ParseAddArgs(args[1:])
		if err != nil {
			t.add(RoleError, err.Error())
			return
		}
		t.persistSetting(func(c *config.Config) {
			if c.Hooks == nil {
				c.Hooks = map[string][]config.HookRule{}
			}
			c.Hooks[string(ev)] = append(c.Hooks[string(ev)], config.HookRule{
				Matcher: rule.Matcher, Command: rule.Command, Timeout: rule.Timeout,
			})
		})
		detail := ""
		if rule.Matcher != "" {
			detail = "（匹配 " + rule.Matcher + "）"
		}
		t.add(RoleSystem, fmt.Sprintf("✓ 已添加钩子 %s%s，已即时生效。", ev, detail))
	case "rm", "remove", "del":
		if len(args) < 3 {
			t.add(RoleError, "用法: /hooks rm <事件> <序号>（序号见 /hooks 列表）")
			return
		}
		ev := hooks.NormalizeEvent(args[1])
		if ev == "" {
			t.add(RoleError, fmt.Sprintf("未知事件 %q（/hooks events 查看全部事件）", args[1]))
			return
		}
		n, err := strconv.Atoi(args[2])
		if err != nil || n <= 0 {
			t.add(RoleError, fmt.Sprintf("序号必须是正整数，收到 %q", args[2]))
			return
		}
		var removed string
		t.persistSetting(func(c *config.Config) {
			list := c.Hooks[string(ev)]
			if n > len(list) {
				return
			}
			removed = list[n-1].Command
			c.Hooks[string(ev)] = append(list[:n-1], list[n:]...)
			if len(c.Hooks[string(ev)]) == 0 {
				delete(c.Hooks, string(ev))
			}
		})
		if removed == "" {
			cfg, _ := config.Load()
			count := len(cfg.Hooks[string(ev)])
			t.add(RoleError, fmt.Sprintf("序号越界：%s 共 %d 条钩子", ev, count))
			return
		}
		t.add(RoleSystem, fmt.Sprintf("✓ 已删除 %s 的第 %d 条钩子: %s", ev, n, removed))
	case "reload":
		cfg, err := config.Load()
		if err != nil {
			t.add(RoleError, "配置加载失败: "+err.Error())
			return
		}
		total := 0
		for _, list := range cfg.Hooks {
			total += len(list)
		}
		if t.onConfigChanged != nil {
			t.onConfigChanged(cfg)
		}
		t.add(RoleSystem, fmt.Sprintf("✓ 已从 config.yaml 重载 %d 条钩子并注入引擎。", total))
	default:
		t.add(RoleError, fmt.Sprintf("未知子命令 %q。用法: /hooks [list|events|add|rm|reload]", args[0]))
	}
}
