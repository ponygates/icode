package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/config/i18n"
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/knowledge"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/searchreplace"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/core/todo"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/core/voice"
	"github.com/ponygates/icode/internal/llm/provider"
	"github.com/ponygates/icode/internal/server"
	"github.com/ponygates/icode/internal/tui"
	"github.com/ponygates/icode/internal/types"
	"github.com/ponygates/icode/internal/update"
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
		Version:  appVersion,
		Callback: cb,
	})

	// Wire TUI stream writer back to callback
	cb.tui = t

	// Config changes made from the TUI (e.g. /lang) must refresh everything
	// the engine derived at startup — chiefly the system prompt that carries
	// the language directive (replies/reasoning/comments follow /lang).
	if a.Engine != nil {
		t.SetOnConfigChanged(func(c *config.Config) {
			a.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(c))
		})
	}

	// Notify in the TUI when a background task completes (P1-3: task-done
	// notification, Claude Code / opencode parity).
	tool.SetCompleteHook(func(id, errMsg string) {
		status := "✓ 后台任务完成: " + id
		if errMsg != "" {
			status = "⚠ 后台任务失败: " + id + " — " + errMsg
		}
		t.AddMessage(tui.RoleSystem, status)
	})

	// Same notification path for background SUB-AGENTS (agt-N): when a
	// detached agent finishes, surface its result availability in-chat.
	tool.SetAgentTaskCompleteHook(func(id, errMsg string) {
		status := "✓ 后台子代理完成: " + id + "（用 task_output 查看结果）"
		if errMsg != "" {
			status = "⚠ 后台子代理失败: " + id + " — " + errMsg
		}
		t.AddMessage(tui.RoleSystem, status)
	})

	// Populate the available model list so the /model picker and Tab model
	// switching work. Without this t.models stays empty and both features
	// silently no-op.
	if a != nil && a.Reg != nil {
		if all := a.Reg.ListAllModels(); len(all) > 0 {
			ids := make([]string, 0, len(all))
			for _, m := range all {
				ids = append(ids, m.ID)
			}
			t.SetModels(ids)
		}
	}

	// In the CLI, permission approvals are resolved interactively by the
	// TUI prompt (agent mode). The engine calls this handler from the
	// streaming goroutine while the main loop is parked awaiting the stream.
	if a != nil && a.Engine != nil {
		a.Engine.SetPermissionHandler(func(sessionID string, req *types.PermissionReq, res permission.CheckResult) permission.Decision {
			return cb.tui.PromptPermission(req.Prompt)
		})
	}

	fmt.Fprintln(os.Stdout)

	// Auto-resume the most recently updated session so the CLI continues the
	// conversation desktop / simpleui last touched — the three ends share one
	// history store, and ListNonDeleted is newest-first. `t.LoadSession` just
	// buffers the transcript here; it is painted when the raw loop starts.
	// Only in interactive mode: piped one-shot prompts must not pollute the
	// most recent session (use `icode exec` for scripting instead).
	if a != nil && a.SessStore != nil && term.IsTerminal(int(os.Stdin.Fd())) {
		if sessions, err := sessionum.ListNonDeleted(a.SessStore, 1); err == nil && len(sessions) > 0 {
			cb.OnResume(sessions[0].ID)
		}
	}

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

		outputFormat, _ := cmd.Flags().GetString("output-format")
		switch outputFormat {
		case "", "text", "json", "stream-json":
		default:
			return fmt.Errorf("invalid --output-format %q (want text|json|stream-json)", outputFormat)
		}
		jsonMode := outputFormat == "json"
		streamJSON := outputFormat == "stream-json"

		if !jsonMode && !streamJSON {
			fmt.Printf("[iCode] Executing (%d chars)...\n\n", len(prompt))
		}

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

		// stream-json: one NDJSON object per event (CI / pipeline mode,
		// Claude Code `--output-format stream-json` parity).
		emitNDJSON := func(v interface{}) {
			b, err := json.Marshal(v)
			if err != nil {
				return
			}
			fmt.Println(string(b))
		}

		startAt := time.Now()
		var finalText strings.Builder
		var toolsUsed []string
		var usage types.TokenUsage

		for event := range eventCh {
			switch event.Type {
			case types.EventText:
				finalText.WriteString(event.Content)
				if streamJSON {
					emitNDJSON(map[string]interface{}{"type": "text", "content": event.Content})
				} else if !jsonMode {
					fmt.Print(event.Content)
				}
			case types.EventToolUse:
				toolsUsed = append(toolsUsed, event.ToolCall.Name)
				if streamJSON {
					emitNDJSON(map[string]interface{}{
						"type": "tool_use", "tool": event.ToolCall.Name,
						"arguments": json.RawMessage(orEmptyJSON(event.ToolCall.Arguments)),
					})
				} else if !jsonMode {
					fmt.Printf("\n[Tool: %s]\n", event.ToolCall.Name)
				}
			case types.EventToolProgress:
				// Live bash output — forward in stream mode, surface in text mode.
				if streamJSON {
					emitNDJSON(map[string]interface{}{"type": "tool_progress", "content": event.Content})
				} else if !jsonMode {
					fmt.Print(event.Content)
				}
			case types.EventDone:
				if event.Meta.Usage.PromptTokens > 0 || event.Meta.Usage.CompletionTokens > 0 {
					usage = event.Meta.Usage
				}
				result := map[string]interface{}{
					"type":        "result",
					"is_error":    false,
					"result":      finalText.String(),
					"session_id":  sess.ID,
					"model":       sess.ModelID,
					"tools_used":  toolsUsed,
					"duration_ms": time.Since(startAt).Milliseconds(),
					"usage": map[string]interface{}{
						"prompt_tokens":     usage.PromptTokens,
						"completion_tokens": usage.CompletionTokens,
					},
				}
				if jsonMode || streamJSON {
					emitNDJSON(result)
				} else {
					fmt.Println()
					fmt.Printf("\n[%d prompt tokens, %d completion tokens]\n",
						usage.PromptTokens, usage.CompletionTokens)
				}
				return nil
			case types.EventError:
				if jsonMode || streamJSON {
					emitNDJSON(map[string]interface{}{
						"type": "result", "is_error": true,
						"result": event.Content, "session_id": sess.ID,
					})
					return fmt.Errorf("%s", event.Content)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "\n[Error: %s]\n", event.Content)
				return fmt.Errorf("%s", event.Content)
			}
		}
		return nil
	},
}

