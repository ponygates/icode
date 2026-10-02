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
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/auth"
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/knowledge"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/prefmem"
	"github.com/ponygates/icode/internal/core/router"
	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/db"
	"github.com/ponygates/icode/internal/llm/provider"
	"github.com/ponygates/icode/internal/lsp"
	"github.com/ponygates/icode/internal/mcp"
	"github.com/ponygates/icode/internal/mesh"
	"github.com/ponygates/icode/internal/notify"
	"github.com/ponygates/icode/internal/scheduler"
	"github.com/ponygates/icode/internal/types"
	"github.com/ponygates/icode/internal/xgo"
	"github.com/ponygates/icode/pkg/modelupdate"
)

// bootstrapQuiet silences the chatty bootstrap progress logs. Interactive
// commands (chat, server, desktop) keep them on stderr where they help
// diagnose startup hangs; one-shot diagnostics (doctor, version, print mode)
// mute them so their output stays clean and parseable.
var bootstrapQuiet bool

// SetBootstrapQuiet toggles bootstrap progress logging. Call before
// Bootstrap(). Warnings and errors are always printed regardless.
func SetBootstrapQuiet(quiet bool) { bootstrapQuiet = quiet }

// blog logs a bootstrap progress line unless muted.
func blog(format string, args ...any) {
	if bootstrapQuiet {
		return
	}
	log.Printf(format, args...)
}

// App is the top-level application container.
type App struct {
	Cfg        *config.Config
	Reg        *registry.Impl
	SessStore  types.SessionStore
	DB         *db.Store // SQLite-backed store (may be nil)
	Engine     *conversation.Engine
	Gate       *permission.Gate
	Updater    *modelupdate.Service
	LSPManager *lsp.Manager // nil when LSP disabled
	// MeshCancel stops the cross-machine message forwarder (nil when the
	// in-memory store is in use).
	MeshCancel context.CancelFunc
	// Knowledge is the local document knowledge base (RAG), nil when no
	// knowledge.dirs are configured.
	Knowledge *knowledge.Manager
	// Scheduler runs WorkBuddy-style scheduled automations (may be nil when
	// there is no persistence backend).
	Scheduler *scheduler.Scheduler
	// MCPPool is the shared MCP (Model Context Protocol) client pool wired
	// into the engine at bootstrap, so MCP tools are available in EVERY
	// surface — TUI, exec/print, server, desktop — not just the HTTP server
	// path (the old gap: /mcp-configured servers that `icode chat` could
	// never see). Nil when the engine is unavailable.
	MCPPool *mcp.Pool
	// mcpToolNames tracks the MCP tool names currently registered into the
	// engine so a refresh can unregister the stale set first.
	mcpToolNames map[string]bool
	mcpMu        sync.Mutex

	// BootTimings records CUMULATIVE milliseconds since Bootstrap() entry at
	// each stage boundary, so `icode doctor` can surface a startup benchmark
	// (Claude Code /doctor startup profile parity). Empty on pre-built Apps.
	BootTimings []BootTiming
}

// BootTiming is one startup-stage checkpoint.
type BootTiming struct {
	Stage  string
	Millis int64 // cumulative ms since bootstrap entry
}

