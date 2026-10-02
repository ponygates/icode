package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/config/i18n"
	registry "github.com/ponygates/icode/internal/llm/provider"
	"github.com/spf13/cobra"
)

var modelCmd = &cobra.Command{
	Use:   "model",
	Short: "查看与管理可用模型",
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
