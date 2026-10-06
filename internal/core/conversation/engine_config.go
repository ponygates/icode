package conversation

import (
	"context"
	"fmt"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/knowledge"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/prefmem"
	"github.com/ponygates/icode/internal/core/router"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/lsp"
	"github.com/ponygates/icode/internal/types"
)

func (e *Engine) SetPermissionHandler(fn PermissionHandler) {
	e.permHandler = fn
}

// SetPreferenceMemory replaces the engine's preference memory Store, e.g.
// with one that has been Restore()d from disk. Pass nil to disable memory.
func (e *Engine) SetPreferenceMemory(s *prefmem.Store) {
	if s == nil {
		s = prefmem.New(prefmem.Options{})
	}
	e.prefMem = s
}

// SetPreferenceSavePath enables debounced auto-persistence of preference
// memory to path. Called after SetPreferenceMemory so a crash mid-session
// does not lose learned preferences.
func (e *Engine) SetPreferenceSavePath(path string) {
	e.prefSaveMu.Lock()
	defer e.prefSaveMu.Unlock()
	e.prefSavePath = path
}

// schedulePrefSave debounces preference persistence: at most one timer is
// armed, and it fires `delay` after the most recent learn so rapid turns
// coalesce into a single disk write.
func (e *Engine) schedulePrefSave(delay time.Duration) {
	if e.prefSavePath == "" || e.prefMem == nil {
		return
	}
	e.prefSaveMu.Lock()
	if e.prefSaveTimer != nil {
		e.prefSaveTimer.Stop()
	}
	e.prefSaveTimer = time.AfterFunc(delay, func() {
		if e.prefSavePath != "" && e.prefMem != nil {
			discard("save preference memory", e.prefMem.SaveFile(e.prefSavePath))
		}
	})
	e.prefSaveMu.Unlock()
}

// FlushPreferenceSave performs an immediate synchronous save, cancelling any
// pending debounce timer. Called on shutdown paths (App.Close) and available
// for tests.
func (e *Engine) FlushPreferenceSave() {
	e.prefSaveMu.Lock()
	if e.prefSaveTimer != nil {
		e.prefSaveTimer.Stop()
		e.prefSaveTimer = nil
	}
	path := e.prefSavePath
	e.prefSaveMu.Unlock()
	if path != "" && e.prefMem != nil {
		discard("flush preference memory", e.prefMem.SaveFile(path))
	}
}

// PreferenceMemory exposes the engine's preference memory Store so the caller
// can persist (Snapshot) or clear (Purge) it independently of the session.
func (e *Engine) PreferenceMemory() *prefmem.Store {
	return e.prefMem
}

// DefaultAutoCompactPct is the default auto-compact threshold: 85% of the
// active model's ContextWindow (Claude Code / Codex CLI parity — competitors
// compact at a token threshold instead of waiting for a manual /compact).
const DefaultAutoCompactPct = 85

// autoCompactMinTurnGapDelta is the anti-thrash guard: a session must see at
// least this many new user turns between two engine-level auto-compactions.
// Without it a session whose kept-recent window alone still exceeds the
// threshold would re-summarize on every single turn for zero benefit.
const autoCompactMinTurnGapDelta = 3

// SetAutoCompactPct configures the auto-compact threshold in percent of the
// model ContextWindow (1-100). 0 (or any out-of-range value) disables
// automatic compaction — the manual /compact prompt path stays available.
// Wired from config tools.auto_compact_pct.
func (e *Engine) SetAutoCompactPct(pct int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if pct < 0 || pct > 100 {
		pct = 0
	}
	e.autoCompactPct = pct
}

// maybeAutoCompact is the threshold-triggered automatic compaction (Claude
// Code parity). It runs at the START of a user turn — before the new user
// message is appended to the optimizer — so the in-flight turn is never
// folded into the summary. It drives the existing compaction machinery
// (tokenopt Optimizer.AutoCompact, the same pipeline /compact and
// CompactRequest use) and never writes a second summariser. The immutable
// cache prefix (system prompt + tool defs + cached summary) is untouched by
// compactLocked. Returns tokens saved and the threshold percent in effect
// (0 = nothing happened).
func (e *Engine) maybeAutoCompact(sessionID string, sess *types.Session, opt *tokenopt.Optimizer) (int, int) {
	e.mu.Lock()
	pct := e.autoCompactPct
	e.mu.Unlock()
	if pct <= 0 || pct > 100 || opt == nil {
		return 0, 0
	}
	opt.SetCompactThreshold(float64(pct) / 100.0)

	userTurns := 0
	if sess != nil {
		for _, m := range sess.Messages {
			if m.Role == types.RoleUser {
				userTurns++
			}
		}
	}
	e.autoCompactMu.Lock()
	last, seen := e.autoCompactTurn[sessionID]
	if seen && userTurns-last < autoCompactMinTurnGapDelta {
		e.autoCompactMu.Unlock()
		return 0, 0 // anti-thrash: too soon after the previous auto-compaction
	}
	e.autoCompactTurn[sessionID] = userTurns
	e.autoCompactMu.Unlock()

	saved, did := opt.AutoCompact()
	if !did {
		e.autoCompactMu.Lock()
		delete(e.autoCompactTurn, sessionID) // don't burn the gap for a no-op
		e.autoCompactMu.Unlock()
		return 0, 0
	}
	return saved, pct
}

