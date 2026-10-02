package slashui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
)

func cmdWhoami(st *State) Result {
	cwd := st.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	sec := st.Security
	if sec == "" {
		if c, err := config.Load(); err == nil && c.SecurityLevel != "" {
			sec = string(c.SecurityLevel)
		} else {
			sec = "local"
		}
	}
	return ok(fmt.Sprintf("iCode %s\n  Model:     %s\n  Provider:  %s\n  Security:  %s\n  CWD:       %s",
		shortStr(st.Version, "dev"), shortStr(st.Model, "未设置"), shortStr(st.Provider, "未设置"),
		permission.SecurityLabel(config.SecurityLevel(sec)), shortDir(cwd)))
}

func cmdStatus(st *State) Result {
	var b strings.Builder
	b.WriteString("iCode 系统状态\n\n")
	cfg, _ := config.Load()
	if cfg != nil {
		b.WriteString(fmt.Sprintf("语言: %s\n", cfg.Language))
		b.WriteString(fmt.Sprintf("默认模型: %s\n", cfg.Defaults.Model))
		b.WriteString(fmt.Sprintf("默认 Provider: %s\n", cfg.Defaults.Provider))
		b.WriteString(fmt.Sprintf("权限模式: %s\n\n", cfg.Defaults.Mode))
		b.WriteString("API Key 状态:\n")
		for name, pc := range cfg.Providers {
			status := "✓ 已配置"
			if pc.APIKey == "" {
				status = "✗ 未配置"
			}
			key := pc.APIKey
			if len(key) > 8 {
				key = key[:4] + "..." + key[len(key)-4:]
			} else if key != "" {
				key = "****"
			}
			b.WriteString(fmt.Sprintf("  %-14s %-10s %s\n", name, status, key))
		}
	}
	b.WriteString("\n活跃会话: ")
	if st.SessionID != "" {
		sid := st.SessionID
		if len(sid) > 8 {
			sid = sid[:8] + "..."
		}
		b.WriteString(sid)
	} else {
		b.WriteString("无")
	}
	return ok(b.String())
}

func cmdConfig(args []string) Result {
	if len(args) >= 2 && strings.ToLower(args[0]) == "set" {
		return cmdConfigSet(args[1:])
	}
	cfg, err := config.LoadOrCreate()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	var b strings.Builder
	b.WriteString("iCode 配置\n\n")
	b.WriteString(fmt.Sprintf("语言:          %s\n", cfg.Language))
	b.WriteString(fmt.Sprintf("默认模型:      %s\n", cfg.Defaults.Model))
	b.WriteString(fmt.Sprintf("默认 Provider: %s\n", cfg.Defaults.Provider))
	b.WriteString(fmt.Sprintf("权限模式:      %s\n", cfg.Defaults.Mode))
	b.WriteString(fmt.Sprintf("安全等级:      %s\n", cfg.SecurityLevel))
	if cfg.Defaults.OutputStyle != "" {
		b.WriteString(fmt.Sprintf("输出风格:      %s\n", cfg.Defaults.OutputStyle))
	}
	if len(cfg.Defaults.ExtraDirs) > 0 {
		b.WriteString(fmt.Sprintf("额外工作目录:  %s\n", strings.Join(cfg.Defaults.ExtraDirs, ", ")))
	}
	if len(cfg.Providers) > 0 {
		b.WriteString("\n提供商:\n")
		for name, pc := range cfg.Providers {
			key := ""
			if pc.APIKey != "" {
				key = "（已配置 Key）"
			}
			b.WriteString(fmt.Sprintf("  %s %s\n", name, key))
		}
	}
	b.WriteString("\n用法: /config set <key> <value>")
	b.WriteString("\n可设置: model, provider, mode, security, lang, output-style, theme")
	b.WriteString("\n配置文件: " + config.DefaultPath())
	return ok(b.String())
}