// orEmptyJSON returns s when it is non-empty, otherwise a valid empty JSON
// object so json.RawMessage marshalling never fails on blank arguments.
func orEmptyJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// clipStr truncates s to n runes and appends "…" when shortened (used to keep
// error messages inside slash-command replies short).
func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Check for a newer iCode release and self-update the binary",
	Long: `Download the latest Windows amd64 release from GitHub and swap the
running binary in place. The new version activates on next launch.
The previous binary is kept as <exe>.old for manual rollback.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		fmt.Println("🔄 正在检查新版本…")
		res, err := update.Upgrade(ctx, appVersion)
		if err != nil {
			return err
		}
		if res.Skipped != "" {
			fmt.Println("✓ " + res.Skipped)
			return nil
		}
		fmt.Printf("✅ 已升级 %s → %s\n", res.From, res.To)
		fmt.Println("重启 iCode 即可使用新版本。回滚备份：" + res.Path + ".old")
		return nil
	},
}

var modelCmd = &cobra.Command{
	Use:   "model",
	Short: "List and manage available AI models",
	Long:  `List installed models, search by provider, and trigger model list updates.`, RunE: func(cmd *cobra.Command, args []string) error {
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
		{"openrouter", "anthropic/claude-sonnet-5", "Coding Plan"},
		{"openrouter", "google/gemini-2.0-flash-exp:free", "Free Tier"},
		{"anthropic", "claude-opus-5", "Coding Plan"},
		{"anthropic", "claude-sonnet-5", "Coding Plan"},
		{"anthropic", "claude-haiku-4-5-20251001", "Token Plan"},
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
	voiceRec  *voice.Recorder // active /voice recorder (nil when idle)
}

func (c *chatCallback) OnSend(text string, attachments []types.Attachment) {
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
	eventCh, err := c.app.Engine.Send(ctx, c.sessionID, text, attachments)
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
		case types.EventThinking:
			// Store thinking content for display in the thinking box
			c.tui.AddMessage(tui.RoleThinking, event.Content)
		case types.EventSystem:
			c.tui.AddMessage(tui.RoleSystem, strings.TrimSpace(event.Content))
		case types.EventPlanProposal:
			// A plan-mode turn finished — arm the confirmation prompt so the
			// next Enter executes the plan (Esc cancels).
			c.tui.SetPlanPending(true)
		case types.EventToolUse:
			c.lastTool = event.ToolCall.Name
			// Strip empty/no-op parameter objects so the conversation
			// shows "⏺ git_status" instead of "⏺ git_status {}".
			args := event.ToolCall.Arguments
			if strings.TrimSpace(args) == "{}" {
				args = ""
			}
			c.tui.AddToolMessage(event.ToolCall.Name, args, "")
		case types.EventToolProgress:
			// Live tool output (bash streaming): append to the active tool card.
			c.tui.AppendToolProgress(event.Content)
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
	// The event channel closed without an explicit EventDone/EventError
	// (e.g. the user hit Esc and the engine stopped the stream). Reset the
	// streaming state unconditionally — EndStream is idempotent — so the UI
	// can never get stuck in "generating…" with a dead Esc key.
	c.tui.EndStream()
}

// OnListSessions returns a formatted list of saved sessions.
func (c *chatCallback) OnPlanConfirm() {
	if c.app != nil && c.app.Gate != nil {
		c.app.Gate.SetMode(permission.ModeAuto)
	}
	c.tui.SetPlanPending(false)
	c.OnSend("计划已确认。请按上述计划立即开始执行，不要再重复或重新规划，直接动手。", nil)
}

func (c *chatCallback) OnListSessions() string {
	if c.app == nil || c.app.SessStore == nil {
		return "No session store available."
	}
	sessions, err := sessionum.ListNonDeleted(c.app.SessStore, 20)
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

// OnListSessionsStructured implements tui.Callback — returns lightweight
// session descriptors for the interactive /resume picker.
func (c *chatCallback) OnListSessionsStructured(limit int) []tui.SessionInfo {
	if c.app == nil || c.app.SessStore == nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	sessions, err := sessionum.ListNonDeleted(c.app.SessStore, limit)
	if err != nil {
		return nil
	}
	out := make([]tui.SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		out = append(out, tui.SessionInfo{
			ID:      s.ID,
			Title:   title,
			Model:   s.ModelID,
			Updated: s.UpdatedAt.Format("01-02 15:04"),
		})
	}
	return out
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
	if sessionum.IsDeleted(sess) {
		return fmt.Sprintf("该会话已被软删除，先用 /restore %s 恢复。", id)
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

// OnCompactSummarize asks the engine to produce a model-generated semantic
// summary of the active session's older turns (/compact parity). The summary
// is cached into the session metadata (marked semantic) so a later
// /resume --compact or /resume --lite reuses it without another model call.
// Returns "" on any failure — the TUI then falls back to the free local trim.
func (c *chatCallback) OnCompactSummarize(instruction string) string {
	if c.app == nil || c.app.Engine == nil || c.sessionID == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sum, err := c.app.Engine.SummarizeConversation(ctx, c.sessionID, instruction)
	if err != nil || sum == "" {
		return ""
	}
	if sess, err := c.app.SessStore.Get(c.sessionID); err == nil {
		_ = sessionum.Save(c.app.SessStore, sess, sum)
		_ = sessionum.MarkSemantic(c.app.SessStore, sess)
	}
	return sum
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
	case "/thinking":
		if c.app == nil || c.app.Engine == nil {
			c.tui.AddMessage(tui.RoleSystem, "引擎未初始化。")
			break
		}
		if len(args) == 0 {
			if b := c.app.Engine.ThinkingBudget(); b > 0 {
				c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("当前 extended thinking 已开启，预算 %d tokens。用法: /thinking <on|off|<tokens>>", b))
			} else {
				c.tui.AddMessage(tui.RoleSystem, "当前 extended thinking 已关闭。用法: /thinking <on|off|<tokens>>（如 /thinking 4096，对 Anthropic 模型生效）")
			}
			break
		}
		budget := 0
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "on":
			budget = 4096
		case "off", "0":
			budget = 0
		default:
			n, err := strconv.Atoi(strings.TrimSpace(args[0]))
			if err != nil || n < 1024 {
				c.tui.AddMessage(tui.RoleSystem, "无效预算（至少 1024 tokens）。用法: /thinking <on|off|<tokens>>")
				break
			}
			budget = n
		}
		c.app.Engine.SetThinking(budget)
		if cfg, err := config.Load(); err == nil {
			cfg.Defaults.ThinkingTokens = budget
			_ = cfg.Save(config.DefaultPath())
		}
		if budget > 0 {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("✓ extended thinking 已开启（预算 %d tokens，对 Anthropic 模型生效，已持久化）", budget))
		} else {
			c.tui.AddMessage(tui.RoleSystem, "✓ extended thinking 已关闭（已持久化）。")
		}
	case "/session":
		if c.sessionID != "" {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Active session: %s", c.sessionID))
		} else {
			c.tui.AddMessage(tui.RoleSystem, "No active session. Start typing to create one.")
		}
	case "/resume":
		if len(args) == 0 || c.app == nil || c.app.SessStore == nil {
			c.tui.AddMessage(tui.RoleSystem, "Usage: /resume <session-id> [--lite[=<n>] | --compact[=<n>]]")
			break
		}
		id := args[0]
		lite := 0
		compact := false
		bad := false
		if len(args) > 1 {
			switch a := strings.TrimSpace(args[1]); {
			case a == "--lite":
				lite = 4
			case strings.HasPrefix(a, "--lite="):
				fmt.Sscanf(strings.TrimPrefix(a, "--lite="), "%d", &lite)
				if lite <= 0 {
					c.tui.AddMessage(tui.RoleSystem, "用法: /resume <session-id> --lite=<n>（应为正整数）")
					bad = true
				}
			case a == "--compact":
				compact = true
				lite = 4
			case strings.HasPrefix(a, "--compact="):
				compact = true
				fmt.Sscanf(strings.TrimPrefix(a, "--compact="), "%d", &lite)
				if lite <= 0 {
					c.tui.AddMessage(tui.RoleSystem, "用法: /resume <session-id> --compact=<n>（应为正整数）")
					bad = true
				}
			default:
				c.tui.AddMessage(tui.RoleSystem, "用法: /resume <session-id> --lite=<n> 或 --compact[=<n>]")
				bad = true
			}
		}
		if !bad && lite > 0 {
			sess, err := c.app.SessStore.Get(id)
			if err != nil {
				c.tui.AddMessage(tui.RoleSystem, "会话不存在: "+id)
				break
			}
			if compact && !sessionum.IsSemantic(sess) && c.app.Engine != nil {
				// Resume compaction (Claude Code parity): generate a semantic
				// summary on the spot and cache it, so the resumed session
				// feeds the model only summary + recent turns. A cached
				// semantic summary is reused without a second model call.
				c.tui.AddMessage(tui.RoleSystem, "⏳ 正在生成会话语义摘要…（模型压缩，约 10–60 秒）")
				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				sum, serr := c.app.Engine.SummarizeConversation(ctx, id, "")
				cancel()
				switch {
				case serr != nil:
					c.tui.AddMessage(tui.RoleSystem, "⚠ 模型摘要生成失败（"+clipStr(serr.Error(), 140)+"），已回退为本地存档摘要。")
				case sum == "":
					c.tui.AddMessage(tui.RoleSystem, "该会话轮次太少，无需模型摘要；如已有本地摘要则继续压缩恢复。")
				default:
					_ = sessionum.Save(c.app.SessStore, sess, sum)
					_ = sessionum.MarkSemantic(c.app.SessStore, sess)
				}
			}
			if sessionum.Get(sess) == "" {
				c.tui.AddMessage(tui.RoleSystem, "该会话还没有存档摘要，无法压缩恢复。先 /summarize、/compact 或退出时自动存档后再试。")
				break
			}
			if err := sessionum.SetLite(c.app.SessStore, sess, lite); err != nil {
				c.tui.AddMessage(tui.RoleSystem, "设置 lite 模式失败: "+err.Error())
				break
			}
			if compact {
				c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("✓ 已启用紧凑恢复（语义摘要 + 最近 %d 条消息送入模型，后续轮次自动生效）", lite))
			}
		}
		if !bad {
			c.tui.AddMessage(tui.RoleSystem, c.OnResume(id))
		}
	case "/restore":
		if len(args) == 0 || c.app == nil || c.app.SessStore == nil {
			c.tui.AddMessage(tui.RoleSystem, "Usage: /restore <session-id> — 恢复被 /clear 软删除的会话")
			break
		}
		if sess, err := c.app.SessStore.Get(args[0]); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "会话不存在: "+args[0])
		} else if !sessionum.IsDeleted(sess) {
			c.tui.AddMessage(tui.RoleSystem, "该会话未被删除，无需恢复。")
		} else if err := sessionum.Restore(c.app.SessStore, sess); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "恢复失败: "+err.Error())
		} else {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已恢复会话 %s — %d 条消息", sess.ID, len(sess.Messages)))
		}
	case "/export":
		if len(args) == 0 || c.app == nil || c.app.SessStore == nil {
			c.tui.AddMessage(tui.RoleSystem, "Usage: /export <session-id> [output.json]")
			break
		}
		sess, err := c.app.SessStore.Get(args[0])
		if err != nil {
			c.tui.AddMessage(tui.RoleSystem, "会话不存在: "+args[0])
			break
		}
		data, err := sessionum.Export(sess)
		if err != nil {
			c.tui.AddMessage(tui.RoleSystem, "导出失败: "+err.Error())
			break
		}
		path := fmt.Sprintf("icode-%s.json", sess.ID)
		if len(args) > 1 {
			path = args[1]
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "写入失败: "+err.Error())
			break
		}
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已导出会话 %s（%d 条消息）到 %s", sess.ID, len(sess.Messages), path))
	case "/import":
		if len(args) == 0 || c.app == nil || c.app.SessStore == nil {
			c.tui.AddMessage(tui.RoleSystem, "Usage: /import <session.json>")
			break
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			c.tui.AddMessage(tui.RoleSystem, "读取失败: "+err.Error())
			break
		}
		sess, imported, err := sessionum.Import(c.app.SessStore, data)
		if err != nil {
			c.tui.AddMessage(tui.RoleSystem, "导入失败: "+err.Error())
			break
		}
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已导入会话 %s（%d 条消息），正在切换…", sess.ID, imported))
		c.tui.AddMessage(tui.RoleSystem, c.OnResume(sess.ID))
	case "/fork":
		if len(args) > 0 && c.app != nil && c.app.SessStore != nil {
			spec := args[0]
			srcID := spec
			n := 0
			if at := strings.LastIndex(spec, "@"); at > 0 {
				srcID = spec[:at]
				fmt.Sscanf(spec[at+1:], "%d", &n)
			}
			forked, err := sessionum.Fork(c.app.SessStore, srcID, n)
			if err != nil {
				c.tui.AddMessage(tui.RoleSystem, "分叉失败: "+err.Error())
			} else {
				c.tui.AddMessage(tui.RoleSystem, c.OnResume(forked.ID))
				c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已从 %s 分叉出独立会话 %s — %d 条消息", srcID, forked.ID, len(forked.Messages)))
			}
		} else {
			c.tui.AddMessage(tui.RoleSystem, "Usage: /fork <session-id>[@<n>]")
		}
	case "/goal":
		sub := "show"
		if len(args) > 0 {
			sub = strings.ToLower(args[0])
		}
		withSess := func(fn func(*types.Session)) {
			if c.sessionID == "" || c.app == nil || c.app.SessStore == nil {
				c.tui.AddMessage(tui.RoleSystem, "没有活跃会话。")
				return
			}
			sess, err := c.app.SessStore.Get(c.sessionID)
			if err != nil {
				c.tui.AddMessage(tui.RoleSystem, "读取会话失败: "+err.Error())
				return
			}
			fn(sess)
		}
		switch sub {
		case "set":
			if len(args) < 2 {
				c.tui.AddMessage(tui.RoleSystem, "用法: /goal set <目标文本> [--verify <验收命令>]")
				break
			}
			goal, verify := "", ""
			rest := args[1:]
			for i := 0; i < len(rest); i++ {
				if rest[i] == "--verify" || rest[i] == "-v" {
					if i+1 < len(rest) {
						verify = strings.TrimSpace(strings.Join(rest[i+1:], " "))
					}
					break
				}
				if goal != "" {
					goal += " "
				}
				goal += rest[i]
			}
			goal = strings.TrimSpace(goal)
			if goal == "" {
				c.tui.AddMessage(tui.RoleSystem, "目标不能为空。用法: /goal set <目标文本> [--verify <验收命令>]")
				break
			}
			finalGoal, finalVerify := goal, verify
			withSess(func(sess *types.Session) {
				if err := sessionum.SetGoal(c.app.SessStore, sess, finalGoal); err != nil {
					c.tui.AddMessage(tui.RoleSystem, "保存目标失败: "+err.Error())
					return
				}
				if finalVerify != "" {
					if err := sessionum.SetGoalVerify(c.app.SessStore, sess, finalVerify); err != nil {
						c.tui.AddMessage(tui.RoleSystem, "保存验收命令失败: "+err.Error())
						return
					}
					c.tui.AddMessage(tui.RoleSystem, "已设置可验收目标（每轮自动迭代直到验收命令通过）：\n目标："+finalGoal+"\n验收命令："+finalVerify)
					return
				}
				c.tui.AddMessage(tui.RoleSystem, "已设置长目标（后续每轮对话都会自动携带）：\n"+finalGoal)
			})
		case "show":
			withSess(func(sess *types.Session) {
				if g := sessionum.GetGoal(sess); g != "" {
					msg := "当前目标（长目标模式生效中）：\n" + g
					if v := sessionum.GetGoalVerify(sess); v != "" {
						msg += "\n验收命令：" + v
					}
					c.tui.AddMessage(tui.RoleSystem, msg)
				} else {
					c.tui.AddMessage(tui.RoleSystem, "当前没有目标。\n用法: /goal set <目标> [--verify <验收命令>] | /goal show | /goal clear")
				}
			})
		case "clear", "unset":
			withSess(func(sess *types.Session) {
				if err := sessionum.SetGoal(c.app.SessStore, sess, ""); err != nil {
					c.tui.AddMessage(tui.RoleSystem, "清除目标失败: "+err.Error())
				} else {
					_ = sessionum.SetGoalVerify(c.app.SessStore, sess, "")
					c.tui.AddMessage(tui.RoleSystem, "已清除目标，退出长目标模式。")
				}
			})
		default:
			c.tui.AddMessage(tui.RoleSystem, "用法: /goal set <目标> [--verify <验收命令>] | /goal show | /goal clear")
		}
	case "/budget":
		withSess := func(fn func(*types.Session)) {
			if c.sessionID == "" || c.app == nil || c.app.SessStore == nil {
				c.tui.AddMessage(tui.RoleSystem, "没有活跃会话。")
				return
			}
			sess, err := c.app.SessStore.Get(c.sessionID)
			if err != nil {
				c.tui.AddMessage(tui.RoleSystem, "读取会话失败: "+err.Error())
				return
			}
			fn(sess)
		}
		sub := "show"
		if len(args) > 0 {
			sub = strings.ToLower(args[0])
		}
		switch sub {
		case "set":
			if len(args) < 2 {
				c.tui.AddMessage(tui.RoleSystem, "用法: /budget set <上限token数>（如 /budget set 16000）")
				break
			}
			n := 0
			fmt.Sscanf(args[1], "%d", &n)
			if n < 1000 {
				c.tui.AddMessage(tui.RoleSystem, "预算应为 ≥1000 的 token 数。")
				break
			}
			withSess(func(sess *types.Session) {
				if err := sessionum.SetBudget(c.app.SessStore, sess, n); err != nil {
					c.tui.AddMessage(tui.RoleSystem, "保存预算失败: "+err.Error())
				} else {
					c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已设置 Token 预算: %d\n每次请求估算超限会自动压缩为摘要 + 最近消息。", n))
				}
			})
		case "show":
			withSess(func(sess *types.Session) {
				if bg := sessionum.BudgetMax(sess); bg > 0 {
					line := fmt.Sprintf("当前 Token 预算: %d\n预警阈值: %d%%（/budget warn <50-95> 调整）", bg, sessionum.BudgetWarnPct(sess))
					if cnt, last, wc := sessionum.TrimStats(sess); cnt > 0 || wc > 0 {
						line += fmt.Sprintf("\n护栏记录: 自动压缩 %d 次", cnt)
						if last != "" {
							line += fmt.Sprintf("（最近 %s）", last)
						}
						if wc > 0 {
							line += fmt.Sprintf("，提前预警 %d 次", wc)
						}
					}
					c.tui.AddMessage(tui.RoleSystem, line)
				} else {
					c.tui.AddMessage(tui.RoleSystem, "当前没有 Token 预算。\n用法: /budget set <上限> | /budget show | /budget warn <50-95> | /budget clear")
				}
			})
		case "warn":
			if len(args) < 2 {
				withSess(func(sess *types.Session) {
					c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("当前预警阈值: %d%%\n用法: /budget warn <50-95> — 调整提前预警线", sessionum.BudgetWarnPct(sess)))
				})
				break
			}
			n := 0
			fmt.Sscanf(args[1], "%d", &n)
			withSess(func(sess *types.Session) {
				if err := sessionum.SetWarnPct(c.app.SessStore, sess, n); err != nil {
					c.tui.AddMessage(tui.RoleSystem, "设置预警阈值失败: "+err.Error())
				} else {
					c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("已设置预算预警阈值: %d%%", n))
				}
			})
		case "clear":
			withSess(func(sess *types.Session) {
				if err := sessionum.SetBudget(c.app.SessStore, sess, 0); err != nil {
					c.tui.AddMessage(tui.RoleSystem, "关闭预算失败: "+err.Error())
				} else {
					c.tui.AddMessage(tui.RoleSystem, "已关闭 Token 预算，恢复完整上下文。")
				}
			})
		default:
			c.tui.AddMessage(tui.RoleSystem, "用法: /budget set <上限> | /budget show | /budget warn <50-95> | /budget clear")
		}
	case "/clear":
		if c.sessionID != "" && c.app != nil && c.app.SessStore != nil {
			if sess, err := c.app.SessStore.Get(c.sessionID); err == nil {
				if err := sessionum.MarkDeleted(c.app.SessStore, sess); err != nil {
					c.tui.AddMessage(tui.RoleSystem, "归档失败（会话未删除）: "+err.Error())
				}
			}
		}
		c.sessionID = ""
		c.tui.LoadSession(nil)
		c.tui.AddMessage(tui.RoleSystem, "Session cleared (soft). /restore <id> 可恢复。")
	case "/summarize":
		if c.sessionID != "" && c.app != nil && c.app.SessStore != nil {
			if sess, err := c.app.SessStore.Get(c.sessionID); err == nil {
				mode := ""
				if c.app.Gate != nil {
					mode = string(c.app.Gate.Mode())
				}
				sum := sessionum.Generate(sess, c.tui.CurrentModel(), c.tui.CurrentProvider(), mode)
				if sum != "" && sessionum.Save(c.app.SessStore, sess, sum) == nil {
					c.tui.AddMessage(tui.RoleSystem, "✓ 会话摘要已存档（零 token，退出后可恢复）。")
				}
			}
		}
	case "/review":
		edits := searchreplace.StageForSession(c.sessionID).List()
		if len(edits) == 0 {
			c.tui.AddMessage(tui.RoleSystem, "No staged edits. Use the search_replace tool to propose changes first.")
			break
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("📋 Staged edits (%d):\n", len(edits)))
		for i, ed := range edits {
			status := "✓ valid"
			if !ed.Valid {
				status = "✗ invalid"
			}
			b.WriteString(fmt.Sprintf("\n── #%d %s [%s] ──\n", i, ed.FilePath, status))
			b.WriteString(fmt.Sprintf("   Reason: %s\n", ed.Reason))
			if ed.Valid && ed.Diff != "" {
				// Show diff with colored markers
				for _, line := range strings.Split(ed.Diff, "\n") {
					if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "@@") {
						b.WriteString(fmt.Sprintf("  %s\n", line))
					} else if strings.HasPrefix(line, "-") {
						b.WriteString(fmt.Sprintf("  \033[31m%s\033[0m\n", line))
					} else if strings.HasPrefix(line, "+") {
						b.WriteString(fmt.Sprintf("  \033[32m%s\033[0m\n", line))
					} else {
						b.WriteString(fmt.Sprintf("  %s\n", line))
					}
				}
			} else if !ed.Valid {
				b.WriteString(fmt.Sprintf("  (search text not found — cannot generate diff)\n"))
			}
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
		stage := searchreplace.StageForSession(c.sessionID)
		n := stage.Count()
		if n == 0 {
			c.tui.AddMessage(tui.RoleSystem, "No staged edits to apply.")
			break
		}

		// Snapshot files before applying (for atomic rollback)
		edits := stage.List()
		snapshottedFiles := make(map[string]bool)
		for _, ed := range edits {
			if ed.Valid && ed.FilePath != "" && !snapshottedFiles[ed.FilePath] {
				if checkpoint.DefaultUndo != nil {
					_, _ = checkpoint.DefaultUndo.SnapshotFile(context.Background(), ed.FilePath)
				}
				snapshottedFiles[ed.FilePath] = true
			}
		}

		results := stage.ApplyValid()

		// Check for failures — auto-rollback on any failure
		hasFailures := false
		for _, r := range results {
			c.tui.AddMessage(tui.RoleSystem, r)
			if strings.HasPrefix(r, "FAILED") {
				hasFailures = true
			}
		}

		applied := countApplied(results)
		if hasFailures && applied > 0 && checkpoint.DefaultUndo != nil {
			c.tui.AddMessage(tui.RoleSystem, "⚠️ 编辑失败，自动回滚中...")
			if restored, err := checkpoint.DefaultUndo.Undo(context.Background(), 1); err != nil {
				c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("回滚失败: %v — 请手动 /undo 1", err))
			} else {
				c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("✅ 已回滚 %d 个文件到编辑前的状态", len(restored)))
			}
		}

		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Applied %d/%d staged edits. Remaining: %d",
			applied, n, stage.Count()))

	case "/reject":
		stage := searchreplace.StageForSession(c.sessionID)
		n := stage.Count()
		if n == 0 {
			c.tui.AddMessage(tui.RoleSystem, "No staged edits to reject.")
			break
		}
		stage.Clear()
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Rejected %d staged edits.", n))

	case "/search":
		query := strings.Join(args, " ")
		if query == "" {
			c.tui.AddMessage(tui.RoleSystem, "Usage: /search <query> — search past conversations")
			break
		}
		if c.app == nil || c.app.SessStore == nil {
			c.tui.AddMessage(tui.RoleSystem, "No session store available.")
			break
		}
		results, err := c.app.SessStore.SearchMessages(query, 20)
		if err != nil {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Search error: %v", err))
			break
		}
		if len(results) == 0 {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("No results for %q.", query))
			break
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("🔍 Found %d results for %q:\n", len(results), query))
		for _, r := range results {
			title := r.SessionTitle
			if title == "" {
				title = "Untitled"
			}
			// Truncate content for display
			content := strings.ReplaceAll(r.Content, "\n", " ")
			if len(content) > 120 {
				content = content[:120] + "..."
			}
			b.WriteString(fmt.Sprintf("\n  [%s] %s\n", title, r.Role))
			b.WriteString(fmt.Sprintf("    %s\n", content))
		}
		b.WriteString(fmt.Sprintf("\n/resume <session_id> to load a session"))
		c.tui.AddMessage(tui.RoleSystem, b.String())

	case "/voice":
		// Toggle mic capture: first call starts recording, second stops and
		// transcribes via the configured ASR provider, then submits the text
		// as a message.
		if c.app == nil {
			c.tui.AddMessage(tui.RoleSystem, "语音输入暂不可用。")
			break
		}
		if c.voiceRec == nil {
			rec := voice.NewRecorder()
			if err := rec.Start(); err != nil {
				c.tui.AddMessage(tui.RoleSystem, "录音启动失败: "+err.Error())
				break
			}
			c.voiceRec = rec
			c.tui.AddMessage(tui.RoleSystem, "🎙 正在录音（最多30秒）…再次输入 /voice 结束并识别")
			break
		}
		wav, err := c.voiceRec.Stop()
		c.voiceRec = nil
		if err != nil {
			c.tui.AddMessage(tui.RoleSystem, "录音结束失败: "+err.Error())
			break
		}
		c.tui.AddMessage(tui.RoleSystem, "⏳ 正在识别语音…")

		// Determine provider from config
		provider := c.app.Cfg.Voice.Provider
		if provider == "" {
			provider = voice.ProviderZhipu
		}

		var text string
		var terr error

		switch provider {
		case voice.ProviderBaidu:
			apiKey := c.app.Cfg.Voice.BaiduAPIKey
			secretKey := c.app.Cfg.Voice.BaiduSecretKey
			text, terr = voice.TranscribeBaidu(context.Background(), apiKey, secretKey, wav)
		case voice.ProvideriFlytek:
			appID := c.app.Cfg.Voice.IFlytekAppID
			apiKey := c.app.Cfg.Voice.IFlytekAPIKey
			apiSecret := c.app.Cfg.Voice.IFlytekAPISecret
			text, terr = voice.TranscribeiFlytek(context.Background(), appID, apiKey, apiSecret, wav)
		default: // zhipu
			apiKey := c.app.Cfg.APIKey("zhipu")
			text, terr = voice.TranscribeZhipu(context.Background(), apiKey, wav, "voice.wav")
		}

		if terr != nil {
			c.tui.AddMessage(tui.RoleSystem, "识别失败: "+terr.Error())
			break
		}
		c.tui.AddMessage(tui.RoleSystem, "✅ 已识别:「"+text+"」")
		// Send immediately so the recognised text flows through the normal path.
		c.OnSend(text, nil)

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

// OnRenameSession implements tui.Callback — retitles the active session in the
// backend store so the sidebar / resume list reflect it immediately.
func (c *chatCallback) OnRenameSession(title string) string {
	if c.app == nil || c.app.SessStore == nil || c.sessionID == "" {
		return "无会话存储可用。"
	}
	sess, err := c.app.SessStore.Get(c.sessionID)
	if err != nil {
		return "会话不存在: " + c.sessionID
	}
	sess.Title = title
	if err := c.app.SessStore.Update(sess); err != nil {
		return "保存标题失败: " + err.Error()
	}
	return ""
}

// OnSetAskUser implements tui.Callback — wires the TUI's interactive
// multiple-choice asker into the engine so the ask_user_question tool can
// render options and read the user's choice (Claude Code AskUserQuestion
// parity). Headless / simpleui / desktop leave the engine's AskUser nil and
// the tool degrades gracefully.
func (c *chatCallback) OnSetAskUser(fn func(question string, options []string) (int, error)) {
	if c.app == nil || c.app.Engine == nil {
		return
	}
	c.app.Engine.AskUser = tool.AskUserFunc(fn)
}

// OnSetAskUserForm implements tui.Callback — wires the multi-question wizard
// asker into the engine (opencode AskQuestion parity).
func (c *chatCallback) OnSetAskUserForm(fn func(questions []tool.FormQuestion) ([]tool.FormAnswer, error)) {
	if c.app == nil || c.app.Engine == nil {
		return
	}
	c.app.Engine.AskUserForm = tool.AskUserFormFunc(fn)
}

// OnSetMode implements tui.Callback — switches the permission gate so the
// TUI's displayed mode and the enforced mode can never drift apart.
func (c *chatCallback) OnSetMode(mode string) string {
	if c.app == nil || c.app.Gate == nil {
		return ""
	}
	c.app.Gate.SetMode(permission.Mode(mode))
	return ""
}

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

// OnTokenStats implements tui.Callback — surfaces iCode's token-saving
// metrics so the Cache-First Loop is visible, not invisible.
func (c *chatCallback) OnTokenStats() string {
	if c.app == nil || c.app.Engine == nil {
		return "引擎未初始化。"
	}
	if c.sessionID == "" {
		return "没有活跃会话，先发一条消息再查看统计。"
	}
	stats := c.app.Engine.SessionStats(c.sessionID)
	if stats == nil {
		return "暂无统计数据。"
	}
	var b strings.Builder
	b.WriteString("🪙 iCode Token 节省报告\n\n")
	b.WriteString(fmt.Sprintf("已节省 Token:   %s\n", formatInt(stats.TokensSaved)))
	b.WriteString(fmt.Sprintf("缓存命中率:     %.1f%%\n", stats.CacheHitRate*100))
	b.WriteString(fmt.Sprintf("累计压缩次数:   %d\n", stats.CompactionsDone))
	b.WriteString(fmt.Sprintf("Prompt Token:   %s\n", formatInt(stats.PromptTokens)))
	b.WriteString(fmt.Sprintf("Completion:     %s\n", formatInt(stats.CompletionTokens)))
	b.WriteString(fmt.Sprintf("总 Token:       %s\n", formatInt(stats.TotalTokens)))
	if stats.CacheHitTokens > 0 {
		b.WriteString(fmt.Sprintf("缓存命中 Token: %s\n", formatInt(stats.CacheHitTokens)))
	}
	if stats.EstimatedCost > 0 {
		b.WriteString(fmt.Sprintf("预估费用:       ¥%.4f\n", stats.EstimatedCost))
	}
	if stats.EstimatedSavedCost > 0 {
		b.WriteString(fmt.Sprintf("预估节省:       ¥%.4f\n", stats.EstimatedSavedCost))
	}
	if len(stats.Rounds) > 0 {
		b.WriteString("\n每轮明细（缓存命中即可见节省）:\n")
		for _, r := range stats.Rounds {
			hit := "   miss"
			if r.CacheHit > 0 {
				hit = fmt.Sprintf("hit %8s", formatInt(r.CacheHit))
			}
			bar := cliSpark(r.Prompt)
			b.WriteString(fmt.Sprintf("  #%-2d  prompt %-9s comp %-7s  cache %s  ¥%.4f  %s\n",
				r.Turn, formatInt(r.Prompt), formatInt(r.Completion), hit, r.Cost, bar))
		}
	}
	b.WriteString("\n机制: Cache-First Loop（不可变前缀 + 追加日志 + 易失暂存）\n")
	b.WriteString("5 层压缩: Snip → 去重 → 折叠 → 摘要 → 预算上限")
	return b.String()
}

// cliSpark renders a tiny ASCII sparkline for prompt-size growth across turns
// (each block ≈ 2k tokens).
func cliSpark(prompt int) string {
	blocks := prompt / 2000
	if blocks > 12 {
		blocks = 12
	}
	return strings.Repeat("█", blocks) + strings.Repeat("░", 12-blocks)
}

// OnOutputStyle implements tui.Callback — applies an answer style live and
// persists it to config (takes effect from the next model turn).
func (c *chatCallback) OnOutputStyle(style string) string {
	style = strings.ToLower(strings.TrimSpace(style))
	if style != "concise" && style != "normal" && style != "verbose" {
		return "无效风格: " + style + "（可选 concise|normal|verbose）"
	}
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
	}
	cfg.Defaults.OutputStyle = style
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return "保存配置失败: " + err.Error()
	}
	if c.app != nil && c.app.Engine != nil {
		c.app.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
		return "输出风格已设为 " + style + "（已即时生效并持久化）"
	}
	return "输出风格已设为 " + style + "（已持久化，重启会话后生效）"
}

// OnAddDir implements tui.Callback — registers an extra working directory and
// re-applies the composed system prompt so the model can reference it.
func (c *chatCallback) OnAddDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "解析路径失败: " + err.Error()
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "目录不存在或不是文件夹: " + abs
	}
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
	}
	for _, d := range cfg.Defaults.ExtraDirs {
		if d == abs {
			return "该目录已在工作目录列表中: " + abs
		}
	}
	cfg.Defaults.ExtraDirs = append(cfg.Defaults.ExtraDirs, abs)
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return "保存配置失败: " + err.Error()
	}
	if c.app != nil && c.app.Engine != nil {
		c.app.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
	}
	return fmt.Sprintf("✓ 已添加工作目录: %s（共 %d 个，已注入上下文）", abs, len(cfg.Defaults.ExtraDirs))
}

// OnUpdateModels implements tui.Callback — refreshes provider model catalogs
// and the TUI model picker list.
func (c *chatCallback) OnUpdateModels() string {
	if c.app == nil {
		return "引擎未初始化。"
	}
	updates, err := c.app.RefreshModels(context.Background())
	if err != nil && len(updates) == 0 {
		return "刷新失败: " + err.Error()
	}
	var b strings.Builder
	b.WriteString("模型目录刷新结果：\n")
	ok, fail := 0, 0
	for _, u := range updates {
		if u.Success {
			ok++
			b.WriteString(fmt.Sprintf("  ✓ %-14s %d 个模型（%s）\n", u.Name, u.Count, u.Source))
		} else {
			fail++
			msg := u.Error
			if msg == "" {
				msg = "未知错误"
			}
			b.WriteString(fmt.Sprintf("  ✗ %-14s %s\n", u.Name, msg))
		}
	}
	b.WriteString(fmt.Sprintf("成功 %d · 失败 %d", ok, fail))
	// Refresh the TUI's model list so /model and Tab switching see updates.
	if c.app.Reg != nil && c.tui != nil {
		if all := c.app.Reg.ListAllModels(); len(all) > 0 {
			ids := make([]string, 0, len(all))
			for _, m := range all {
				ids = append(ids, m.ID)
			}
			c.tui.SetModels(ids)
		}
	}
	return b.String()
}

func (c *chatCallback) OnAddCustomModel(provider, modelID, name string) string {
	if c.app == nil || c.app.Reg == nil {
		return "引擎未初始化。"
	}
	if provider == "" || modelID == "" {
		return "用法: /models add <provider> <model_id> [name]"
	}
	if name == "" {
		name = modelID
	}
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
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
		return "保存配置失败: " + err.Error()
	}
	// Live-register so the model works immediately (no restart needed).
	c.app.RegisterCustomModel(m)
	if c.tui != nil {
		if all := c.app.Reg.ListAllModels(); len(all) > 0 {
			ids := make([]string, 0, len(all))
			for _, mm := range all {
				ids = append(ids, mm.ID)
			}
			c.tui.SetModels(ids)
		}
	}
	return fmt.Sprintf("✓ 已添加自定义模型 %s（%s / %s）", m.ID, provider, name)
}

func (c *chatCallback) OnRemoveCustomModel(id string) string {
	if c.app == nil || c.app.Reg == nil {
		return "引擎未初始化。"
	}
	if id == "" {
		return "用法: /models rm <id>（id 形如 provider/model_id）"
	}
	cfg, err := config.Load()
	if err != nil {
		return "读取配置失败: " + err.Error()
	}
	if !cfg.DeleteModel(id) {
		return fmt.Sprintf("模型 %q 未找到。", id)
	}
	if err := cfg.Save(config.DefaultPath()); err != nil {
		return "保存配置失败: " + err.Error()
	}
	c.app.RemoveCustomModel(id)
	if c.tui != nil {
		if all := c.app.Reg.ListAllModels(); len(all) > 0 {
			ids := make([]string, 0, len(all))
			for _, mm := range all {
				ids = append(ids, mm.ID)
			}
			c.tui.SetModels(ids)
		}
	}
	return fmt.Sprintf("✓ 已移除自定义模型 %s", id)
}

// LSPQuery implements tui.Callback — runs an on-demand LSP code-intelligence
// query for /lsp. Delegates to the shared lsp.Manager.QueryReport so the TUI
// behaves identically to the HTTP slash layer.
func (c *chatCallback) LSPQuery(sub string, args []string) string {
	if c.app == nil || c.app.LSPManager == nil {
		return "LSP 未启用。请在配置中开启 lsp.enabled（config.toml 或 /config）。"
	}
	out, err := c.app.LSPManager.QueryReport(sub, args)
	if err != nil {
		return "⚠ " + err.Error()
	}
	return out
}

// KnowledgeQuery implements tui.Callback — searches the local document
// knowledge base for /kb.
func (c *chatCallback) KnowledgeQuery(query string) string {
	if c.app == nil || c.app.Knowledge == nil {
		return "知识库未配置。请在 config.yaml 的 knowledge.dirs 中指定文档目录。"
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Sprintf("知识库状态：已索引 %d 个片段。\n用法: /kb <查询>", c.app.Knowledge.ChunkCount())
	}
	results := c.app.Knowledge.Search(query, 5)
	return knowledge.Format(results)
}

// CreateIdleTask implements tui.Callback — creates an off-peak task for /idle.
func (c *chatCallback) CreateIdleTask(name, prompt string) string {
	if c.app == nil || c.app.Scheduler == nil {
		return "调度器不可用（需持久化后端）。"
	}
	t, err := c.app.Scheduler.Create(name, prompt, "idle")
	if err != nil {
		return "创建闲时任务失败: " + err.Error()
	}
	return fmt.Sprintf("✓ 已创建闲时任务「%s」（ID: %s）\n将在闲时窗口（低峰时段）自动执行，完成后通知你。", t.Name, t.ID)
}

// OnPermissionNote implements tui.Callback — records the reason the user gave
// when rejecting a tool call (Tab note on the permission prompt), so the agent
// sees it on the next turn instead of retrying blindly.
func (c *chatCallback) OnPermissionNote(toolPrompt, note string) {
	if c.app == nil || c.app.SessStore == nil || c.sessionID == "" {
		return
	}
	sess, err := c.app.SessStore.Get(c.sessionID)
	if err != nil {
		return
	}
	sess.Messages = append(sess.Messages, types.Message{
		Role:      types.RoleSystem,
		Content:   fmt.Sprintf("用户拒绝了这次操作，原因：%s", note),
		Timestamp: time.Now(),
	})
	_ = c.app.SessStore.Update(sess)
	c.tui.AddMessage(tui.RoleSystem, "已拒绝并说明："+note)
}

// formatInt renders an integer with thousands separators.
func formatInt(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
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
			Version:  appVersion,
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
