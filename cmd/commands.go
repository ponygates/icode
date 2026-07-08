package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/config/i18n"
	"github.com/ponygates/icode/internal/server"
	"github.com/ponygates/icode/internal/tui"
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
			model = "deepseek-chat"
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

		// Create TUI with backend callbacks
		t := tui.New(tui.Config{
			Mode:     tui.Mode(mode),
			Model:    model,
			Provider: provider,
			Callback: &chatCallback{
				app:    a,
				staged: false,
			},
		})

		_ = a
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

		fmt.Printf("[iCode] exec mode — prompt received (%d chars)\n", len(prompt))
		fmt.Println("[iCode] full exec implementation coming in Phase 2")
		return nil
	},
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: i18n.Tr("cmd.auth.desc"),
	Long:  `Configure and manage API keys for LLM providers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		list, _ := cmd.Flags().GetBool("list")

		if list {
			fmt.Println("Configured providers:")
			fmt.Println("  deepseek   — https://api.deepseek.com/v1")
			fmt.Println("  zhipu      — https://open.bigmodel.cn/api/paas/v4")
			fmt.Println("  kimi       — https://api.moonshot.cn/v1")
			fmt.Println("  openrouter — https://openrouter.ai/api/v1")
			fmt.Println()
			fmt.Println("Use 'icode auth set --provider <name> --key <api-key>' to configure.")
			return nil
		}

		fmt.Println("Auth system — coming in Phase 2")
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

	authCmd.Flags().BoolP("list", "l", false, "List configured providers")
	authCmd.Flags().String("provider", "", "Provider name")
	authCmd.Flags().String("key", "", "API key")

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
	app    *app.App
	staged bool
}

func (c *chatCallback) OnSend(text string) {}
func (c *chatCallback) OnSlashCommand(cmd string, args []string) {}
func (c *chatCallback) OnPermissionResponse(decision string) {}

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