// BootStageDeltas converts cumulative timings into per-stage deltas sorted
// slowest-first (the doctor startup profile view).
func (a *App) BootStageDeltas() []BootTiming {
	var out []BootTiming
	prev := int64(0)
	for _, bt := range a.BootTimings {
		out = append(out, BootTiming{Stage: bt.Stage, Millis: bt.Millis - prev})
		prev = bt.Millis
	}
	// Slowest first so the doctor report leads with the real cost.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Millis > out[j-1].Millis; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// BootTotalMillis returns the cumulative ms of the LAST recorded stage —
// i.e. total bootstrap wall time.
func (a *App) BootTotalMillis() int64 {
	if len(a.BootTimings) == 0 {
		return 0
	}
	return a.BootTimings[len(a.BootTimings)-1].Millis
}

// Bootstrap initializes all subsystems and returns a ready-to-use App.
func Bootstrap() (*App, error) {
	app := &App{}
	t0 := time.Now()
	blog("[iCode] bootstrap: entering (config.Load)")
	// mark records a cumulative startup checkpoint for the doctor benchmark
	// (startup profile, Claude Code /doctor parity).
	mark := func(stage string) {
		app.BootTimings = append(app.BootTimings, BootTiming{Stage: stage, Millis: time.Since(t0).Milliseconds()})
	}

	// 1. Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Printf("[iCode] Config load warning: %v; using defaults", err)
		cfg = config.Default()
	}
	app.Cfg = cfg
	mark("config")
	blog("[iCode] bootstrap: config loaded (t=%dms)", time.Since(t0).Milliseconds())

	// 1b. Adopt the persisted working directory (/cd), so the CLI/TUI/server
	// start in the directory the user last moved to instead of the process
	// launch directory. Best-effort: ignore missing / unreadable dirs.
	if cfg.Defaults.WorkingDir != "" {
		if info, err := os.Stat(cfg.Defaults.WorkingDir); err == nil && info.IsDir() {
			if err := os.Chdir(cfg.Defaults.WorkingDir); err == nil {
				blog("[iCode] bootstrap: cwd -> %s", cfg.Defaults.WorkingDir)
			}
		}
	}

	// 2. Try SQLite persistence first, fall back to in-memory
	dbStore, err := db.New(db.Config{})
	if err != nil {
		log.Printf("[iCode] SQLite init failed: %v; using in-memory store", err)
		app.SessStore = session.NewStore()
	} else {
		app.DB = dbStore
		app.SessStore = dbStore
		// Mesh forwarder: push remote-bound messages ("<peer>/<session>")
		// to peer machines every 3s. No peers configured → drain is a no-op.
		if msgStore, ok := interface{}(dbStore).(mesh.MessageStore); ok {
			fwd := &mesh.Forwarder{Store: msgStore}
			ctx, cancel := context.WithCancel(context.Background())
			xgo.GoSafe("mesh.forwarder", func() { fwd.Run(ctx, 3*time.Second) })
			app.MeshCancel = cancel
		}
	}
	mark("storage")
	blog("[iCode] bootstrap: SQLite ready (t=%dms)", time.Since(t0).Milliseconds())

	// 3. Initialize provider registry
	app.Reg = registry.NewRegistry()
	app.registerProviders(cfg)
	mark("providers")
	blog("[iCode] bootstrap: providers registered (t=%dms)", time.Since(t0).Milliseconds())

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
	// Strike-counter escalation: N consecutive blocks force manual mode.
	app.Gate.SetStrikeThreshold(cfg.Permission.StrikeThreshold)
	// Parameter-level hard rules (config [permission.rules]): first match
	// wins and overrides every other decision path, so patterns like
	// "Bash(git push:*)" → ask are enforced even in auto/yolo modes.
	if len(cfg.Permission.Rules) > 0 {
		rules := make([]permission.ParamRule, 0, len(cfg.Permission.Rules))
		for _, r := range cfg.Permission.Rules {
			d := permission.Decision(strings.ToLower(strings.TrimSpace(r.Decision)))
			if d != permission.DecisionAllow && d != permission.DecisionDeny && d != permission.DecisionAsk {
				continue
			}
			rules = append(rules, permission.ParamRule{Pattern: r.Pattern, Decision: d})
		}
		app.Gate.SetParamRules(rules)
	}
	// Claude Code settings.json permission compatibility: rules from
	// ~/.claude/settings.json + .claude/settings.json take effect in Agent
	// mode just like iCode's own hooks.yaml rules.
	wd, _ := os.Getwd()
	app.Gate.SetClaudeSettings(permission.LoadClaudeSettings(wd))

	// 5. Initialize conversation engine (with permission gate wired in)
	app.Engine = conversation.NewEngine(app.Reg, app.SessStore, app.Gate)
	// Engine knobs from config (audit 2026-08-31). NOTE: must stay AFTER
	// NewEngine — a nil Engine here panics every entrypoint.
	if cfg.Tools.MaxToolRounds > 0 {
		app.Engine.SetMaxToolRounds(cfg.Tools.MaxToolRounds)
	}
	// Auto-compact threshold (percent of the model context window; unset =
	// engine default 85, 0 = disable → manual /compact only).
	if cfg.Tools.AutoCompactPct != nil {
		app.Engine.SetAutoCompactPct(*cfg.Tools.AutoCompactPct)
	}
	app.Engine.SetHumanizeLLMPolish(cfg.Permission.HumanizeLLMPolish)
	// Auto-mode classifier (Claude Code parity): a cheap model judges
	// Write/Execute/Connect calls in auto mode so safe ones auto-approve.
	app.Engine.SetClassifierModel(cfg.Permission.ClassifierModel)
	// Cross-session messaging: wire SQLite into send_message / inbox /
	// list_agents so sessions can address each other (SendMessage parity).
	if msgStore, ok := interface{}(dbStore).(conversation.MessageStore); ok {
		app.Engine.WireMessageStore(msgStore)
	}
	app.Engine.SetGenerationParams(cfg.Defaults.Temperature, cfg.Defaults.MaxTokens)
	// Per-model generation overrides (temperature / top_p / max output) layered
	// on top of the globals above. Bound as a method value so later config
	// edits apply without re-wiring.
	app.Engine.SetModelParamsResolver(app.Cfg.ModelGeneration)
	// Extended thinking (Anthropic): config thinking_tokens > 0 enables it.
	if cfg.Defaults.ThinkingTokens > 0 {
		app.Engine.SetThinking(cfg.Defaults.ThinkingTokens)
	}
	// Prompt cache TTL (Anthropic cache_control ttl, Claude Code parity):
	// main conversation uses prompt_cache_ttl, subagents stay at their own TTL.
	if cfg.Defaults.PromptCacheTTL != "" {
		app.Engine.SetCacheTTL(cfg.Defaults.PromptCacheTTL)
	}
	// Per-model contracted prices (Claude Code modelPricing parity).
	if len(cfg.Defaults.ModelPricing) > 0 {
		app.Engine.SetModelPricing(cfg.Defaults.ModelPricing)
	}
	app.Engine.SetSystemPrompt(config.EffectiveSystemPrompt(cfg))
	app.Engine.SetFallbackModels(cfg.Defaults.FallbackModels)
	// Load remembered user preferences from disk (persisted on Close) so they
	// survive restarts. Empty on first run. Auto-save is enabled so a crash
	// mid-session does not lose newly-learned preferences.
	app.Engine.SetPreferenceMemory(prefmem.LoadFile(prefmem.DefaultPath()))
	app.Engine.SetPreferenceSavePath(prefmem.DefaultPath())
	mark("engine")
	blog("[iCode] bootstrap: engine ready (t=%dms)", time.Since(t0).Milliseconds())

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
					Model:        mi.WireModel(),
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
			} else {
				// Auto-detect the project's languages from manifest files and
				// eagerly start their LSPs (OpenCode parity — no manual
				// auto_start needed for go/ts/rust/python/java projects).
				if langs := lsp.DetectProjectLanguages(cfg.Defaults.WorkingDir); len(langs) > 0 {
					log.Printf("[iCode LSP] auto-detected project languages: %v", langs)
					go func(langs []string) {
						for _, lang := range langs {
							lctx, lcancel := context.WithTimeout(context.Background(), 10*time.Second)
							if err := app.LSPManager.StartLanguageServer(lctx, lang); err != nil {
								log.Printf("[iCode LSP] auto-start %s skipped: %v", lang, err)
							}
							lcancel()
						}
					}(langs)
				}
			}
		}
	}

	// 5e2. Document knowledge base (RAG) — /kb + search_knowledge. Indexing
	// runs in the background so a large docs directory never blocks boot.
	if len(cfg.Knowledge.Dirs) > 0 {
		app.Knowledge = knowledge.New(cfg.Knowledge.Dirs)
		app.Engine.SetKnowledgeManager(app.Knowledge)
		app.Engine.RegisterTool(knowledge.NewTool(app.Knowledge, cfg.Knowledge.TopK))
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[iCode knowledge] index panic: %v", r)
				}
			}()
			n, err := app.Knowledge.Index(context.Background())
			if err != nil {
				log.Printf("[iCode knowledge] index error: %v", err)
			} else {
				log.Printf("[iCode knowledge] indexed %d chunks from %v", n, cfg.Knowledge.Dirs)
			}
		}()
	}

	// 5e3. Notification policy (enable/disable + do-not-disturb window).
	notify.SetPolicy(cfg.Notify.Enabled, cfg.Notify.QuietFrom, cfg.Notify.QuietTo)

	// 5e4. MCP pool — connect configured MCP servers in the background and
	// register their tools into the engine so every surface (TUI/exec/print,
	// not just the HTTP server) can use them (Claude Code parity).
	app.initMCPPool()

	// 5f. Lifecycle hooks (PreToolUse/PostToolUse/UserPromptSubmit/Stop) —
	// Claude Code parity.
	if len(cfg.Hooks) > 0 {
		wd, _ := os.Getwd()
		app.Engine.SetHooksRunner(hooks.NewRunner(hooks.RulesFromConfig(cfg.Hooks), wd))
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

	// 5h. Tavily web-search API key, read from the config system so it works
	// without a shell env var (web_search/tavily engine).
	tool.SetTavilyAPIKey(cfg.APIKey("tavily"))
	mark("skills/teams/hooks")
	blog("[iCode] bootstrap: skills/teams/hooks/LSP/multimodal done (t=%dms)", time.Since(t0).Milliseconds())

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
	mark("updater")
	blog("[iCode] bootstrap: updater ready (t=%dms)", time.Since(t0).Milliseconds())

	// 8. Scheduled automations (WorkBuddy-style). Only active when there is a
	//    persistent store (SQLite); in-memory fallback sessions skip it.
	if dbStore != nil {
		newSess := func() (string, error) {
			sess := &types.Session{
				ID:           fmt.Sprintf("%x", time.Now().UnixNano()),
				ModelID:      cfg.Defaults.Model,
				ProviderName: cfg.Defaults.Provider,
				Title:        "定时任务",
			}
			if sess.ModelID == "" {
				sess.ModelID = "deepseek-chat"
			}
			if sess.ProviderName == "" {
				sess.ProviderName = "deepseek"
			}
			if err := app.SessStore.Create(sess); err != nil {
				return "", err
			}
			return sess.ID, nil
		}
		sch := scheduler.New(dbStore, app.Engine, newSess)
		sch.SetIdleWindow(cfg.Scheduler.IdleStart, cfg.Scheduler.IdleEnd)
		app.Scheduler = sch
		sch.Start(context.Background())
		blog("[iCode] bootstrap: scheduler ready (t=%dms)", time.Since(t0).Milliseconds())
	}
	mark("scheduler")

	return app, nil
}

