// Package app provides the bootstrap and lifecycle for the iCode application.
// It wires together all subsystems: providers, tools, sessions, permissions, and updates.
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/lsp"
	"github.com/ponygates/icode/internal/core/router"
	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/db"
	"github.com/ponygates/icode/internal/llm/provider"
	"github.com/ponygates/icode/internal/llm/provider/anthropic"
	"github.com/ponygates/icode/internal/llm/provider/deepseek"
	"github.com/ponygates/icode/internal/llm/provider/huawei"
	"github.com/ponygates/icode/internal/llm/provider/kimi"
	"github.com/ponygates/icode/internal/llm/provider/nvidia"
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
	LSPManager *lsp.Manager // nil when LSP disabled
}

// Bootstrap initializes all subsystems and returns a ready-to-use App.
func Bootstrap() (*App, error) {
	app := &App{}
	t0 := time.Now()

	// 1. Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Printf("[iCode] Config load warning: %v; using defaults", err)
		cfg = config.Default()
	}
	app.Cfg = cfg
	log.Printf("[iCode] bootstrap: config loaded (t=%dms)", time.Since(t0).Milliseconds())

	// 2. Try SQLite persistence first, fall back to in-memory
	dbStore, err := db.New(db.Config{})
	if err != nil {
		log.Printf("[iCode] SQLite init failed: %v; using in-memory store", err)
		app.SessStore = session.NewStore()
	} else {
		app.DB = dbStore
		app.SessStore = dbStore
	}
	log.Printf("[iCode] bootstrap: SQLite ready (t=%dms)", time.Since(t0).Milliseconds())

	// 3. Initialize provider registry
	app.Reg = registry.NewRegistry()
	app.registerProviders(cfg)
	log.Printf("[iCode] bootstrap: providers registered (t=%dms)", time.Since(t0).Milliseconds())

	// 4. Initialize permission gate with the configured security level.
	//    NewGate defaults to SecLocal; we must apply the user's configured
	//    level so that API keys they have set actually work.
	// Default mode is Auto: read-only tools auto-approved, mutating ones ask.
	// Matches Claude Code / Reasonix "Edit automatically" default.
	app.Gate = permission.NewGate(permission.ModeAuto)
	if cfg.SecurityLevel == config.SecLocal && hasExternalKeys(cfg) {
		cfg.SecurityLevel = config.SecForeignLLM
		_ = cfg.Save(config.DefaultPath())
	}
	app.Gate.SetSecurityLevel(cfg.SecurityLevel)
	// Apply the configured permission mode from settings
	if cfg.Defaults.Mode != "" {
		app.Gate.SetMode(permission.Mode(cfg.Defaults.Mode))
	}

	// 5. Initialize conversation engine (with permission gate wired in)
	app.Engine = conversation.NewEngine(app.Reg, app.SessStore, app.Gate)
	app.Engine.SetGenerationParams(cfg.Defaults.Temperature, cfg.Defaults.MaxTokens)
	app.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
	app.Engine.SetFallbackModels(cfg.Defaults.FallbackModels)
	log.Printf("[iCode] bootstrap: engine ready (t=%dms)", time.Since(t0).Milliseconds())

	// 5b. Wire smart model router (simple → cheap, complex → powerful)
	defaultModel := cfg.Defaults.Model
	if defaultModel == "" {
		defaultModel = "deepseek-chat"
	}
	defaultProv := cfg.Defaults.Provider
	if defaultProv == "" {
		defaultProv = "deepseek"
	}
	modelRouter := router.New(router.Config{
		DefaultModel:  defaultModel,
		DefaultProv:   defaultProv,
		CheapModel:    cfg.Defaults.CheapModel,
		CheapProv:     cfg.Defaults.CheapProv,
		PowerfulModel: cfg.Defaults.PowerfulModel,
		PowerfulProv:  cfg.Defaults.PowerfulProv,
	})
	// Local semantic routing (config routing.mode: "embedding", now the
	// DEFAULT) — a fully offline, zero-token classifier that refines the
	// keyword baseline. It only upgrades the result when confident, falling
	// back to keyword otherwise, so it can never make routing worse. An empty
	// mode (legacy configs) also maps to embedding.
	if cfg.Routing.Mode == "embedding" || cfg.Routing.Mode == "" {
		modelRouter.SetEmbeddingClassifier(router.NewSemanticClassifier().Classify)
	}
	// Optional LLM-graded routing (config routing.mode: "llm") — uses a cheap
	// model to classify query complexity; falls back to keyword heuristics on
	// any error, so it can never break the conversation.
	if cfg.Routing.Mode == "llm" {
		clsModel := cfg.Routing.ClassifierModel
		if clsModel == "" {
			clsModel = cfg.Defaults.CheapModel
		}
		if clsModel == "" {
			clsModel = defaultModel
		}
		if p, mi, err := app.Reg.ResolveModel(clsModel); err == nil {
			modelRouter.SetLLMClassifier(func(ctx context.Context, query string) (router.Complexity, error) {
				msg, err := p.Chat(ctx, types.ChatRequest{
					Messages: []types.Message{{
						Role:    types.RoleUser,
						Content: "Classify the coding-assistant query below as exactly one word: simple, normal, or complex.\nsimple = quick Q&A, no code changes; normal = standard coding task; complex = multi-step / large refactor / deep analysis.\nAnswer with the single word only.\n\nQuery:\n" + query,
					}},
					Model:        mi.ID,
					ProviderName: mi.Provider,
					MaxTokens:    8,
				})
				if err != nil {
					return router.ComplexityNormal, err
				}
				switch {
				case strings.Contains(strings.ToLower(msg.Content), "simple"):
					return router.ComplexitySimple, nil
				case strings.Contains(strings.ToLower(msg.Content), "complex"):
					return router.ComplexityComplex, nil
				default:
					return router.ComplexityNormal, nil
				}
			})
		}
	}
	app.Engine.SetRouter(modelRouter)

	// Wire sub-agent runner into the tool registry (Claude Code task tool parity)
	app.Engine.WireTaskRunner()

	// 5c. Load skills (SKILL.md) and inject them into the engine system prompt
	// so the model can discover and follow them on demand (Claude Code parity).
	app.Engine.SetSkillsRegistry(skills.Load(skills.DefaultDirs()...))
	// Wire the on-demand skill loader so the use_skill tool can fetch a skill's
	// full body without it ever bloating the immutable (cached) system prefix.
	app.Engine.SetSkillLoader(app.Engine.SkillBody)

	// 5d. Load multi-agent teams (built-in + user/project .icode/teams/*.yaml)
	// and register them so the task tool can dispatch `team:<name>`.
	for _, td := range agent.DefaultTeamDefs() {
		app.Engine.RegisterTeam(td)
	}
	for _, td := range agent.LoadTeams(agent.TeamDefaultDirs()...) {
		app.Engine.RegisterTeam(td)
	}

	// 5e. Activate LSP code intelligence. The engine lazily starts the matching
	// language server on first file edit and injects compile errors as hints.
	if cfg.LSP.Enabled {
		if cwd, err := os.Getwd(); err == nil {
			app.LSPManager = lsp.NewManager(cwd)
			app.Engine.SetLSPManager(app.LSPManager)
			// Eagerly start user-pinned language servers in the BACKGROUND with a
			// per-server timeout. A server that starts but never answers the
			// initialize handshake must never block the boot path — doing this
			// synchronously froze desktop startup before the window could open
			// (the classic "桌面启动卡死", same class as the MCP boot blocker).
			if len(cfg.LSP.AutoStart) > 0 {
				go func(langs []string) {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[iCode LSP] auto-start panic: %v", r)
						}
					}()
					for _, lang := range langs {
						lctx, lcancel := context.WithTimeout(context.Background(), 10*time.Second)
						if err := app.LSPManager.StartLanguageServer(lctx, lang); err != nil {
							log.Printf("[iCode LSP] auto-start %s skipped: %v", lang, err)
						}
						lcancel()
					}
				}(cfg.LSP.AutoStart)
			}
		}
	}

	// 5f. Lifecycle hooks (PreToolUse/PostToolUse/Stop) — Claude Code parity.
	if len(cfg.Hooks) > 0 {
		rules := make(map[string][]hooks.Rule, len(cfg.Hooks))
		for ev, list := range cfg.Hooks {
			for _, hr := range list {
				rules[ev] = append(rules[ev], hooks.Rule{Matcher: hr.Matcher, Command: hr.Command, Timeout: hr.Timeout})
			}
		}
		wd, _ := os.Getwd()
		app.Engine.SetHooksRunner(hooks.NewRunner(rules, wd))
	}

	// 5g. Multimodal generation backend (image_gen/video_gen). Injected even
	// when unset so the tools can report a friendly "not configured" hint.
	app.Engine.SetMultimodalOptions(tool.MultimodalOptions{
		ImageBaseURL: cfg.Multimodal.ImageBaseURL,
		ImageModel:   cfg.Multimodal.ImageModel,
		VideoBaseURL: cfg.Multimodal.VideoBaseURL,
		VideoModel:   cfg.Multimodal.VideoModel,
		APIKey:       cfg.Multimodal.APIKey,
		OutputDir:    cfg.Multimodal.OutputDir,
	})
	log.Printf("[iCode] bootstrap: skills/teams/hooks/LSP/multimodal done (t=%dms)", time.Since(t0).Milliseconds())

	// 6. Initialize undo system (file-level snapshot /undo)
	if err := checkpoint.InitUndo(""); err != nil {
		log.Printf("[iCode] Undo init skipped: %v", err)
	}

	// 7. Initialize model update service
	home, _ := os.UserHomeDir()
	cacheDir := filepath.Join(home, ".icode", "cache")
	app.Updater = modelupdate.NewService(cacheDir)
	for _, name := range app.Reg.List() {
		p, _ := app.Reg.Get(name)
		app.Updater.Register(p)
	}
	log.Printf("[iCode] bootstrap: updater ready (t=%dms)", time.Since(t0).Milliseconds())

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
		{"nvidia", func(k, b string) types.Provider { return nvidia.New(k, b) }},
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

// hasExternalKeys returns true if any non-local provider has an API key set.
func hasExternalKeys(cfg *config.Config) bool {
	localProviders := map[string]bool{"ollama": true, "llama": true, "local": true, "lmstudio": true}
	for name, pc := range cfg.Providers {
		if pc.APIKey != "" && !localProviders[name] {
			return true
		}
	}
	return false
}

// Close shuts down all subsystems gracefully.
func (app *App) Close() error {
	tool.KillAllBgTasks()
	if app.LSPManager != nil {
		app.LSPManager.CloseAll()
	}
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
