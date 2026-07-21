package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/config/i18n"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/searchreplace"
	"github.com/ponygates/icode/internal/core/todo"
	"github.com/ponygates/icode/internal/llm/provider"
	"github.com/ponygates/icode/internal/server"
	"github.com/ponygates/icode/internal/tui"
	"github.com/ponygates/icode/internal/types"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: i18n.Tr("cmd.chat.desc"),
	Long:  `Start an interactive AI coding session with multi-turn conversation, tool use, and real-time streaming.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, _ := cmd.Flags().GetString("provider")
		model, _ := cmd.Flags().GetString("model")
		mode, _ := cmd.Flags().GetString("mode")
		return startChat(provider, model, mode)
	},
}

// startChat boots the interactive TUI session. It is shared by both the
// `chat` subcommand and the default behaviour when iCode is launched with no
// arguments (e.g. by double-clicking the executable), so a bare `icode`
// opens the chat immediately instead of printing help and closing the window.
func startChat(provider, model, mode string) error {
	// Load persisted settings; reuse the same config object to also check the key.
	cfg, _ := config.Load()

	// Fall back to persisted settings when flags are not supplied.
	if cfg != nil {
		if model == "" && cfg.Defaults.Model != "" {
			model = cfg.Defaults.Model
		}
		if provider == "" && cfg.Defaults.Provider != "" {
			provider = cfg.Defaults.Provider
		}
		if mode == "" && cfg.Defaults.Mode != "" {
			mode = cfg.Defaults.Mode
		}
	}
	if model == "" {
		model = "openrouter/free"
	}
	if provider == "" {
		provider = "openrouter"
	}
	if mode == "" {
		mode = "agent"
	}

	// Friendly hint when the active provider has no API key configured.
	if cfg != nil {
		if k := cfg.APIKey(provider); k == "" {
			fmt.Fprintf(os.Stdout, "⚠ 尚未配置 %s 的 API Key。\n   配置命令：icode auth set --provider %s --key <YOUR_KEY>\n   或在桌面端 ⚙ 设置 → API 密钥\n\n", provider, provider)
		}
	}

	// Try to bootstrap the full app with real backends
	fmt.Fprintf(os.Stdout, "Initializing iCode backend...\n")
	a, err := app.Bootstrap()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: bootstrap failed: %v\n", err)
	}

	// Build the callback bridge
	cb := &chatCallback{app: a}

	// Create TUI with backend callbacks
	tuiLang, tuiTheme := "", ""
	if cfg != nil {
		tuiLang = cfg.Language
		tuiTheme = cfg.TUI.Theme
	}
	t := tui.New(tui.Config{
		Mode:     tui.Mode(mode),
		Model:    model,
		Provider: provider,
		Lang:     tuiLang,
		Theme:    tuiTheme,
		Callback: cb,
	})

	// Wire TUI stream writer back to callback
	cb.tui = t

	// In the CLI, permission approvals are resolved interactively by the
	// TUI prompt (agent mode). The engine calls this handler from the
	// streaming goroutine while the main loop is parked awaiting the stream.
	if a != nil && a.Engine != nil {
		a.Engine.SetPermissionHandler(func(sessionID string, req *types.PermissionReq, res permission.CheckResult) permission.Decision {
			return cb.tui.PromptPermission(req.Prompt)
		})
	}

	fmt.Fprintln(os.Stdout)

	// Show the ASCII logo at startup when not attached to a TTY (pipe /
	// logged output). In raw mode the logo is rendered inside the TUI
	// banner instead.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		for _, l := range tui.Logo() {
			fmt.Fprintln(os.Stdout, l)
		}
		fmt.Fprintln(os.Stdout)
	}
	return t.Run()
}

var execCmd = &cobra.Command{
	Use:   "exec",
	Short: i18n.Tr("cmd.exec.desc"),
	Long:  `Execute a single prompt in non-interactive mode for scripting and CI/CD integration.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt, _ := cmd.Flags().GetString("prompt")
		if prompt == "" && len(args) > 0 {
			prompt = args[0]
		}
		if prompt == "" {
			return fmt.Errorf("no prompt provided; use -p or pass as argument")
		}

		fmt.Printf("[iCode] Executing (%d chars)...\n\n", len(prompt))

		a, err := app.Bootstrap()
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		defer a.Close()

		// Non-interactive automation: auto-approve tool calls so exec never
		// blocks waiting for a permission prompt.
		if a.Engine != nil {
			a.Engine.SetPermissionHandler(func(sessionID string, req *types.PermissionReq, res permission.CheckResult) permission.Decision {
				return permission.DecisionAllow
			})
		}

		// Create a session
		sess := &types.Session{
			ID:           fmt.Sprintf("exec-%x", time.Now().UnixNano()),
			ModelID:      "openrouter/free",
			ProviderName: "openrouter",
			Title:        prompt,
		}
		if model, _ := cmd.Flags().GetString("model"); model != "" {
			sess.ModelID = model
		}
		if prov, _ := cmd.Flags().GetString("provider"); prov != "" {
			sess.ProviderName = prov
		}

		if err := a.SessStore.Create(sess); err != nil {
			return fmt.Errorf("create session: %w", err)
		}

		ctx := context.Background()
		eventCh, err := a.Engine.Send(ctx, sess.ID, prompt)
		if err != nil {
			return fmt.Errorf("engine: %w", err)
		}

		for event := range eventCh {
			switch event.Type {
			case types.EventText:
				fmt.Print(event.Content)
			case types.EventToolUse:
				fmt.Printf("\n[Tool: %s]\n", event.ToolCall.Name)
			case types.EventDone:
				fmt.Println()
				fmt.Printf("\n[%d prompt tokens, %d completion tokens]\n",
					event.Meta.Usage.PromptTokens,
					event.Meta.Usage.CompletionTokens)
				return nil
			case types.EventError:
				fmt.Fprintf(cmd.ErrOrStderr(), "\n[Error: %s]\n", event.Content)
				return fmt.Errorf("%s", event.Content)
			}
		}
		return nil
	},
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: i18n.Tr("cmd.auth.desc"),
	Long:  `Configure and manage API keys for LLM providers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		list, _ := cmd.Flags().GetBool("list")
		provider, _ := cmd.Flags().GetString("provider")
		key, _ := cmd.Flags().GetString("key")
		show, _ := cmd.Flags().GetBool("show")
		del, _ := cmd.Flags().GetBool("delete")

		cfg, err := config.Load()
		if err != nil {
			cfg = config.Default()
		}

		if list {
			fmt.Println("\nConfigured providers:")
			for name, pc := range cfg.Providers {
				masked := "********"
				if pc.APIKey == "" {
					masked = "(not set)"
				}
				base := pc.APIBase
				if base == "" {
					base = "(default)"
				}
				disabled := ""
				if pc.Disabled {
					disabled = " [disabled]"
				}
				fmt.Printf("  %-14s base: %-45s key: %s%s\n", name, base, masked, disabled)
			}
			return nil
		}

		if provider == "" {
			return fmt.Errorf("--provider flag is required")
		}

		if del {
			delete(cfg.Providers, provider)
			fmt.Printf("Removed configuration for %s\n", provider)
			home, _ := os.UserHomeDir()
			return cfg.Save(filepath.Join(home, ".icode", "config.yaml"))
		}

		if show {
			pc, ok := cfg.Providers[provider]
			if !ok || pc.APIKey == "" {
				return fmt.Errorf("no API key configured for %s", provider)
			}
			fmt.Printf("%s API Key: %s\n", provider, pc.APIKey)
			return nil
		}

		if key == "" {
			return fmt.Errorf("--key flag is required (use --show to view, --delete to remove)")
		}

		pc := cfg.Providers[provider]
		pc.APIKey = key
		cfg.Providers[provider] = pc

		home, _ := os.UserHomeDir()
		if err := cfg.Save(filepath.Join(home, ".icode", "config.yaml")); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Printf("API key saved for %s\n", provider)
		return nil
	},
}

var modelCmd = &cobra.Command{
	Use:   "model",
	Short: "List and manage available AI models",
	Long:  `List installed models, search by provider, and trigger model list updates.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		refresh, _ := cmd.Flags().GetBool("refresh")
		search, _ := cmd.Flags().GetString("search")

		if refresh {
			fmt.Println("🔄 " + i18n.Tr("update.checking"))
			ctx := context.Background()

			// Bootstrap the app to get the updater service
			a, err := app.Bootstrap()
			if err != nil {
				return fmt.Errorf("bootstrap: %w", err)
			}

			updates, err := a.Updater.UpdateAll(ctx)
			if err != nil {
				return fmt.Errorf("refresh models: %w", err)
			}

			fmt.Printf("\n%-20s %-6s %-8s %s\n", "Provider", "Count", "Source", "Status")
			fmt.Println(strings.Repeat("-", 60))
			total := 0
			for _, u := range updates {
				status := "✅"
				if !u.Success {
					status = "❌"
				}
				fmt.Printf("%-20s %-6d %-8s %s", u.Name, u.Count, u.Source, status)
				if u.Error != "" {
					fmt.Printf(" (%s)", u.Error)
				}
				fmt.Println()
				total += u.Count
			}
			fmt.Println(strings.Repeat("-", 60))
			fmt.Printf("Total: %d models across %d providers\n", total, len(updates))
			fmt.Println(i18n.Tr("update.updated"))
			return nil
		}

		if search != "" {
			fmt.Printf("Searching for models matching: %q\n\n", search)
			// Bootstrap to get registered models
			a, err := app.Bootstrap()
			if err != nil {
				return fmt.Errorf("bootstrap: %w", err)
			}
			printModelsBySearch(a.Reg, search)
			return nil
		}

		fmt.Println("Available models (built-in registry):")
		fmt.Println()
		printDefaultModels()
		fmt.Println()
		fmt.Println("Tip: use --refresh to fetch latest models from all providers")
		return nil
	},
}

var configCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "View or change iCode settings",
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
		case len(args) >= 1 && strings.ToLower(args[0]) == "mcp":
			return runConfigMCP(cmd, cfg, args[1:])
		case len(args) >= 1 && strings.ToLower(args[0]) == "model":
			return runConfigModel(cfg, args[1:])
		case len(args) >= 1 && strings.ToLower(args[0]) == "key":
			return runConfigKey(cfg, args[1:])
		case len(args) >= 2:
			return runConfigSet(cmd, cfg, args[0], strings.Join(args[1:], " "))
		default:
			return fmt.Errorf("usage: icode config [key] [value]")
		}
	},
}

func runConfigSet(cmd *cobra.Command, cfg *config.Config, key, value string) error {
	key = strings.ToLower(key)
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
		default:
			return fmt.Errorf("unknown setting: %s (try: model, provider, mode, lang, theme, diff, syntax)", key)
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
//   icode config model                       → list custom/override models
//   icode config model add <p> <id> [name]   → add a custom model
//   icode config model rm <id>               → remove a custom model (id = provider/model_id)
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
//   icode config key                 → list key status (same as `providers`)
//   icode config key <p> <key>       → save API key for provider <p>
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

	chatCmd.Flags().StringP("provider", "p", "", "LLM provider to use")
	chatCmd.Flags().StringP("model", "m", "", "Model ID to use")
	chatCmd.Flags().String("mode", "agent", "Interaction mode (plan/agent/yolo)")

	authCmd.Flags().BoolP("list", "L", false, "List configured providers")
	authCmd.Flags().String("provider", "", "Provider name")
	authCmd.Flags().String("key", "", "API key")
	authCmd.Flags().Bool("show", false, "Show API key for a provider")
	authCmd.Flags().Bool("delete", false, "Remove configuration for a provider")

	modelCmd.Flags().BoolP("refresh", "r", false, "Refresh model list from all providers")
	modelCmd.Flags().String("search", "", "Filter models by name")

	configCmd.Flags().String("command", "", "MCP server command")
	configCmd.Flags().String("mcp-args", "", "MCP server arguments (space-separated)")
	configCmd.Flags().String("mcp-type", "stdio", "MCP server type (stdio|sse)")
	configCmd.Flags().String("mcp-url", "", "MCP server URL (for SSE)")
	configCmd.Flags().Bool("mcp-enabled", true, "Enable MCP server on startup")
}