// registerProviders adds all built-in providers to the registry.
func (app *App) registerProviders(cfg *config.Config) {
	names := []string{
		"deepseek", "zhipu", "kimi", "openrouter", "volcengine", "tencent",
		"huawei", "scnet", "nvidia", "sensenova", "agnes",
	}

	// Built through auth.Provider so every live provider learns whether its
	// key field holds an API key or an OAuth subscription token, and gets the
	// 401 renewal path — the registry is shared by all surfaces.
	for _, name := range names {
		provCfg, ok := cfg.Providers[name]
		if !ok {
			provCfg = config.ProviderCfg{}
		}
		if provCfg.Disabled {
			continue
		}

		if err := app.Reg.Register(auth.Provider(name, provCfg)); err != nil {
			log.Printf("[iCode] Failed to register %s: %v", name, err)
		}
	}

	// Anthropic has its own Provider interface (not OpenAI-compatible)
	anthropicCfg, ok := cfg.Providers["anthropic"]
	if !ok {
		anthropicCfg = config.ProviderCfg{}
	}
	if !anthropicCfg.Disabled {
		if err := app.Reg.Register(auth.Provider("anthropic", anthropicCfg)); err != nil {
			log.Printf("[iCode] Failed to register anthropic: %v", err)
		}
	}

	// Register any user-defined vendors from the config file as generic
	// OpenAI-compatible providers (added via `icode config provider`, the
	// desktop settings UI, or a shared config). These back user-added custom
	// models and give CLI/TUI/simpleUI the same capability the server exposes.
	// Also register any persisted custom models so the engine can resolve them
	// at chat time in every frontend.
	app.registerCustomProviders(cfg)
	app.registerCustomModels(cfg)
}