// SetMaxToolRounds sets the agent tool-iteration cap per turn (0 = default).
func (e *Engine) SetMaxToolRounds(n int) {
	e.mu.Lock()
	e.maxToolRounds = n
	e.mu.Unlock()
}

// SetHumanizeLLMPolish toggles the extra LLM call that rewrites denial
// reasons into friendlier text (default off — local templates only).
func (e *Engine) SetHumanizeLLMPolish(on bool) {
	e.mu.Lock()
	e.humanizeLLMPolish = on
	e.mu.Unlock()
}

// CircuitBreakerStatus returns a snapshot of every session's circuit breaker
// for the UI layer (diagnostics panel, /status, server API).
func (e *Engine) CircuitBreakerStatus() []CircuitStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	var all []CircuitStatus
	for _, dl := range e.doomLoops {
		all = append(all, dl.CircuitStatus()...)
	}
	return all
}

// doomLoopFor returns the per-session detector, creating it on first use.
func (e *Engine) doomLoopFor(sessionID string) *DoomLoopDetector {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.doomLoops == nil {
		e.doomLoops = make(map[string]*DoomLoopDetector)
	}
	dl := e.doomLoops[sessionID]
	if dl == nil {
		dl = NewDoomLoopDetector()
		e.doomLoops[sessionID] = dl
	}
	return dl
}

// bumpStopHookBlocks increments and returns the per-session count of
// consecutive Stop-hook blocks. The 3-cap stops a broken hook from looping
// the agent forever (Claude Code parity).
func (e *Engine) bumpStopHookBlocks(sessionID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopHookBlocks == nil {
		e.stopHookBlocks = make(map[string]int)
	}
	e.stopHookBlocks[sessionID]++
	return e.stopHookBlocks[sessionID]
}

// resetStopHookBlocks clears the consecutive-block counter — called when a
// turn ends naturally or a fresh user message starts a new turn.
func (e *Engine) resetStopHookBlocks(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.stopHookBlocks, sessionID)
}

// learnPreferences scans a user message for explicit preference statements
// and records them in memory (book: prefer a repeated, explicit statement;
// ignore everything else, especially code).
func (e *Engine) learnPreferences(content string) {
	if e.prefMem == nil {
		return
	}
	learned := false
	for _, pref := range prefmem.Extract(content) {
		e.prefMem.Remember(pref)
		learned = true
	}
	if learned {
		// Debounced persistence: crash-safe without spamming disk per turn.
		e.schedulePrefSave(2 * time.Second)
	}
}

func (e *Engine) SetPermissionResponse(requestID string, decision permission.Decision) {
	e.permMu.Lock()
	ch, ok := e.permRespChans[requestID]
	delete(e.permRespChans, requestID)
	e.permMu.Unlock()
	if ok {
		select {
		case ch <- decision:
		default:
		}
	}
}

func (e *Engine) SetGenerationParams(temperature float64, maxTokens int) {
	e.temperature = temperature
	e.maxTokens = maxTokens
}

// ModelParamsResolver reports the user-configured generation overrides for one
// model. temperature is nil when unset, so an explicit 0 (a legitimate
// "deterministic" choice) stays distinguishable; topP is 0 when unset (top_p 0
// is not a meaningful sampling setting); maxOutput is 0 when unset. ok=false
// means the model has no overrides at all.
//
// The Engine deliberately does not import the config package: the host passes
// (*config.Config).ModelGeneration here, which binds to the live config
// pointer, so later edits are picked up without re-wiring.
type ModelParamsResolver func(provider, modelID string) (temperature *float64, topP float64, maxOutput int, ok bool)

// SetModelParamsResolver installs the per-model override source. Passing nil
// disables overrides and the global defaults apply to every model.
func (e *Engine) SetModelParamsResolver(fn ModelParamsResolver) {
	e.modelParams = fn
}

