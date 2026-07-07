package tui

import (
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/provider"
	"github.com/ponygates/icode/internal/tools"
)

type App struct {
	cfg    *config.Config
	agent  *AgentWrapper
}

type AgentWrapper struct {
	registry *provider.Registry
	provider provider.Provider
	tools    []provider.ToolDef
}

func New(cfg *config.Config) *App {
	reg := provider.NewRegistry()
	return &App{
		cfg: cfg,
		agent: &AgentWrapper{
			registry: reg,
		},
	}
}

func (a *App) Run() error {
	fmt.Println("iCode v0.1.0 — AI Coding Agent")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("Privacy mode: %s\n", a.cfg.Privacy.Mode)
	fmt.Printf("Default model: %s\n", a.cfg.Provider.Default)
	fmt.Printf("Permission mode: %s\n\n", a.cfg.Permission.Mode)
	fmt.Println("Interactive mode coming soon with Bubble Tea TUI.")
	fmt.Println("For now, use the direct REPL mode.")
	return nil
}

func (a *App) ProcessInput(input string) {
	toolCfg := tools.Config{
		WorkspaceRoot: ".",
		Timeout:       120000,
	}
	_ = toolCfg
	fmt.Printf("You said: %s\n", input)
}