// registerCustomProviders registers non-built-in vendors from the config file
// (e.g. a user's OpenAI-compatible gateway) as generic providers.
func (app *App) registerCustomProviders(cfg *config.Config) {
	if app.Reg == nil {
		return
	}
	for name, pc := range cfg.Providers {
		if _, err := app.Reg.Get(name); err == nil {
			continue // already registered (built-in or earlier custom)
		}
		if pc.Disabled {
			continue
		}
		if err := app.Reg.Register(auth.Provider(name, pc)); err != nil {
			log.Printf("[iCode] Failed to register custom provider %s: %v", name, err)
		}
	}
}

// registerCustomModels registers user-defined model entries from the config
// file into the live registry so the engine can resolve them at chat time.
func (app *App) registerCustomModels(cfg *config.Config) {
	if app.Reg == nil {
		return
	}
	for _, m := range cfg.Models {
		if m.Custom {
			app.registerCustomModel(m)
		}
	}
}

// registerCustomModel registers a single user-defined model (used both at
// bootstrap and when a custom model is added while running).
func (app *App) registerCustomModel(m config.ModelCfg) {
	if m.ID == "" {
		m.ID = config.ModelKey(m.Provider, m.ModelID)
	}
	info := types.ModelInfo{
		ID:              m.ID,
		Name:            m.Name,
		Provider:        m.Provider,
		ContextWindow:   m.ContextWindow,
		MaxOutputTokens: m.MaxOutput,
	}
	// The wire name: the provider's API expects the bare model id, not the
	// "provider/model_id" registry key (see types.ModelInfo.WireModel).
	if m.ModelID != "" && m.ModelID != m.ID {
		info.APIModelID = m.ModelID
	}
	app.Reg.RegisterCustomModel(info, m.ModelID)
}

// RegisterCustomModel registers a custom model at runtime (used by slash
// commands and the simple UI after persisting the new model to config).
func (app *App) RegisterCustomModel(m config.ModelCfg) {
	app.registerCustomModel(m)
}

// RemoveCustomModel deregisters a custom model at runtime.
func (app *App) RemoveCustomModel(id string) {
	if app.Reg == nil {
		return
	}
	app.Reg.RemoveCustomModel(id)
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
	if app.MeshCancel != nil {
		app.MeshCancel()
	}
	tool.KillAllBgTasks()
	tool.KillAllAgentTasks()
	// Persist remembered preferences so they survive restarts. flushPrefSave
	// cancels any pending debounce timer and writes a final synchronous copy.
	// A failure here is non-fatal — memory is an enhancement, not a requirement.
	if app.Engine != nil {
		app.Engine.FlushPreferenceSave()
	}
	if app.LSPManager != nil {
		app.LSPManager.CloseAll()
	}
	if app.MCPPool != nil {
		app.MCPPool.CloseAll()
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
