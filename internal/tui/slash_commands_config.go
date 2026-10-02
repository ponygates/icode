package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/ponygates/icode/internal/config"
	projectcontext "github.com/ponygates/icode/internal/core/context"
	"github.com/ponygates/icode/internal/core/permission"
)

// doctor runs a quick, non-blocking system diagnostic (Claude Code's /doctor).
// It reports config sanity, configured providers, and memory file paths. No
// network calls are made so it can never hang on a restricted network.
func (t *TUI) doctor() {
	var b strings.Builder
	b.WriteString("iCode 诊断:\n")
	b.WriteString(fmt.Sprintf("  版本:       %s\n", t.version))
	b.WriteString(fmt.Sprintf("  模型:       %s\n", t.model))
	b.WriteString(fmt.Sprintf("  提供商:     %s\n", t.provider))
	b.WriteString(fmt.Sprintf("  安全等级:   %s\n",
		permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
	cfg, err := config.Load()
	if err != nil {
		b.WriteString("  配置:       读取失败 " + err.Error() + "\n")
	} else {
		configured := 0
		for _, pc := range cfg.Providers {
			if pc.APIKey != "" {
				configured++
			}
		}
		b.WriteString(fmt.Sprintf("  已配置 Key: %d 个提供商\n", configured))
	}
	if proj := t.projectMemoryPath(); proj != "" {
		b.WriteString("  项目记忆:   " + proj + "\n")
	}
	if user, err := projectcontext.UserMemoryPath(); err == nil {
		b.WriteString("  用户记忆:   " + user + "\n")
	}
	t.add(RoleSystem, b.String())
}

// memoryCommand shows (or edits) the memory files (Claude Code's /memory).
func (t *TUI) memoryCommand(args []string) {
	proj := t.projectMemoryPath()
	user, _ := projectcontext.UserMemoryPath()
	if len(args) > 0 && strings.ToLower(args[0]) == "edit" {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			if runtime.GOOS == "windows" {
				editor = "notepad"
			} else {
				editor = "vi"
			}
		}
		cmd := exec.Command(editor, proj)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.add(RoleError, "打开编辑器失败: "+err.Error())
		} else {
			t.add(RoleSystem, "[x] 已用 "+editor+" 打开项目记忆 "+proj)
		}
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("记忆文件:\n  项目级: %s\n  用户级: %s\n\n", proj, user))
	if data, err := os.ReadFile(proj); err == nil && len(data) > 0 {
		b.WriteString("── 项目记忆 (ICODE.md) ──\n" + string(data) + "\n")
	} else {
		b.WriteString("（项目记忆为空，用 `# <内容>` 追加，或 `/memory edit` 编辑）\n")
	}
	t.add(RoleSystem, b.String())
}

// permissionsCommand shows the current permission/security configuration.
func (t *TUI) permissionsCommand(args []string) {
	cfg, _ := config.Load()
	var b strings.Builder
	b.WriteString("权限 / 安全:\n")
	b.WriteString(fmt.Sprintf("  当前安全等级: %s\n", permission.SecurityLabel(config.SecurityLevel(t.securityLevel))))
	b.WriteString(fmt.Sprintf("  配置值:       %s\n", t.securityLevel))
	if cfg != nil {
		if len(cfg.Hooks) > 0 {
			b.WriteString("  生命周期钩子:\n")
			for ev, rules := range cfg.Hooks {
				for _, r := range rules {
					b.WriteString(fmt.Sprintf("    %-12s %s\n", ev, r.Command))
				}
			}
		}
	}
	b.WriteString("\n切换安全等级: /security [local|desensitize|local-llm|foreign-llm|unrestricted]\n")
	b.WriteString("工具级允许/拒绝请在桌面端 设置 → 工具权限 中配置。")
	t.add(RoleSystem, b.String())
}

