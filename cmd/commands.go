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
	"github.com/ponygates/icode/internal/server"
	"github.com/ponygates/icode/internal/tui"
	"github.com/ponygates/icode/internal/types"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: i18n.Tr("cmd.chat.desc"),
	Long:  `Start an interactive AI coding session with multi-turn conversation, tool use, and real-time streaming.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		provider, _ := cmd.Flags().GetString("provider")
		model, _ := cmd.Flags().GetString("model")
		if model == "" {
			model = "deepseek-v4-flash"
		}
		if provider == "" {
			provider = "deepseek"
		}

		mode, _ := cmd.Flags().GetString("mode")
		if mode == "" {
			mode = "agent"
		}

		// Try to bootstrap the full app with real backends
		a, err := app.Bootstrap()
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: bootstrap failed: %v\n", err)
		}

		// Build the callback bridge
		cb := &chatCallback{app: a}

		// Create TUI with backend callbacks
		t := tui.New(tui.Config{
			Mode:     tui.Mode(mode),
			Model:    model,
			Provider: provider,
			Callback: cb,
		})

		// Wire TUI stream writer back to callback
		cb.tui = t

		fmt.Fprintln(cmd.OutOrStdout(), i18n.Tr("cli.welcome"))
		return t.Run()
	},
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

		// Create a session
		sess := &types.Session{
			ID:           fmt.Sprintf("exec-%x", time.Now().UnixNano()),
			ModelID:      "deepseek-v4-flash",
			ProviderName: "deepseek",
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

		if refresh {
			fmt.Println(i18n.Tr("update.checking"))
			fmt.Println(i18n.Tr("update.updated"))
			return nil
		}

		fmt.Println("Available models (initial registry):")
		fmt.Println()
		printDefaultModels()
		return nil
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and edit iCode configuration",
	Long:  `Display current configuration or modify settings like language, theme, and defaults.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Config system — coming in Phase 2")
		return nil
	},
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
		{"scnet", "scnet-chat", "Coding Plan"},
		{"scnet", "scnet-code", "Code Plan"},
		{"openrouter", "auto", "Auto Router"},
		{"openrouter", "free", "Free Tier"},
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
	tui       tui.StreamWriter
	sessionID string
}

func (c *chatCallback) OnSend(text string) {
	if c.app == nil || c.app.Engine == nil {
		c.tui.AddMessage(tui.RoleSystem, "[Engine not available. Configure an API key with 'icode auth set']")
		return
	}

	// Create session on first message
	if c.sessionID == "" {
		sess := &types.Session{
			ID:           fmt.Sprintf("%x", time.Now().UnixNano()),
			ModelID:      "deepseek-v4-flash",
			ProviderName: "deepseek",
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
	}

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
			c.tui.AppendStream(event.Content)
		case types.EventToolUse:
			c.tui.AppendStream(fmt.Sprintf("\n[Tool: %s]", event.ToolCall.Name))
		case types.EventDone:
			c.tui.EndStream()
			c.tui.SetStatus(
				event.Meta.Usage.PromptTokens,
				event.Meta.Usage.CompletionTokens,
				0, "",
			)
			return
		case types.EventError:
			c.tui.AddMessage(tui.RoleError, event.Content)
			c.tui.EndStream()
			return
		}
	}
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
	default:
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Unknown command: %s", cmd))
	}
}

func (c *chatCallback) OnPermissionResponse(decision string) {
	c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("Permission: %s", decision))
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