func printDefaultModels() {
	models := []struct{ provider, model, plan string }{
		{"deepseek", "deepseek-v4-flash", "Coding Plan"},
		{"deepseek", "deepseek-v4-pro", "Reasoning Plan"},
		{"deepseek", "deepseek-chat", "Legacy → V4 Flash"},
		{"zhipu", "glm-5", "Coding Plan"},
		{"zhipu", "glm-4-flash", "Free Plan"},
		{"kimi", "kimi-k2.7-code", "Coding Plan"},
		{"kimi", "kimi-k2.6", "Token Plan"},
		{"volcengine", "doubao-seed-2.1-pro", "Coding Plan"},
		{"volcengine", "doubao-seed-2.1-turbo", "Token Plan"},
		{"tencent", "hunyuan-turbos", "Coding Plan (free)"},
		{"tencent", "hunyuan-t1", "Reasoning Plan"},
		{"huawei", "pangu-5.0-pro", "Coding Plan"},
		{"huawei", "pangu-5.0-code", "Code Plan"},
		{"scnet", "scnet-chat", "codingplan"},
		{"scnet", "MiniMax-m2.5", "codingplan"},
		{"scnet", "scnet-code", "tokenplan"},
		{"scnet", "deepseek-v4-flash", "tokenplan"},
		{"scnet", "deepseek-v4-pro", "tokenplan"},
		{"openrouter", "auto", "Auto Router"},
		{"openrouter", "openrouter/free", "Free Tier"},
		{"openrouter", "openai/gpt-4o", "Token Plan"},
		{"openrouter", "anthropic/claude-sonnet-4", "Coding Plan"},
		{"openrouter", "google/gemini-2.0-flash-exp:free", "Free Tier"},
		{"anthropic", "claude-sonnet-4-20250514", "Coding Plan"},
		{"anthropic", "claude-haiku-4-20250514", "Token Plan"},
	}

	fmt.Println()
	fmt.Printf("  %-16s %-42s %s\n", "Provider", "Model", "Plan")
	fmt.Println("  " + strings.Repeat("-", 78))
	for _, m := range models {
		fmt.Printf("  [%-12s] %-42s %s\n", m.provider, m.model, m.plan)
	}
}