func (t *TUI) configCommand(args []string) {
	if len(args) > 0 && strings.ToLower(args[0]) == "set" {
		if len(args) < 3 {
			t.add(RoleSystem, "用法: /config set <key> <value>\n可配置: theme|lang|security|model|provider")
			return
		}
		key := strings.ToLower(args[1])
		val := strings.Join(args[2:], " ")
		switch key {
		case "theme":
			if val != "auto" && val != "dark" && val != "light" {
				t.add(RoleSystem, "theme 仅支持 auto|dark|light")
				return
			}
			t.theme = val
			t.persistSetting(func(c *config.Config) { c.TUI.Theme = val })
			t.add(RoleSystem, "主题 → "+t.themeName(val))
		case "lang":
			if val != "zh-CN" && val != "zh-TW" && val != "en" {
				t.add(RoleSystem, "lang 仅支持 zh-CN|zh-TW|en")
				return
			}
			t.lang = val
			t.persistSetting(func(c *config.Config) { c.Language = val })
			t.add(RoleSystem, "语言 → "+t.langName(val))
		case "security":
			t.securityLevel = val
			lvl := config.ParseSecurityLevel(val)
			t.persistSetting(func(c *config.Config) { c.SecurityLevel = lvl })
			t.add(RoleSystem, "隐私 → "+permission.SecurityLabel(lvl))
		case "model":
			t.model = val
			t.add(RoleSystem, "模型 → "+val)
		case "provider":
			t.provider = val
			t.add(RoleSystem, "服务商 → "+val)
		default:
			t.add(RoleSystem, "未知配置项: "+key)
			return
		}
		return
	}
	t.configDump()
}

// configDump prints the full settings cheat-sheet (labels localized). Wired
// to the settings panel's 高级配置 row; /config set still edits values.
func (t *TUI) configDump() {
	cfg, _ := config.Load()
	lang := "zh-CN"
	theme := "auto"
	diff := "unified"
	syntax := "on"
	if cfg != nil {
		lang = cfg.Language
		theme = cfg.TUI.Theme
		diff = cfg.TUI.DiffMode
		if !cfg.TUI.SyntaxHL {
			syntax = "off"
		}
	}
	t.add(RoleSystem, fmt.Sprintf("当前设置：\n  模型:   %s\n  服务商: %s\n  模式:   %s\n  语言:   %s\n  主题:   %s\n  Diff:   %s\n  隐私:   %s\n  高亮:   %s\n\n用 `/config set <key> <value>` 或 `/lang` `/theme` `/security` 即时切换。",
		t.model, t.provider, t.modeLabel(t.mode), t.langName(lang), t.themeName(theme), diff,
		permission.SecurityLabel(config.SecurityLevel(t.securityLevel)), syntax))
}

// appendMemory writes a quick memory note, mirroring Claude Code's `#`
// shortcut. Plain `# note` targets the project memory file (./ICODE.md);
// `# user: note` targets the cross-project user memory (~/.icode/).
// The same rule applies in the desktop app so both surfaces stay consistent.
func (t *TUI) appendMemory(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		t.add(RoleSystem, "用法: # <记到项目 ICODE.md>  |  # user: <记到用户级 ~/.icode>")
		return
	}
	lower := strings.ToLower(text)
	var userPrefix string
	if strings.HasPrefix(lower, "user:") {
		userPrefix = "user:"
	} else if strings.HasPrefix(lower, "user：") {
		userPrefix = "user："
	}
	if userPrefix != "" {
		note := strings.TrimSpace(text[len(userPrefix):])
		if err := projectcontext.AppendUserMemory(note); err != nil {
			t.add(RoleError, "追加 memory 失败: "+err.Error())
			return
		}
		path, _ := projectcontext.UserMemoryPath()
		t.add(RoleSystem, fmt.Sprintf("[x] 已记录到用户级 %s", path))
		return
	}
	path, err := projectcontext.AppendProjectMemory(text)
	if err != nil {
		t.add(RoleError, "追加 memory 失败: "+err.Error())
		return
	}
	t.add(RoleSystem, fmt.Sprintf("[x] 已记录到项目 %s", path))
}
