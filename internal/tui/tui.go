package tui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/agent"
	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/permissions"
	"github.com/ponygates/icode/internal/provider"
	"github.com/ponygates/icode/internal/provider/optimizer"
	"github.com/ponygates/icode/internal/tools"
)

type App struct {
	cfg    *config.Config
	agent  *agent.Agent
	perm   *permissions.Manager
	reg    *provider.Registry
}

func New(cfg *config.Config, reg *provider.Registry) *App {
	perm := permissions.New(
		cfg.Permission.Mode,
		cfg.Permission.ReadOnlyDirs,
		cfg.Permission.DenyDirs,
		cfg.Permission.BashDenyCmds,
		".",
	)

	pk := provider.ParseModelKey(cfg.Provider.Default)
	prov := reg.Get(pk.Name)

	toolCfg := tools.Config{
		WorkspaceRoot: ".",
		Permissions:   perm,
		Timeout:       120 * time.Second,
	}

	allTools := []agent.Tool{
		tools.NewReadTool(toolCfg),
		tools.NewWriteTool(toolCfg),
		tools.NewEditTool(toolCfg),
		tools.NewBashTool(toolCfg),
		tools.NewGrepTool(toolCfg),
		tools.NewGlobTool(toolCfg),
	}

	agt := agent.New(prov, allTools, agent.Config{
		SystemPrompt: defaultSystemPrompt,
		MaxTurns:     cfg.Permission.MaxTurns,
		MaxTokens:    4096,
		Model:        pk.Model,
		Profile:      optimizer.ForProvider(pk.Name, pk.Model),
	})

	return &App{
		cfg:   cfg,
		agent: agt,
		perm:  perm,
		reg:   reg,
	}
}

func (a *App) Run() error {
	fmt.Println(a.banner())
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("iCode> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return err
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if strings.HasPrefix(input, "/") {
			if a.handleCommand(input) {
				continue
			}
			continue
		}

		a.processInput(input)
	}
}

func (a *App) handleCommand(cmd string) bool {
	switch {
	case cmd == "/quit" || cmd == "/exit":
		fmt.Println("bye")
		os.Exit(0)
		return true

	case cmd == "/clear":
		a.agent.ClearHistory()
		fmt.Println("history cleared")
		return true

	case cmd == "/help":
		a.printHelp()
		return true

	case cmd == "/providers":
		for _, name := range a.reg.List() {
			p := a.reg.Get(name)
			fmt.Printf("  %-12s %s\n", name, strings.Join(p.Models(), ", "))
		}
		return true

	case cmd == "/profile":
		prof := a.agent.Profile()
		fmt.Printf("Provider:  %s\n", prof.Provider)
		fmt.Printf("Model:     %s\n", prof.Model)
		fmt.Printf("Temperature: %.1f\n", prof.Temperature)
		fmt.Printf("TopP:      %.1f\n", prof.TopP)
		fmt.Printf("MaxTokens: %d\n", prof.MaxTokens)
		fmt.Printf("ToolStyle: %s\n", prof.ToolStyle)
		fmt.Printf("StripThink: %v\n", prof.StripThinkTag)
		fmt.Printf("PromptLen: %d chars\n", len(prof.SystemPrompt))
		return true

	case strings.HasPrefix(cmd, "/model "):
		modelKey := strings.TrimSpace(strings.TrimPrefix(cmd, "/model "))
		pk := provider.ParseModelKey(modelKey)
		prov := a.reg.Get(pk.Name)
		if prov == nil {
			fmt.Printf("unknown provider: %s\n", pk.Name)
			return true
		}
		newTools := a.createTools()
		prof := optimizer.ForProvider(pk.Name, pk.Model)
		a.agent = agent.New(prov, newTools, agent.Config{
			SystemPrompt: defaultSystemPrompt,
			MaxTurns:     a.cfg.Permission.MaxTurns,
			MaxTokens:    4096,
			Model:        pk.Model,
			Profile:      prof,
		})
		fmt.Printf("switched to %s/%s\n", pk.Name, pk.Model)
		fmt.Printf("optimization: temp=%.1f topP=%.1f stripThink=%v\n",
			prof.Temperature, prof.TopP, prof.StripThinkTag)
		return true

	case cmd == "/mode":
		fmt.Printf("permission mode: %s\n", a.cfg.Permission.Mode)
		fmt.Printf("privacy mode: %s\n", a.cfg.Privacy.Mode)
		fmt.Printf("model: %s\n", a.cfg.Provider.Default)
		prof := a.agent.Profile()
		fmt.Printf("profile: temp=%.1f topP=%.1f maxTok=%d tool=%s stripThink=%v\n",
			prof.Temperature, prof.TopP, prof.MaxTokens, prof.ToolStyle, prof.StripThinkTag)
		return true
	}

	return false
}

func (a *App) createTools() []agent.Tool {
	toolCfg := tools.Config{
		WorkspaceRoot: ".",
		Permissions:   a.perm,
		Timeout:       120 * time.Second,
	}
	return []agent.Tool{
		tools.NewReadTool(toolCfg),
		tools.NewWriteTool(toolCfg),
		tools.NewEditTool(toolCfg),
		tools.NewBashTool(toolCfg),
		tools.NewGrepTool(toolCfg),
		tools.NewGlobTool(toolCfg),
	}
}

func (a *App) processInput(input string) {
	ctx := context.Background()

	a.agent.OnEvent(func(ev agent.StreamEvent) {
		switch ev.Type {
		case "text":
			fmt.Print(ev.Content)
		case "tool_call":
			fmt.Printf("\n\n🔧 Tool Call: %s\n", ev.Content)
		case "tool_result":
			result := ev.Content
			if len(result) > 500 {
				result = result[:500] + fmt.Sprintf("\n... (%d more bytes)", len(ev.Content)-500)
			}
			fmt.Printf("📎 Result: %s\n", result)
		case "done":
			fmt.Println()
		case "thinking":
			fmt.Print("💭 ")
		}
	})

	fmt.Println()
	if err := a.agent.Run(ctx, input); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
	}
	fmt.Println()
}

func (a *App) printHelp() {
	fmt.Print(`Commands:
  /quit, /exit       Exit iCode
  /clear             Clear conversation history
  /help              Show this help
  /providers         List available providers
  /model <name>      Switch model (e.g. /model deepseek/deepseek-v4-flash)
  /mode              Show current modes
  /profile           Show provider optimization profile
  /model <name>      Auto-optimized for each provider
`)
}

func (a *App) banner() string {
	return fmt.Sprintf(`  ___   ___  ___  ___  ___
 / _ \ / __|/ _ \| __|/ __|
| (_) | (__|  __/| _| \__ \
 \___/ \___|\___/|___||___/
 iCode v0.1.0
────────────────────────────
 Privacy: %s   Perm: %s   Model: %s`,
		a.cfg.Privacy.Mode,
		a.cfg.Permission.Mode,
		a.cfg.Provider.Default,
	)
}

const defaultSystemPrompt = `You are iCode, an AI coding assistant powered by a multi-provider LLM engine.

You have access to a set of tools you can use to help the user with their tasks.
Always think through what tools you need and use them sequentially.

Follow these guidelines:
1. First, understand what the user wants.
2. Use tools to explore, read, and modify the codebase.
3. When writing code, follow existing conventions, patterns, and style.
4. Always check what exists before making changes.
5. Use bash for building, testing, and git operations.
6. Be concise and direct in your responses.
7. When making edits, prefer the edit tool for targeted changes.`
