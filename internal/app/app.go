// Package app provides the bootstrap and lifecycle for the iCode application.
// It wires together all subsystems: providers, tools, sessions, permissions, and updates.
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/db"
	"github.com/ponygates/icode/internal/llm/provider"
	"github.com/ponygates/icode/internal/llm/provider/anthropic"
	"github.com/ponygates/icode/internal/llm/provider/deepseek"
	"github.com/ponygates/icode/internal/llm/provider/huawei"
	"github.com/ponygates/icode/internal/llm/provider/kimi"
	"github.com/ponygates/icode/internal/llm/provider/openrouter"
	"github.com/ponygates/icode/internal/llm/provider/scnet"
	"github.com/ponygates/icode/internal/llm/provider/tencent"
	"github.com/ponygates/icode/internal/llm/provider/volcengine"
	"github.com/ponygates/icode/internal/llm/provider/zhipu"
	"github.com/ponygates/icode/internal/types"
	"github.com/ponygates/icode/pkg/modelupdate"
)

// App is the top-level application container.
type App struct {
	Cfg       *config.Config
	Reg       *registry.Impl
	SessStore types.SessionStore
	DB        *db.Store // SQLite-backed store (may be nil)
	Engine    *conversation.Engine
	Gate      *permission.Gate
	Updater   *modelupdate.Service
}

// Bootstrap initializes all subsystems and returns a ready-to-use App.
func Bootstrap() (*App, error) {
	app := &App{}

	// 1. Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Printf("[iCode] Config load warning: %v; using defaults", err)
		cfg = config.Default()
	}
	app.Cfg = cfg

	// 2. Try SQLite persistence first, fall back to in-memory
	dbStore, err := db.New(db.Config{})
	if err != nil {
		log.Printf("[iCode] SQLite init failed: %v; using in-memory store", err)
		app.SessStore = session.NewStore()
	} else {
		app.DB = dbStore
		app.SessStore = dbStore
	}

	// 3. Initialize provider registry
	app.Reg = registry.NewRegistry()
	app.registerProviders(cfg)

	// 4. Initialize permission gate
	app.Gate = permission.NewGate(permission.ModeAgent)

	// 5. Initialize conversation engine
	app.Engine = conversation.NewEngine(app.Reg, app.SessStore)

	// 6. Initialize model update service
	home, _ := os.UserHomeDir()
	cacheDir := filepath.Join(home, ".icode", "cache")
	app.Updater = modelupdate.NewService(cacheDir)
	for _, name := range app.Reg.List() {
		p, _ := app.Reg.Get(name)
		app.Updater.Register(p)
	}

	return app, nil
}

// registerProviders adds all built-in providers to the registry.
func (app *App) registerProviders(cfg *config.Config) {
	providers := []struct {
		name string
		fn   func(key, base string) types.Provider
	}{
		{"deepseek", func(k, b string) types.Provider { return deepseek.New(k, b) }},
		{"zhipu", func(k, b string) types.Provider { return zhipu.New(k, b) }},
		{"kimi", func(k, b string) types.Provider { return kimi.New(k, b) }},
		{"openrouter", func(k, b string) types.Provider { return openrouter.New(k, b) }},
		{"volcengine", func(k, b string) types.Provider { return volcengine.New(k, b) }},
		{"tencent", func(k, b string) types.Provider { return tencent.New(k, b) }},
		{"huawei", func(k, b string) types.Provider { return huawei.New(k, b) }},
		{"scnet", func(k, b string) types.Provider { return scnet.New(k, b) }},
	}

	for _, entry := range providers {
		provCfg, ok := cfg.Providers[entry.name]
		if !ok {
			provCfg = config.ProviderCfg{}
		}
		if provCfg.Disabled {
			continue
		}

		p := entry.fn(provCfg.APIKey, provCfg.APIBase)
		if err := app.Reg.Register(p); err != nil {
			log.Printf("[iCode] Failed to register %s: %v", entry.name, err)
		}
	}

	// Anthropic has its own Provider interface (not OpenAI-compatible)
	anthropicCfg, ok := cfg.Providers["anthropic"]
	if !ok {
		anthropicCfg = config.ProviderCfg{}
	}
	if !anthropicCfg.Disabled {
		ap := anthropic.New(anthropicCfg.APIKey, anthropicCfg.APIBase)
		if err := app.Reg.Register(ap); err != nil {
			log.Printf("[iCode] Failed to register anthropic: %v", err)
		}
	}
}

// Close shuts down all subsystems gracefully.
func (app *App) Close() error {
	if app.DB != nil {
		return app.DB.Close()
	}
	return nil
}

// RefreshModels triggers a model list refresh from all providers.
func (app *App) RefreshModels(ctx context.Context) ([]modelupdate.ProviderUpdate, error) {
	return app.Updater.UpdateAll(ctx)
}

// PrintProviderStatus displays the current provider registration status.
func (app *App) PrintProviderStatus() {
	fmt.Println("\nRegistered Providers:")
	fmt.Println("  " + fmt.Sprintf("%-14s %-8s %s", "Name", "Models", "Cache"))
	fmt.Println("  " + "----------------------------------------")
	for _, name := range app.Reg.List() {
		p, err := app.Reg.Get(name)
		if err != nil {
			continue
		}
		cache := "No"
		if p.SupportsCache() {
			cache = "Yes"
		}
		fmt.Printf("  %-14s %-8d %s\n", name, len(p.ListModels()), cache)
	}

	// Check SQLite status
	if app.DB != nil {
		sessions, _ := app.SessStore.List(100, 0)
		fmt.Printf("\n  SQLite: active (%d stored sessions)\n", len(sessions))
	} else {
		fmt.Println("\n  SQLite: not available (in-memory mode)")
	}
}
