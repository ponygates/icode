package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "查看或修改 iCode 设置",
	Long: `Show the settings panel, or set a value:
  icode config                 show settings panel
  icode config model <id>      set default model
  icode config provider <name> set default provider
  icode config mode <m>        set permission mode (plan|agent|yolo|default)
  icode config lang <l>        set language (zh-CN|zh-TW|en)
  icode config theme <t>       set theme (auto|dark|light)
  icode config diff <d>        set diff mode (unified|split)
  icode config syntax <s>      set syntax highlight (on|off)
  icode config key <p> <key>   set API key for a provider
  icode config voice provider <name>     set voice provider (zhipu|baidu|xfyun)
  icode config voice baidu <key> <secret>   set Baidu voice API credentials
  icode config voice xfyun <appid> <key> <secret>  set iFlytek voice API credentials
  icode config voice            show current voice settings
  icode config model           list custom models
  icode config model add <p> <id> [name]   add a custom model
  icode config model rm <id>   remove a custom model
  icode config providers       list providers and key status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadOrCreate()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		switch {
		case len(args) == 0:
			fmt.Print(renderConfigPanel(cfg))
			return nil
		case len(args) == 1 && args[0] == "providers":
			return runConfigProviders(cmd, cfg)
		case len(args) >= 1 && strings.ToLower(args[0]) == "why":
			return runConfigWhy(cfg, args[1:])
		case len(args) >= 1 && strings.ToLower(args[0]) == "mcp":
			return runConfigMCP(cmd, cfg, args[1:])
		case len(args) >= 1 && strings.ToLower(args[0]) == "model":
			return runConfigModel(cfg, args[1:])
		case len(args) >= 1 && strings.ToLower(args[0]) == "key":
			return runConfigKey(cfg, args[1:])
		case len(args) >= 1 && strings.ToLower(args[0]) == "voice":
			return runConfigVoice(cfg, args[1:])
		case len(args) >= 2:
			return runConfigSet(cmd, cfg, args[0], strings.Join(args[1:], " "))
		default:
			return fmt.Errorf("usage: icode config [key] [value]")
		}
	},
}

// configKeyPath maps a `icode config <key>` shorthand to the dotted path the
// settings layers use, so `why` and the managed-policy check speak the same
// language as the config files.
func configKeyPath(key string) string {
	switch strings.ToLower(key) {
	case "model":
		return "defaults.model"
	case "provider":
		return "defaults.provider"
	case "mode":
		return "defaults.mode"
	case "lang", "language":
		return "language"
	case "theme":
		return "tui.theme"
	case "diff":
		return "tui.diff_mode"
	case "syntax":
		return "tui.syntax_highlight"
	case "voice.provider":
		return "voice.provider"
	default:
		return ""
	}
}

// runConfigWhy answers "where did this value come from?" — defaults, user,
// project, env, cli or managed. With no argument it prints the whole map.
func runConfigWhy(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		report := cfg.LayerReport()
		if len(report) == 0 {
			fmt.Println("没有加载任何设置文件，全部使用默认值。")
			return nil
		}
		keys := make([]string, 0, len(report))
		for k := range report {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys)+2)
		lines = append(lines, "设置来源（低到高：user < project < env < cli < managed）：")
		for _, k := range keys {
			lines = append(lines, fmt.Sprintf("  %s%s%s", k, strings.Repeat(" ", max(1, 40-len(k))), report[k]))
		}
		if pinned := cfg.ManagedKeys(); len(pinned) > 0 {
			lines = append(lines, fmt.Sprintf("托管策略锁定 %d 项（不可被本地设置覆盖）：%s", len(pinned), strings.Join(pinned, ", ")))
		}
		fmt.Println(strings.Join(lines, "\n"))
		return nil
	}
	key := strings.ToLower(args[0])
	path := configKeyPath(key)
	if path == "" {
		path = key
	}
	layer := cfg.SettingLayer(path)
	if layer == "" {
		fmt.Printf("%s (%s)：来自默认值\n", key, path)
		return nil
	}
	fmt.Printf("%s (%s)：来自 %s 层\n", key, path, layer)
	return nil
}

func runConfigSet(cmd *cobra.Command, cfg *config.Config, key, value string) error {
	key = strings.ToLower(key)
	if path := configKeyPath(key); path != "" && cfg.IsManaged(path) {
		// The write would look successful and revert on the next Load — so it
		// is refused out loud instead.
		return fmt.Errorf("%s 已由托管策略锁定（%s），iCode 不会覆盖它；要改请改策略文件 %v",
			key, path, config.ManagedPaths())
	}
	switch key {
	case "model":
		cfg.Defaults.Model = value
	case "provider":
		cfg.Defaults.Provider = value
	case "mode":
		switch strings.ToLower(value) {
		case "plan", "agent", "yolo", "default":
			cfg.Defaults.Mode = strings.ToLower(value)
		default:
			return fmt.Errorf("mode must be one of: plan, agent, yolo, default")
		}
	case "lang", "language":
		switch value {
		case "zh-CN", "zh-TW", "en":
			cfg.Language = value
		default:
			return fmt.Errorf("language must be one of: zh-CN, zh-TW, en")
		}
	case "theme":
		switch strings.ToLower(value) {
		case "auto", "dark", "light":
			cfg.TUI.Theme = strings.ToLower(value)
		default:
			return fmt.Errorf("theme must be one of: auto, dark, light")
		}
	case "diff":
		switch strings.ToLower(value) {
		case "unified", "split":
			cfg.TUI.DiffMode = strings.ToLower(value)
		default:
			return fmt.Errorf("diff must be one of: unified, split")
		}
	case "syntax":
		switch strings.ToLower(value) {
		case "on", "true", "1", "yes":
			cfg.TUI.SyntaxHL = true
		case "off", "false", "0", "no":
			cfg.TUI.SyntaxHL = false
		default:
			return fmt.Errorf("syntax must be one of: on, off")
		}
	case "voice.provider":
		switch strings.ToLower(value) {
		case "zhipu", "baidu", "xfyun":
			cfg.Voice.Provider = strings.ToLower(value)
		default:
			return fmt.Errorf("voice.provider must be one of: zhipu, baidu, xfyun")
		}
	default:
		return fmt.Errorf("unknown setting: %s (try: model, provider, mode, lang, theme, diff, syntax, voice.provider)", key)
	}

	if err := cfg.Save(config.DefaultPath()); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("✓ Saved %s = %s  →  %s\n", key, value, config.DefaultPath())
	return nil
}

func runConfigProviders(cmd *cobra.Command, cfg *config.Config) error {
	fmt.Println()
	fmt.Printf("  %-14s %-10s %s\n", "Provider", "Status", "Base URL")
	fmt.Println("  " + strings.Repeat("─", 60))
	order := []string{"deepseek", "zhipu", "kimi", "volcengine", "tencent", "huawei", "scnet", "openrouter", "anthropic"}
	for _, name := range order {
		p, ok := cfg.Providers[name]
		if !ok {
			continue
		}
		status := "no key"
		if p.APIKey != "" {
			status = "● ready"
		} else if p.Disabled {
			status = "disabled"
		}
		base := p.APIBase
		if base == "" {
			base = "(default)"
		}
		fmt.Printf("  %-14s %-10s %s\n", name, status, base)
	}
	fmt.Println()
	return nil
}

// runConfigModel manages user-defined model entries.
//
//	icode config model                       → list custom/override models
//	icode config model add <p> <id> [name]   → add a custom model
//	icode config model rm <id>               → remove a custom model (id = provider/model_id)
func runConfigModel(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		if len(cfg.Models) == 0 {
			fmt.Println("\n  No custom models yet. Add one with:")
			fmt.Println("    icode config model add <provider> <model_id> [display_name]")
			return nil
		}
		fmt.Println()
		fmt.Printf("  %-26s %-14s %-10s %s\n", "ID", "Provider", "Model", "Name")
		fmt.Println("  " + strings.Repeat("─", 70))
		for _, m := range cfg.Models {
			name := m.Name
			if name == "" {
				name = "—"
			}
			fmt.Printf("  %-26s %-14s %-10s %s\n", m.ID, m.Provider, m.ModelID, name)
		}
		fmt.Println()
		return nil
	}

	switch strings.ToLower(args[0]) {
	case "add":
		if len(args) < 3 {
			return fmt.Errorf("usage: icode config model add <provider> <model_id> [display_name]")
		}
		provider := args[1]
		modelID := args[2]
		name := modelID
		if len(args) >= 4 {
			name = strings.Join(args[3:], " ")
		}
		m := config.ModelCfg{
			Provider: provider,
			ModelID:  modelID,
			Name:     name,
			Custom:   true,
		}
		m.ID = config.ModelKey(provider, modelID)
		cfg.UpsertModel(m)
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("✓ Added custom model %s (provider=%s)\n", m.ID, provider)
		return nil
	case "rm", "remove", "del", "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: icode config model rm <provider/model_id>")
		}
		id := args[1]
		if !cfg.DeleteModel(id) {
			return fmt.Errorf("model %q not found", id)
		}
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("✓ Removed model %s\n", id)
		return nil
	default:
		return fmt.Errorf("unknown model subcommand: %s (try: add, rm)", args[0])
	}
}

// runConfigKey sets (or shows) the API key for a provider.
//
//	icode config key                 → list key status (same as `providers`)
//	icode config key <p> <key>       → save API key for provider <p>
func runConfigKey(cfg *config.Config, args []string) error {
	if len(args) < 1 || args[0] == "" {
		return runConfigProviders(nil, cfg)
	}
	if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
		return fmt.Errorf("usage: icode config key <provider> <apikey>")
	}
	provider := args[0]
	key := strings.Join(args[1:], " ")
	pc := cfg.Providers[provider]
	pc.APIKey = key
	cfg.Providers[provider] = pc
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("✓ Saved API key for %s  →  %s\n", provider, config.DefaultPath())
	return nil
}

// runConfigVoice handles `icode config voice ...` commands.
func runConfigVoice(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		// Show current voice settings
		fmt.Println()
		fmt.Println("  语音输入设置")
		fmt.Println("  " + strings.Repeat("─", 50))
		fmt.Printf("  提供商     : %s\n", cfg.Voice.Provider)
		fmt.Printf("  百度 API Key: %s\n", maskString(cfg.Voice.BaiduAPIKey))
		fmt.Printf("  百度 Secret : %s\n", maskString(cfg.Voice.BaiduSecretKey))
		fmt.Printf("  讯飞 App ID : %s\n", cfg.Voice.IFlytekAppID)
		fmt.Printf("  讯飞 API Key: %s\n", maskString(cfg.Voice.IFlytekAPIKey))
		fmt.Printf("  讯飞 Secret : %s\n", maskString(cfg.Voice.IFlytekAPISecret))
		fmt.Println()
		fmt.Println("  用法:")
		fmt.Println("    icode config voice provider <name>         设置提供商 (zhipu|baidu|xfyun)")
		fmt.Println("    icode config voice baidu <key> <secret>    设置百度语音 API 凭证")
		fmt.Println("    icode config voice xfyun <appid> <key> <secret>  设置讯飞语音 API 凭证")
		fmt.Println()
		return nil
	}

	subcmd := strings.ToLower(args[0])
	switch subcmd {
	case "provider":
		if len(args) < 2 {
			return fmt.Errorf("用法: icode config voice provider <name> (zhipu|baidu|xfyun)")
		}
		provider := strings.ToLower(args[1])
		switch provider {
		case "zhipu", "baidu", "xfyun":
			cfg.Voice.Provider = provider
		default:
			return fmt.Errorf("提供商必须是: zhipu, baidu, xfyun")
		}
	case "baidu":
		if len(args) < 3 {
			return fmt.Errorf("用法: icode config voice baidu <api_key> <secret_key>")
		}
		cfg.Voice.BaiduAPIKey = args[1]
		cfg.Voice.BaiduSecretKey = args[2]
	case "xfyun":
		if len(args) < 4 {
			return fmt.Errorf("用法: icode config voice xfyun <app_id> <api_key> <api_secret>")
		}
		cfg.Voice.IFlytekAppID = args[1]
		cfg.Voice.IFlytekAPIKey = args[2]
		cfg.Voice.IFlytekAPISecret = args[3]
	default:
		return fmt.Errorf("未知命令: %s (可用: provider, baidu, xfyun)", subcmd)
	}

	if err := cfg.Save(config.DefaultPath()); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	fmt.Printf("✓ 语音设置已保存  →  %s\n", config.DefaultPath())
	return nil
}

func maskString(s string) string {
	if s == "" {
		return "(未设置)"
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
func runConfigMCP(cmd *cobra.Command, cfg *config.Config, args []string) error {
	if len(args) == 0 {
		if len(cfg.MCP) == 0 {
			fmt.Println("\n  No MCP servers configured.")
			fmt.Println("  Add one with:")
			fmt.Println("    icode config mcp add <name> --command <cmd> [--mcp-args \"<args>\"] [--mcp-type stdio|sse]")
			return nil
		}
		fmt.Println()
		fmt.Printf("  %-20s %-8s %-6s %s\n", "Name", "Type", "Enabled", "Command")
		fmt.Println("  " + strings.Repeat("─", 65))
		for _, m := range cfg.MCP {
			enabled := "✓"
			if !m.Enabled {
				enabled = "✗"
			}
			cmdStr := m.Command
			if len(m.Args) > 0 {
				cmdStr += " " + strings.Join(m.Args, " ")
			}
			fmt.Printf("  %-20s %-8s %-6s %s\n", m.Name, m.Type, enabled, cmdStr)
		}
		fmt.Println()
		return nil
	}
	switch strings.ToLower(args[0]) {
	case "add":
		name := ""
		if len(args) >= 2 {
			name = args[1]
		}
		command, _ := cmd.Flags().GetString("command")
		mcpArgs, _ := cmd.Flags().GetString("mcp-args")
		mcpType, _ := cmd.Flags().GetString("mcp-type")
		mcpURL, _ := cmd.Flags().GetString("mcp-url")
		enabled, _ := cmd.Flags().GetBool("mcp-enabled")
		if name == "" {
			return fmt.Errorf("usage: icode config mcp add <name> --command <cmd> [--mcp-args \"<args>\"]")
		}
		if command == "" {
			return fmt.Errorf("--command flag is required")
		}
		var argsList []string
		if mcpArgs != "" {
			argsList = strings.Fields(mcpArgs)
		}
		m := config.MCPServerCfg{
			Name:    name,
			Type:    mcpType,
			Command: command,
			Args:    argsList,
			URL:     mcpURL,
			Enabled: enabled,
		}
		cfg.UpsertMCP(m)
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("✓ MCP server %q saved  →  %s\n", name, config.DefaultPath())
		return nil
	case "rm", "remove", "del", "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: icode config mcp rm <name>")
		}
		name := args[1]
		cfg.RemoveMCP(name)
		if err := cfg.Save(config.DefaultPath()); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("✓ Removed MCP server %q\n", name)
		return nil
	default:
		return fmt.Errorf("unknown mcp subcommand: %s (try: add, rm)", args[0])
	}
}

// renderConfigPanel builds a boxed, colored settings panel (plain text so it
// also works in non-TTY / logged output; ANSI is applied via fmt).
func renderConfigPanel(cfg *config.Config) string {
	d := cfg.Defaults
	var b strings.Builder
	// Display-width-aware pad so CJK characters (width 2) align the border.
	runeW := func(s string) int {
		w := 0
		for _, r := range s {
			if r >= 0x1100 && (r <= 0x115F || r >= 0x2E80 && r <= 0xA4CF ||
				r >= 0xAC00 && r <= 0xD7A3 || r >= 0xF900 && r <= 0xFAFF ||
				r >= 0xFE30 && r <= 0xFE4F || r >= 0xFF00 && r <= 0xFF60 ||
				r >= 0xFFE0 && r <= 0xFFE6 || r >= 0x3000 && r <= 0x303E) {
				w += 2
			} else {
				w++
			}
		}
		return w
	}
	const interior = 56
	pad := func(s string) string {
		gap := interior - runeW(s)
		if gap <= 0 {
			return s
		}
		return s + strings.Repeat(" ", gap)
	}
	row := func(s string) string { return "  │ " + pad(s) + " │" }
	border := "  ╭" + strings.Repeat("─", interior) + "╮"
	foot := "  ╰" + strings.Repeat("─", interior) + "╯"

	b.WriteString("\n")
	b.WriteString(border + "\n")
	b.WriteString(row("◆ iCode Settings") + "\n")
	b.WriteString(row("") + "\n")
	b.WriteString(row("Default model :   "+d.Model) + "\n")
	b.WriteString(row("Default provider:  "+d.Provider) + "\n")
	b.WriteString(row("Permission mode :  "+d.Mode) + "\n")
	b.WriteString(row("Language        :  "+cfg.Language) + "\n")
	b.WriteString(row("Theme           :  "+cfg.TUI.Theme) + "\n")
	b.WriteString(row("Diff mode       :  "+cfg.TUI.DiffMode) + "\n")
	b.WriteString(row("Syntax highlight:  "+boolToStr(cfg.TUI.SyntaxHL)) + "\n")
	b.WriteString(row("Server          :  "+serverStr(cfg)) + "\n")
	b.WriteString(row("") + "\n")
	b.WriteString(row("Languages: zh-CN / zh-TW / en") + "\n")
	b.WriteString(row("Themes:   auto / dark / light") + "\n")
	b.WriteString(row("") + "\n")
	b.WriteString(row("API key  : icode config key <provider> <key>") + "\n")
	b.WriteString(row("Model    : icode config model add <p> <id> [name]") + "\n")
	b.WriteString(row("Change   : icode config <model|provider|mode|lang|theme|diff|syntax> <value>") + "\n")
	b.WriteString(row("TUI live : /lang <zh-CN|zh-TW|en>  ·  /theme <auto|dark|light>") + "\n")
	b.WriteString(foot + "\n")
	b.WriteString("\n")
	return b.String()
}

func boolToStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func serverStr(cfg *config.Config) string {
	if cfg.Server.Port == 0 {
		return "off (auto)"
	}
	return fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
}

func init() {
	execCmd.Flags().StringP("prompt", "p", "", "The prompt to execute")
	execCmd.Flags().StringP("file", "f", "", "Read prompt from file")
	execCmd.Flags().IntP("max-turns", "t", 10, "Maximum conversation turns")
	execCmd.Flags().String("output-format", "text", "Output format: text | json | stream-json (NDJSON)")

	chatCmd.Flags().StringP("provider", "p", "", "LLM provider to use")
	chatCmd.Flags().StringP("model", "m", "", "Model ID to use")
	chatCmd.Flags().String("mode", "agent", "Interaction mode (plan/agent/yolo)")

	authCmd.Flags().BoolP("list", "L", false, "List configured providers")
	authCmd.Flags().String("provider", "", "Provider name")
	authCmd.Flags().String("key", "", "API key")
	authCmd.Flags().Bool("show", false, "Show masked API key for a provider")
	authCmd.Flags().Bool("reveal", false, "With --show, print the raw key (it will be visible in the terminal)")
	authCmd.Flags().Bool("no-verify", false, "With --key, skip the connectivity check")

	loginCmd.Flags().Bool("no-verify", false, "Save without a connectivity check")
	loginCmd.Flags().Bool("oauth", false, "用订阅账号登录（OAuth 授权码 + PKCE，需先在 providers.<name>.oauth 配置厂商端点）")
	authCmd.Flags().Bool("delete", false, "Remove configuration for a provider")

	modelCmd.Flags().BoolP("refresh", "r", false, "Refresh model list from all providers")
	modelCmd.Flags().String("search", "", "Filter models by name")

	configCmd.Flags().String("command", "", "MCP server command")
	configCmd.Flags().String("mcp-args", "", "MCP server arguments (space-separated)")
	configCmd.Flags().String("mcp-type", "stdio", "MCP server type (stdio|sse)")
	configCmd.Flags().String("mcp-url", "", "MCP server URL (for SSE)")
	configCmd.Flags().Bool("mcp-enabled", true, "Enable MCP server on startup")
}