// printModelsBySearch filters and displays models matching a search term.
func printModelsBySearch(reg *registry.Impl, search string) {
	models := reg.ListAllModels()
	matched := false
	fmt.Printf("  %-16s %-42s %s\n", "Provider", "Model", "Context")
	fmt.Println("  " + strings.Repeat("-", 78))
	searchLower := strings.ToLower(search)
	for _, m := range models {
		if strings.Contains(strings.ToLower(m.ID), searchLower) ||
			strings.Contains(strings.ToLower(m.Name), searchLower) ||
			strings.Contains(strings.ToLower(m.Provider), searchLower) {
			cw := ""
			if m.ContextWindow > 0 {
				cw = fmt.Sprintf("%dK", m.ContextWindow/1024)
			}
			fmt.Printf("  [%-12s] %-42s %s\n", m.Provider, m.ID, cw)
			matched = true
		}
	}
	if !matched {
		fmt.Printf("  No models found matching %q\n", search)
	}
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose system health and connectivity",
	Long:  `Check provider status, database health, and overall system configuration.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := app.Bootstrap()
		if err != nil {
			fmt.Printf("Bootstrap error: %v\n", err)
			return err
		}
		defer app.Close()

		app.PrintProviderStatus()
		fmt.Println()
		fmt.Println("iCode system check complete.")
		return nil
	},
}

// chatCallback bridges the TUI to the iCode backend.
type chatCallback struct {
	app       *app.App
	tui       *tui.TUI
	sessionID string
	lastTool  string
}

func (c *chatCallback) OnSend(text string) {
	if c.app == nil || c.app.Engine == nil {
		c.tui.AddMessage(tui.RoleSystem, "[Engine not available. Configure an API key with 'icode auth set']")
		return
	}

	model := c.tui.CurrentModel()
	provider := c.tui.CurrentProvider()

	// Create session on first message
	if c.sessionID == "" {
		sess := &types.Session{
			ID:           fmt.Sprintf("%x", time.Now().UnixNano()),
			ModelID:      model,
			ProviderName: provider,
			Title:        text,
		}
		if len(text) > 40 {
			sess.Title = text[:40] + "..."
		}
		if err := c.app.SessStore.Create(sess); err == nil {
			c.sessionID = sess.ID
		} else {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("[Session error: %v]", err))
			return
		}
	} else if c.app.SessStore != nil {
		// Keep the session's model/provider in sync with the TUI.
		if sess, err := c.app.SessStore.Get(c.sessionID); err == nil {
			if sess.ModelID != model || sess.ProviderName != provider {
				sess.ModelID = model
				sess.ProviderName = provider
				c.app.SessStore.Update(sess)
			}
		}
	}

	c.lastTool = ""
	ctx := context.Background()
	eventCh, err := c.app.Engine.Send(ctx, c.sessionID, text)
	if err != nil {
		c.tui.AddMessage(tui.RoleError, fmt.Sprintf("Engine error: %v", err))
		return
	}

	// Read streaming events and push to TUI
	for event := range eventCh {
		switch event.Type {
		case types.EventText:
			if c.lastTool != "" {
				// This text is the tool-result wrapper emitted by the engine.
				result := strings.TrimSpace(strings.TrimPrefix(
					strings.TrimPrefix(event.Content, "\n"),
					"[Tool: "+c.lastTool+"]"))
				if result != "" {
					c.tui.AppendToolResult(result)
				}
				c.lastTool = ""
			} else {
				c.tui.AppendStream(event.Content)
			}
		case types.EventToolUse:
			c.lastTool = event.ToolCall.Name
			// Strip empty/no-op parameter objects so the conversation
			// shows "⏺ git_status" instead of "⏺ git_status {}".
			args := event.ToolCall.Arguments
			if strings.TrimSpace(args) == "{}" {
				args = ""
			}
			c.tui.AddToolMessage(event.ToolCall.Name, args, "")
		case types.EventDone:
			u := event.Meta.Usage
			var cacheRate float64
			if total := u.PromptTokens + u.CompletionTokens; total > 0 {
				cacheRate = float64(u.CacheHitTokens) / float64(total)
			}
			// Resolve the model to compute cost + context-window usage for the
			// Claude Code-style status bar.
			costStr := ""
			ctxWin := 0
			if _, mi, rerr := c.app.Reg.ResolveModel(model); rerr == nil {
				costStr = formatCost(estimateCost(u, mi), primaryCurrency(mi))
				ctxWin = mi.ContextWindow
			}
			c.tui.SetStatus(u.PromptTokens, u.CompletionTokens, cacheRate, costStr)
			c.tui.SetContext(u.PromptTokens, ctxWin)
			c.tui.EndStream()
			return
		case types.EventError:
			c.tui.AddMessage(tui.RoleError, event.Content)
			c.tui.EndStream()
			return
		}
	}
}

// OnListSessions returns a formatted list of saved sessions.
func (c *chatCallback) OnListSessions() string {
	if c.app == nil || c.app.SessStore == nil {
		return "No session store available."
	}
	sessions, err := c.app.SessStore.List(20, 0)
	if err != nil || len(sessions) == 0 {
		return "No saved sessions yet. Start chatting to create one."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Saved sessions (%d):\n", len(sessions)))
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		sb.WriteString(fmt.Sprintf("  %s  %s  [%s]\n", s.ID, title, s.ModelID))
	}
	return sb.String()
}

// OnResume loads a past session's messages into the TUI.
func (c *chatCallback) OnResume(id string) string {
	if c.app == nil || c.app.SessStore == nil {
		return "No session store available."
	}
	sess, err := c.app.SessStore.Get(id)
	if err != nil {
		return fmt.Sprintf("Session not found: %s", id)
	}
	c.sessionID = sess.ID

	var msgs []tui.Message
	for _, m := range sess.Messages {
		tm := tui.Message{Role: tui.Role(m.Role), Content: m.Content}
		if m.Role == "tool" && len(m.ToolCalls) > 0 {
			tm.Tool = m.ToolCalls[0].Name
			tm.ToolArgs = m.ToolCalls[0].Arguments
		}
		msgs = append(msgs, tm)
	}
	c.tui.LoadSession(msgs)
	return fmt.Sprintf("Resumed session %s — %d messages loaded", id, len(msgs))
}

func (c *chatCallback) OnSlashCommand(cmd string, args []string) {
	switch strings.ToLower(cmd) {
	case "/model":
		if len(args) > 0 {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Switched model to: %s", args[0]))
		}
	case "/mode":
		if len(args) > 0 {
			if c.app != nil && c.app.Gate != nil {
				c.app.Gate.SetMode(permission.Mode(strings.ToLower(args[0])))
			}
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Mode set to: %s", args[0]))
		}
	case "/session":
		if c.sessionID != "" {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Active session: %s", c.sessionID))
		} else {
			c.tui.AddMessage(tui.RoleSystem, "No active session. Start typing to create one.")
		}
	case "/clear":
		if c.sessionID != "" && c.app != nil && c.app.SessStore != nil {
			c.app.SessStore.Delete(c.sessionID)
		}
		c.sessionID = ""
		c.tui.AddMessage(tui.RoleSystem, "Session cleared.")
	case "/review":
		edits := searchreplace.StageList()
		if len(edits) == 0 {
			c.tui.AddMessage(tui.RoleSystem, "No staged edits. Use the search_replace tool to propose changes first.")
			break
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Staged edits (%d):\n", len(edits)))
		for i, ed := range edits {
			status := "✓ valid"
			if !ed.Valid {
				status = "✗ invalid"
			}
			searchPreview := strings.ReplaceAll(ed.Search, "\n", "\\n")
			if len(searchPreview) > 60 {
				searchPreview = searchPreview[:60] + "..."
			}
			b.WriteString(fmt.Sprintf("\n  #%d %s [%s]\n", i, ed.FilePath, status))
			b.WriteString(fmt.Sprintf("      SEARCH: %q\n", searchPreview))
			b.WriteString(fmt.Sprintf("      Reason: %s\n", ed.Reason))
		}
		b.WriteString("\n/apply   — apply all valid staged edits")
		b.WriteString("\n/reject  — discard staged edits")
		c.tui.AddMessage(tui.RoleSystem, b.String())

		case "/undo":
			steps := 1
			if len(args) > 0 {
				fmt.Sscanf(args[0], "%d", &steps)
			}
			if checkpoint.DefaultUndo != nil {
				files, err := checkpoint.DefaultUndo.Undo(context.Background(), steps)
				if err != nil {
					c.tui.AddMessage(tui.RoleSystem, "撤销失败: "+err.Error())
				} else if len(files) == 0 {
					c.tui.AddMessage(tui.RoleSystem, "没有可撤销的更改")
				} else {
					c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已撤销 %d 步，还原了 %d 个文件:", steps, len(files)))
					for _, f := range files {
						c.tui.AddMessage(tui.RoleSystem, "  - "+f)
					}
				}
			} else {
				c.tui.AddMessage(tui.RoleSystem, "撤销系统未初始化")
			}
	case "/apply":
		n := searchreplace.StageCount()
		if n == 0 {
			c.tui.AddMessage(tui.RoleSystem, "No staged edits to apply.")
			break
		}
		results := searchreplace.StageApplyValid()
		for _, r := range results {
			c.tui.AddMessage(tui.RoleSystem, r)
		}
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Applied %d/%d staged edits. Remaining: %d",
			countApplied(results), n, searchreplace.StageCount()))

	case "/reject":
		n := searchreplace.StageCount()
		if n == 0 {
			c.tui.AddMessage(tui.RoleSystem, "No staged edits to reject.")
			break
		}
		searchreplace.StageClear()
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Rejected %d staged edits.", n))

	case "/config":
		if cfg, cerr := config.LoadOrCreate(); cerr == nil {
			c.tui.AddMessage(tui.RoleSystem, renderConfigPanel(cfg))
		} else {
			c.tui.AddMessage(tui.RoleSystem, "Config unavailable: "+cerr.Error())
		}
	default:
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Unknown command: %s", cmd))
	}
}

func (c *chatCallback) OnPermissionResponse(decision string) {
	c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Permission: %s", decision))
}

// TodoCounts implements tui.Callback — surfaces the current session's todo
// tally in the status bar. Missing session / empty list → all zeros.
func (c *chatCallback) TodoCounts() (pending, active, done, total int) {
	if c.sessionID == "" {
		return
	}
	return todo.Default.Counts(c.sessionID)
}

func (c *chatCallback) SessionID() string { return c.sessionID }

func (c *chatCallback) OnInterrupt() {
	if c.app != nil && c.app.Engine != nil && c.sessionID != "" {
		c.app.Engine.Stop(c.sessionID)
	}
}

func (c *chatCallback) OnStatus() string {
	if c.app == nil {
		return "引擎未初始化。配置 API Key 后重试。"
	}
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
			// Mask the key
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
	if c.sessionID != "" {
		b.WriteString(c.sessionID[:8] + "...")
	} else {
		b.WriteString("无")
	}

	return b.String()
}

// estimateCost mirrors core/conversation.calculateCost for the CLI status bar.
func estimateCost(u types.TokenUsage, mi types.ModelInfo) float64 {
	if len(mi.Plans) == 0 {
		return 0
	}
	plan := mi.Plans[0]
	cacheHit := u.CacheHitTokens
	if cacheHit > u.PromptTokens {
		cacheHit = u.PromptTokens
	}
	inputCost := float64(u.PromptTokens-cacheHit) * plan.InputPrice / 1e6
	outputCost := float64(u.CompletionTokens) * plan.OutputPrice / 1e6
	cacheCost := float64(cacheHit) * plan.CachePrice / 1e6
	return inputCost + outputCost + cacheCost
}

func primaryCurrency(mi types.ModelInfo) string {
	if len(mi.Plans) > 0 && mi.Plans[0].Currency != "" {
		return mi.Plans[0].Currency
	}
	return "USD"
}

func formatCost(v float64, cur string) string {
	sym := "$"
	if cur == "CNY" {
		sym = "¥"
	}
	if v <= 0 {
		return sym + "0.0000"
	}
	return fmt.Sprintf("%s%.4f", sym, v)
}

func countApplied(results []string) int {
	n := 0
	for _, r := range results {
		if strings.HasPrefix(r, "APPLIED") {
			n++
		}
	}
	return n
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the iCode backend API server",
	Long:  `Start an HTTP API server for the Electron desktop app or remote API access.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")

		a, err := app.Bootstrap()
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		defer a.Close()

		srv := server.New(server.ServerConfig{
			Config:   a.Cfg,
			Registry: a.Reg,
			Store:    a.SessStore,
			DB:       a.DB,
			Engine:   a.Engine,
			Gate:     a.Gate,
			Updater:  a.Updater,
			Port:     port,
		})

		ctx := context.Background()
		actualPort, err := srv.Start(ctx)
		if err != nil {
			return fmt.Errorf("start server: %w", err)
		}

		fmt.Printf("iCode server running on http://127.0.0.1:%d\n", actualPort)

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		<-sigCh

		fmt.Println("\nShutting down...")
		return srv.Shutdown(context.Background())
	},
}

func init() {
	serverCmd.Flags().Int("port", 0, "Port to listen on (0 = auto)")
}