func cmdConfigSet(args []string) Result {
	if len(args) < 2 {
		return errf("用法: /config set <key> <value>")
	}
	key := strings.ToLower(args[0])
	value := strings.Join(args[1:], " ")
	cfg, err := config.LoadOrCreate()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	switch key {
	case "model":
		cfg.Defaults.Model = value
	case "provider":
		cfg.Defaults.Provider = value
	case "mode":
		valid := map[string]bool{"plan": true, "agent": true, "ask": true, "auto": true, "yolo": true}
		if !valid[strings.ToLower(value)] {
			return errf("无效模式: %s（可选 plan/agent/ask/auto/yolo）", value)
		}
		cfg.Defaults.Mode = strings.ToLower(value)
	case "security":
		cfg.SecurityLevel = config.SecurityLevel(value)
	case "lang", "language":
		valid := map[string]bool{"zh-CN": true, "zh-TW": true, "en": true}
		if !valid[value] {
			return errf("无效语言: %s（可选 zh-CN/zh-TW/en）", value)
		}
		cfg.Language = value
	case "output-style", "outputstyle":
		valid := map[string]bool{"concise": true, "normal": true, "verbose": true}
		if !valid[value] {
			return errf("无效风格: %s（可选 concise/normal/verbose）", value)
		}
		cfg.Defaults.OutputStyle = value
	case "theme":
		valid := map[string]bool{"auto": true, "dark": true, "light": true}
		if !valid[value] {
			return errf("无效主题: %s（可选 auto/dark/light）", value)
		}
		cfg.TUI.Theme = value
	default:
		return errf("未知配置项: %s（可设置: model, provider, mode, security, lang, output-style, theme）", key)
	}
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return errf("保存失败: %v", err)
	}
	return ok(fmt.Sprintf("已设置 %s = %s", key, value))
}

func cmdModels(b *Backend, args []string) Result {
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "add":
			if b == nil || b.RegisterCustomModel == nil {
				return errf("当前界面不支持添加模型。请用 `icode config model add <provider> <model_id> [name]` 或桌面端设置。")
			}
			if len(args) < 3 {
				return errf("用法: /models add <provider> <model_id> [name]")
			}
			name := args[2]
			if len(args) >= 4 {
				name = strings.Join(args[3:], " ")
			}
			if msg := b.RegisterCustomModel(args[1], args[2], name); msg != "" {
				return errf("%s", msg)
			}
			return ok(fmt.Sprintf("✓ 已添加自定义模型 %s（%s / %s）", config.ModelKey(args[1], args[2]), args[1], name))
		case "rm", "remove", "del", "delete":
			if b == nil || b.RemoveCustomModel == nil {
				return errf("当前界面不支持删除模型。请用 `icode config model rm <id>` 或桌面端设置。")
			}
			if len(args) < 2 {
				return errf("用法: /models rm <id>（id 形如 provider/model_id）")
			}
			if msg := b.RemoveCustomModel(args[1]); msg != "" {
				return errf("%s", msg)
			}
			return ok(fmt.Sprintf("✓ 已移除自定义模型 %s", args[1]))
		}
	}

	cfg, err := config.Load()
	if err != nil || len(cfg.Models) == 0 {
		return ok("暂无自定义模型。\n用 `/models add <provider> <model_id> [name]` 新增。")
	}
	var out strings.Builder
	out.WriteString("自定义模型:\n")
	for _, m := range cfg.Models {
		name := m.Name
		if name == "" {
			name = m.ModelID
		}
		out.WriteString(fmt.Sprintf("  %-26s %s / %s\n", m.ID, m.Provider, name))
	}
	return ok(out.String())
}

func cmdModel(b *Backend, st *State, args []string) Result {
	if len(args) == 0 {
		return ok("当前模型: " + shortStr(st.Model, "未设置") + "\n用法: /model <模型ID>（如 /model gpt-4o）")
	}
	id := args[0]
	_ = b
	persistSetting(func(c *config.Config) { c.Defaults.Model = id })
	return Result{Output: "模型 → " + id, Model: id}
}

func cmdProvider(st *State, args []string) Result {
	if len(args) == 0 {
		return ok("当前 Provider: " + shortStr(st.Provider, "未设置") + "\n用法: /provider <名称>")
	}
	persistSetting(func(c *config.Config) { c.Defaults.Provider = args[0] })
	return Result{Output: "服务商 → " + args[0], Provider: args[0]}
}

func cmdMode(b *Backend, st *State, args []string) Result {
	if len(args) == 0 {
		return ok("当前模式: " + shortStr(st.Mode, "agent") + "\n用法: /mode <agent|plan|yolo|auto|ask>")
	}
	want := strings.ToLower(args[0])
	valid := map[string]bool{"agent": true, "plan": true, "yolo": true, "auto": true, "ask": true}
	if !valid[want] {
		return errf("无效模式: %s（可选 agent/plan/yolo/auto/ask）", args[0])
	}
	if b != nil && b.Gate != nil {
		b.Gate.SetMode(permission.Mode(want))
	}
	persistSetting(func(c *config.Config) { c.Defaults.Mode = want })
	return Result{Output: "模式 → " + want, Mode: want}
}