// applyModelParams resolves the effective generation parameters for one model:
// the user's per-model override where set, otherwise the engine-wide defaults.
//
// Resolved in one place so the three request sites (main turn, tool-JSON
// repair, fallback model) can never drift apart.
func (e *Engine) applyModelParams(mi types.ModelInfo) (temperature *float64, topP float64, maxOut int) {
	// The engine-wide default keeps its historical meaning: 0 = "not set", the
	// provider decides. Only an explicit per-model value can pin it to 0.
	if e.temperature > 0 {
		temperature = types.Temp(e.temperature)
	}
	maxOut = mi.MaxOutputTokens
	if e.modelParams == nil || mi.Provider == "" {
		return temperature, topP, maxOut
	}
	ovTemp, ovTopP, ovMaxOut, ok := e.modelParams(mi.Provider, mi.ID)
	if !ok {
		return temperature, topP, maxOut
	}
	if ovTemp != nil {
		temperature = ovTemp
	}
	if ovTopP > 0 {
		topP = ovTopP
	}
	if ovMaxOut > 0 {
		maxOut = ovMaxOut
	}
	return temperature, topP, maxOut
}

// SetThinking toggles extended thinking for Anthropic-capable models.
// budget <= 0 disables it; otherwise every ChatStream request carries a
// thinking block with the given budget.
func (e *Engine) SetThinking(budget int) {
	if budget <= 0 {
		e.thinking = nil
		return
	}
	e.thinking = &types.ThinkingConfig{BudgetTokens: budget}
}

// thinkingConfig returns the active thinking config (nil when disabled).
func (e *Engine) thinkingConfig() *types.ThinkingConfig {
	return e.thinking
}

// SetCacheTTL sets the ephemeral cache breakpoint TTL for the main
// conversation (Anthropic cache_control ttl, Claude Code promptCacheTtl
// parity). Empty restores the provider default.
func (e *Engine) SetCacheTTL(ttl string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cacheTTL = ttl
}

// SetModelPricing installs per-model contracted prices (Claude Code
// modelPricing parity) so /cost reflects negotiated rates.
func (e *Engine) SetModelPricing(p map[string]config.PricingOverride) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p == nil {
		e.pricingOverrides = nil
		return
	}
	e.pricingOverrides = make(map[string]config.PricingOverride, len(p))
	for k, v := range p {
		e.pricingOverrides[k] = v
	}
}

// ThinkingBudget returns the active extended-thinking budget in tokens, or 0
// when extended thinking is disabled.
func (e *Engine) ThinkingBudget() int {
	if e.thinking == nil {
		return 0
	}
	return e.thinking.BudgetTokens
}

// SetSystemPrompt configures a user-defined system prompt override.
// When set, this replaces the hardcoded base system prompt.
// Pass an empty string to restore the default.
func (e *Engine) SetSystemPrompt(prompt string) {
	e.systemPrompt = prompt
}

// SetFallbackModels configures model IDs to try if the primary model fails.
// Each entry should be a valid model ID (e.g. "deepseek-v4-flash").
func (e *Engine) SetFallbackModels(models []string) {
	e.fallbackModels = models
}

// SetRouter enables smart model routing. When set, the engine will
// automatically select the most cost-effective model based on query
// complexity instead of always using the session's configured model.
func (e *Engine) SetRouter(r *router.Router) {
	e.modelRouter = r
}

// SetLSPManager attaches an LSP manager for code intelligence. When set,
// the engine checks for diagnostics after tool execution and injects
// auto-fix hints to the model on compilation errors.
func (e *Engine) SetLSPManager(m *lsp.Manager) {
	e.lspManager = m
}

// LSPManager returns the attached LSP manager (nil when LSP is disabled), so
// slash commands (/lsp) can query diagnostics/symbols/definitions on demand.
func (e *Engine) LSPManager() *lsp.Manager {
	return e.lspManager
}

// SetKnowledgeManager attaches a document knowledge base (RAG) so the
// /kb slash command and search_knowledge tool can query it.
func (e *Engine) SetKnowledgeManager(m *knowledge.Manager) {
	e.knowledge = m
}

// KnowledgeManager returns the attached knowledge base (nil when unconfigured).
func (e *Engine) KnowledgeManager() *knowledge.Manager {
	return e.knowledge
}

// RegisterTeam registers a multi-agent team definition. Once registered,
// the team can be dispatched via the task tool just like a single agent.
// The team name is prefixed with "team:" to distinguish it from single agents.
func (e *Engine) RegisterTeam(def *agent.TeamDef) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.teamRegistry == nil {
		e.teamRegistry = make(map[string]*agent.TeamDef)
	}
	e.teamRegistry[def.Name] = def
}

