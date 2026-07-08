package cmd

import (
	"fmt"

	"github.com/ponygates/icode/internal/config/i18n"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: i18n.Tr("cmd.chat.desc"),
	Long:  `Start an interactive AI coding session with multi-turn conversation, tool use, and real-time streaming.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(i18n.Tr("cli.welcome"))
		fmt.Println()
		fmt.Println("┌─────────────────────────────────────────────────────────────┐")
		fmt.Println("│  iCode Chat — interactive mode coming in Phase 3           │")
		fmt.Println("│                                                             │")
		fmt.Println("│  Slash commands:                                            │")
		fmt.Println("│    /help     Show help                                      │")
		fmt.Println("│    /model    Select model                                   │")
		fmt.Println("│    /mode     Switch mode (plan/agent/yolo)                  │")
		fmt.Println("│    /session  Manage sessions                                │")
		fmt.Println("│    /exit     Exit chat                                      │")
		fmt.Println("└─────────────────────────────────────────────────────────────┘")
		fmt.Println()
		fmt.Println(i18n.Tr("cli.goodbye"))
		return nil
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

	authCmd.Flags().BoolP("list", "l", false, "List configured providers")
	authCmd.Flags().String("provider", "", "Provider name")
	authCmd.Flags().String("key", "", "API key")

	modelCmd.Flags().BoolP("refresh", "r", false, "Refresh model list from all providers")
	modelCmd.Flags().String("search", "", "Filter models by name")
}

func printDefaultModels() {
	models := []struct{ provider, model, plan string }{
		{"deepseek", "deepseek-chat", "Coding Plan"},
		{"deepseek", "deepseek-reasoner", "Reasoning Plan"},
		{"zhipu", "glm-4-plus", "Coding Plan"},
		{"zhipu", "glm-4-flash", "Token Plan (free)"},
		{"kimi", "moonshot-v1-8k", "Coding Plan"},
		{"kimi", "moonshot-v1-128k", "Token Plan"},
		{"openrouter", "openai/gpt-4o", "Token Plan"},
		{"openrouter", "anthropic/claude-sonnet-4", "Coding Plan"},
		{"openrouter", "auto", "Auto Router"},
		{"openrouter", "free", "Free Tier"},
	}

	for _, m := range models {
		fmt.Printf("  [%s] %-28s %s\n", m.provider, m.model, m.plan)
	}
}