// cmdThinking toggles Anthropic extended thinking at runtime and persists it
// to config (Defaults.ThinkingTokens), matching the CLI /thinking command.
func cmdThinking(b *Backend, args []string) Result {
	if b == nil || b.Engine == nil {
		return ok("引擎未初始化。")
	}
	if len(args) == 0 {
		if bg := b.Engine.ThinkingBudget(); bg > 0 {
			return ok(fmt.Sprintf("当前 extended thinking 已开启，预算 %d tokens。用法: /thinking <on|off|<tokens>>", bg))
		}
		return ok("当前 extended thinking 已关闭。用法: /thinking <on|off|<tokens>>（如 /thinking 4096，对 Anthropic 模型生效）")
	}
	budget := 0
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "on":
		budget = 4096
	case "off", "0":
		budget = 0
	default:
		v, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil || v < 1024 {
			return errf("无效预算（至少 1024 tokens）。用法: /thinking <on|off|<tokens>>")
		}
		budget = v
	}
	b.Engine.SetThinking(budget)
	persistSetting(func(c *config.Config) { c.Defaults.ThinkingTokens = budget })
	if budget > 0 {
		return ok(fmt.Sprintf("✓ extended thinking 已开启（预算 %d tokens，对 Anthropic 模型生效，已持久化）", budget))
	}
	return ok("✓ extended thinking 已关闭（已持久化）。")
}

// cmdPreset applies a one-shot execution profile (Reasonix 执行设定 parity):
// light / balanced / deliver — each toggles permission mode + thinking in one
// command instead of the user composing /mode + /thinking by hand.
func cmdPreset(b *Backend, st *State, args []string) Result {
	if len(args) == 0 {
		return ok("用法: /preset <light|balanced|deliver>\n  light     轻量（auto 模式 + 关闭思考，最快最省 token）\n  balanced  均衡（auto 模式，默认思考）\n  deliver   交付（agent 模式 + 开启思考，质量优先）")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "light":
		cmdMode(b, st, []string{"auto"})
		cmdThinking(b, []string{"off"})
		return ok("✓ 执行设定 = light（轻量）：auto 模式 + 关闭 extended thinking。适合简单任务与省 token。")
	case "balanced":
		cmdMode(b, st, []string{"auto"})
		return ok("✓ 执行设定 = balanced（均衡）：auto 模式，思考保持当前设置。")
	case "deliver":
		cmdMode(b, st, []string{"agent"})
		cmdThinking(b, []string{"on"})
		return ok("✓ 执行设定 = deliver（交付）：agent 模式 + 开启 extended thinking。适合复杂重构与高质量交付。")
	default:
		return errf("无效执行设定: %s（可选 light|balanced|deliver）", args[0])
	}
}

// cmdModeShortcut maps the mode-shortcut commands to /mode values:
// /plan → plan, /ask → ask, /debug → agent (the closest "hands-on debugging"
// mode in iCode's agent/plan/yolo/auto/ask vocabulary).
func cmdModeShortcut(b *Backend, st *State, cmd string) Result {
	want := strings.TrimPrefix(cmd, "/")
	if want == "debug" {
		want = "agent"
	}
	return cmdMode(b, st, []string{want})
}

func cmdOutputStyle(b *Backend, args []string) Result {
	if len(args) == 0 {
		cur := "normal"
		if c, err := config.Load(); err == nil && c.Defaults.OutputStyle != "" {
			cur = c.Defaults.OutputStyle
		}
		return ok("当前风格: " + cur + "\n用法: /output-style <concise|normal|verbose>")
	}
	style := strings.ToLower(args[0])
	if style != "concise" && style != "normal" && style != "verbose" {
		return errf("无效风格: %s（可选 concise|normal|verbose）", args[0])
	}
	cfg, err := config.Load()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	cfg.Defaults.OutputStyle = style
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return errf("保存配置失败: %v", err)
	}
	if b != nil && b.Engine != nil {
		b.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
		return ok("输出风格已设为 " + style + "（已即时生效并持久化）")
	}
	return ok("输出风格已设为 " + style + "（已持久化，重启会话后生效）")
}

func cmdLang(args []string, b *Backend) Result {
	if len(args) == 0 {
		return ok("用法: /lang <zh-CN|zh-TW|en>。UI 与模型输出同步切换语言。")
	}
	switch args[0] {
	case "zh-CN", "zh-TW", "en":
		var updated *config.Config
		persistSetting(func(c *config.Config) { c.Language = args[0]; updated = c })
		// Live-refresh the engine so the language directive (replies,
		// reasoning, code comments) reaches the model without a restart.
		if b != nil && b.Engine != nil && updated != nil {
			b.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(updated))
		}
		return ok("语言已设为 " + args[0] + "，模型回复/思考/注释语言已同步生效。")
	default:
		return errf("无效语言: %s（可选 zh-CN/zh-TW/en）", args[0])
	}
}