// SetSkillsRegistry attaches a skill registry (SKILL.md loader). When set,
// the engine injects the available skill definitions into the system prompt
// so the model can discover and follow them. Call from app bootstrap.
func (e *Engine) SetSkillsRegistry(r *skills.Registry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skillReg = r
}

// SetHooksRunner attaches a lifecycle hooks runner (PreToolUse/PostToolUse/
// Stop). Call from app bootstrap after loading config.
func (e *Engine) SetHooksRunner(r *hooks.Runner) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hooksRunner = r
}

// FireHook emits a lifecycle hook on behalf of a non-engine layer (e.g.
// /clear marking a session deleted → SessionEnd). No-ops when no runner is
// attached or the event has no registered rules.
func (e *Engine) FireHook(ev hooks.Event, in hooks.Input) {
	if e == nil {
		return
	}
	if hr := e.getHooksRunner(); hr.HasHooks(ev) {
		hr.Fire(context.Background(), ev, in)
	}
}

// SetSkillLoader wires the on-demand skill resolver into the use_skill tool so
// the model can fetch a SKILL.md body without it ever entering the cached
// system prefix. Call from app bootstrap after SetSkillsRegistry.
func (e *Engine) SetSkillLoader(fn tool.SkillLoader) {
	e.toolReg.SetSkillsLoader(fn)
}

// SkillBody resolves a skill name to its full body + description for the
// use_skill tool. Returns ok=false when the skill is unknown.
func (e *Engine) SkillBody(name string) (body, description string, ok bool) {
	e.mu.Lock()
	reg := e.skillReg
	e.mu.Unlock()
	if reg == nil {
		return "", "", false
	}
	s, found := reg.Get(name)
	if !found {
		return "", "", false
	}
	return s.Body, s.Description, true
}

// SetMultimodalOptions injects the image_gen / video_gen backend config.
// Call from app bootstrap after loading config.
func (e *Engine) SetMultimodalOptions(opts tool.MultimodalOptions) {
	e.toolReg.SetMultimodalOptions(opts)
}

func (e *Engine) getHooksRunner() *hooks.Runner {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hooksRunner
}

// ListSkills returns the skills currently registered with the engine
// (empty slice when no registry is attached). Used by the TUI /api surface.
func (e *Engine) ListSkills() []skills.Skill {
	e.mu.Lock()
	reg := e.skillReg
	e.mu.Unlock()
	if reg == nil {
		return nil
	}
	return reg.List()
}

// EnableSkill turns a skill's user-managed on/off switch on and persists it.
func (e *Engine) EnableSkill(name string) error {
	e.mu.Lock()
	reg := e.skillReg
	e.mu.Unlock()
	if reg == nil {
		return fmt.Errorf("no skill registry attached")
	}
	return reg.SetEnabled(name, true)
}

// DisableSkill turns a skill's user-managed on/off switch off and persists it.
func (e *Engine) DisableSkill(name string) error {
	e.mu.Lock()
	reg := e.skillReg
	e.mu.Unlock()
	if reg == nil {
		return fmt.Errorf("no skill registry attached")
	}
	return reg.SetEnabled(name, false)
}

// ListTeams returns the multi-agent teams registered with the engine.
func (e *Engine) ListTeams() []agent.TeamDef {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]agent.TeamDef, 0, len(e.teamRegistry))
	for _, def := range e.teamRegistry {
		out = append(out, *def)
	}
	return out
}

func (e *Engine) RegisterTool(t types.Tool) {
	e.toolReg.Register(t)
}

func (e *Engine) UnregisterTool(name string) {
	e.toolReg.Unregister(name)
}

// WireTaskRunner injects the engine as the sub-agent runner into the
// tool registry so the Task tool can delegate to sub-agents.
func (e *Engine) WireTaskRunner() {
	e.toolReg.SetTaskRunner(e)
}

// WireMessageStore injects the persistence layer into the cross-session
// messaging tools (send_message / inbox / list_agents). The store must also
// implement the messaging surface; a plain SessionStore is tolerated with
// messaging disabled (tools report "unavailable") to keep optional deps soft.
func (e *Engine) WireMessageStore(store MessageStore) {
	e.toolReg.SetMessageStore(store)
}

// MessageStore is the messaging slice of the persistence layer, kept here so
// the engine does not import db directly.
type MessageStore interface {
	SendAgentMessage(fromID, toID, body string) error
	AgentInbox(sessionID string, limit int, unreadOnly bool) ([]types.AgentMessage, error)
	MarkAgentMessagesRead(sessionID string) error
	ListSessions(limit, offset int) ([]types.Session, error)
}
