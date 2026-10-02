package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/config/i18n"
	"github.com/ponygates/icode/internal/core/auth"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/embedded"
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
	Long:  `Start an interactive AI coding session with multi-turn conversation, tool use, and real-time streaming.`, RunE: func(cmd *cobra.Command, args []string) error {
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
	// 误构建防线：CLI 若为 GUI 子系统（-H windowsgui）构建，中文 IME
	// 桥接会损坏（拼音漏入 + 上屏串直接写屏）。详见 subsystem_check.go。
	warnIfGUISubsystem()

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
			// Hot-reload lifecycle hooks too: any persistSetting write
			// (e.g. /hooks add in the TUI) applies without a restart.
			wd, _ := os.Getwd()
			a.Engine.SetHooksRunner(hooks.NewRunner(hooks.RulesFromConfig(c.Hooks), wd))
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
			return cb.tui.PromptPermission(req.Prompt, res.Severity)
		})
	}

	fmt.Fprintln(os.Stdout)

	// Auto-resume the most recently updated session so the CLI continues the
	// conversation desktop last touched — both ends share one
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
		noVerify, _ := cmd.Flags().GetBool("no-verify")

		cfg, err := config.Load()
		if err != nil {
			cfg = config.Default()
		}

		if list {
			fmt.Println("\nConfigured providers:")
			for name, pc := range cfg.Providers {
				masked := "(not set)"
				if pc.APIKey != "" {
					masked = auth.Masked(pc.APIKey)
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
			return cfg.Save(config.DefaultPath())
		}

		if show {
			pc, ok := cfg.Providers[provider]
			if !ok || pc.APIKey == "" {
				return fmt.Errorf("no API key configured for %s", provider)
			}
			reveal, _ := cmd.Flags().GetBool("reveal")
			shown := auth.Masked(pc.APIKey)
			if reveal {
				shown = pc.APIKey
			} else {
				fmt.Println("（默认打码显示，加 --reveal 查看完整 Key）")
			}
			fmt.Printf("%s API Key: %s\n", provider, shown)
			return nil
		}

		if key == "" {
			return fmt.Errorf("--key flag is required (use --show to view, --delete to remove)")
		}

		res, err := auth.Save(provider, key)
		if err != nil {
			return err
		}
		fmt.Printf("✓ API key saved for %s (%s) → %s\n", res.Provider, res.Masked, res.Path)
		if noVerify {
			return nil
		}
		return verifyAndReport(provider)
	},
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "检查并自更新到最新版 iCode",
	Long: `Download the latest Windows amd64 release from GitHub and swap the
running binary in place. The new version activates on next launch.
The previous binary is kept as <exe>.old for manual rollback.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		fmt.Println("🔄 正在检查新版本…")
		if !update.PublicKeyConfigured() {
			// Developer builds carry the placeholder release key: the updater
			// fails closed instead of silently trusting a download.
			fmt.Println("⚠ 本构建未内置发布签名公钥（占位值），自动更新将 fail closed；" +
				"应急可用 ICODE_UPDATE_ALLOW_UNSIGNED=1 仅按 sha256 安装（会打印安全警告）。")
		}
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

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "诊断系统健康状态与连通性",
	Long:  `检查版本环境、LLM Provider 状态、数据库健康度与整体系统配置。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Keep the one-shot diagnostic output clean: bootstrap progress logs
		// only appear with --verbose (they still help diagnose slow startups).
		if verbose, _ := cmd.Flags().GetBool("verbose"); !verbose {
			app.SetBootstrapQuiet(true)
		}
		a, err := app.Bootstrap()
		if err != nil {
			fmt.Printf("Bootstrap error: %v\n", err)
			return err
		}
		defer a.Close()

		// Environment header (Claude Code /doctor parity): version, platform
		// and the on-disk locations the user may need to inspect manually.
		fmt.Printf("iCode v%s · %s/%s\n", appVersion, runtime.GOOS, runtime.GOARCH)
		if appCommit != "" && appCommit != "unknown" {
			fmt.Printf("commit: %s\n", appCommit)
		}
		fmt.Printf("配置:   %s\n", config.DefaultPath())
		if a.Cfg != nil && a.Cfg.Defaults.Provider != "" {
			fmt.Printf("默认:   %s / %s\n", a.Cfg.Defaults.Provider, a.Cfg.Defaults.Model)
		}
		fmt.Println()

		// Startup benchmark (Claude Code /doctor startup profile parity):
		// total bootstrap wall time + per-stage deltas, slowest first, with
		// a hint on anything unreasonably slow.
		if total := a.BootTotalMillis(); total > 0 {
			fmt.Printf("启动耗时: 共 %dms\n", total)
			for _, bt := range a.BootStageDeltas() {
				flag := ""
				if bt.Millis >= 500 {
					flag = "  ⚠️ 慢"
				}
				fmt.Printf("  %-24s %4dms%s\n", bt.Stage, bt.Millis, flag)
			}
			fmt.Println()
		}

		a.PrintProviderStatus()
		fmt.Println()
		fmt.Println("iCode system check complete.")
		return nil
	},
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "启动后端 API 服务器（供桌面端/扩展使用）",
	Long:  `Start an HTTP API server for the desktop app (WebView2 / browser) or remote API access.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")

		a, err := app.Bootstrap()
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		defer a.Close()

		srv := server.New(server.ServerConfig{
			Config:     a.Cfg,
			Registry:   a.Reg,
			Store:      a.SessStore,
			DB:         a.DB,
			Engine:     a.Engine,
			Gate:       a.Gate,
			Updater:    a.Updater,
			MCPPool:    a.MCPPool,
			MCPRefresh: a.RefreshMCPTools,
			Version:    appVersion,
			Port:       port,
		})

		// Serve the embedded desktop UI so `icode server` is a complete
		// headless web-UI mode (browser on any device → this port).
		if f := embedded.Frontend(); f != nil {
			server.SetEmbeddedFrontend(f)
		}

		ctx := context.Background()
		actualPort, err := srv.Start(ctx)
		if err != nil {
			return fmt.Errorf("start server: %w", err)
		}

		fmt.Printf("iCode server running on http://127.0.0.1:%d\n", actualPort)
		// Mutating endpoints (shell/config/permission/update) require the
		// per-launch Bearer token; the web UI reads it from the URL query,
		// stashes and strips it. Print the ready-to-open URL so users don't
		// have to assemble it by hand.
		fmt.Printf("Web UI: http://127.0.0.1:%d/?token=%s\n", actualPort, srv.APIToken())

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
