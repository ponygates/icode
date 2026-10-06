// Package conversation implements the core conversation loop for iCode.
package conversation

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"math/rand"
	"runtime/debug"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/knowledge"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/prefmem"
	"github.com/ponygates/icode/internal/core/privacy"
	"github.com/ponygates/icode/internal/core/router"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/slashcmd"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/lsp"
	"github.com/ponygates/icode/internal/types"
)

// PermissionHandler resolves an interactive "ask" decision (agent mode) and
// returns the user's final choice.
type PermissionHandler func(sessionID string, req *types.PermissionReq, res permission.CheckResult) permission.Decision

// Engine drives the agentic conversation loop.
type Engine struct {
	providerReg types.ProviderRegistry
	toolReg     *tool.Registry
	sessionSt   types.SessionStore
	gate        *permission.Gate
	permHandler PermissionHandler
	// AskUser is the interactive multiple-choice asker (Claude Code
	// AskUserQuestion parity). Injected by the TUI so ask_user_question tool
	// calls can render options and read the user's choice; nil (headless /
	// desktop HTTP) degrades the tool to an error instead of
	// hanging.
	AskUser tool.AskUserFunc
	// AskUserForm is the multi-question wizard asker (opencode AskQuestion
	// parity). Injected by the TUI; nil degrades ask_user_form gracefully.
	AskUserForm tool.AskUserFormFunc

	mu         sync.Mutex
	optimizers map[string]*tokenopt.Optimizer
	stopFns    map[string]context.CancelFunc

	permMu        sync.Mutex
	permRespChans map[string]chan permission.Decision
	permSeq       uint64

	temperature    float64
	maxTokens      int
	modelParams    ModelParamsResolver // per-model generation overrides (nil = none)
	systemPrompt   string              // user-configured system prompt override
	fallbackModels []string            // model IDs to try if the primary fails

	// Sub-agent runner — dispatches Task tool calls to isolated Optimizer
	// contexts. Created on first use so the tool registry is ready.
	agentRunner    *agent.Runner
	agentRegistry  *agent.Registry
	loadAgentsOnce sync.Once
	// curSessionID tracks the active conversation so fork-mode sub-agents can
	// replay its message prefix and tool permission checks use the same key.
	curSessionID string
	// cuStreaks counts consecutive computer-use input ops per session for the
	// runaway-click guard (reset on any non-CU action or new user turn).
	cuStreaks map[string]int
	// cheapModelID is the configured classifier/aux model ("provider/model")
	// used by low-cost helpers such as GenerateCommitMessage.
	cheapModelID string

	// Doom-loop detector prevents the model from repeating the same tool
	// call more than N consecutive times (OpenCode parity).
	// doomLoops is keyed by sessionID — a process-global detector let
	// concurrent desktop sessions trip each other's breakers.
	doomLoops map[string]*DoomLoopDetector

	// stopHookBlocks counts consecutive Stop-hook blocks per session so a
	// misbehaving hook cannot force an infinite continuation loop (Claude
	// Code parity: max 3 forced continues, then the turn ends anyway).
	// Guarded by e.mu. Reset whenever a turn ends naturally or a new user
	// message starts a fresh turn.
	stopHookBlocks map[string]int

	// maxToolRounds caps agent tool iterations per turn (configurable via
	// tools.max_tool_rounds; 0 = default 25).
	maxToolRounds int
	// humanizeLLMPolish gates the extra denial-rewrite LLM call (default off).
	humanizeLLMPolish bool
	// Budget enforcer (tokenopt Level 4) caps tool-output size per turn so a
	// single huge read/grep/bash never blows the context budget. Activated
	// here so the Cache-First Loop keeps saving tokens even on large repos.
	budgetEnforcer *tokenopt.BudgetEnforcer

	// Smart model router — selects the most cost-effective model based on
	// query complexity. When enabled, simple queries use cheap models and
	// complex tasks use powerful models. Set via SetRouter.
	modelRouter *router.Router

	// LSP manager for code intelligence and diagnostics. When set, the
	// engine checks for compilation errors after tool execution and
	// automatically injects fix hints to the model.
	lspManager *lsp.Manager

	// knowledge is the optional local document knowledge base (RAG) queried
	// by the /kb command and the search_knowledge tool.
	knowledge *knowledge.Manager

	// diagCache remembers the last LSP diagnostics text injected per file so
	// unchanged compile errors are not re-injected on every tool turn
	// (they would bloat the context and alert the model to errors it already
	// saw). Keyed by file path.
	diagCache map[string]string

	// Multi-agent team registry — teams defined via TeamDef are registered
	// here and dispatched by the task tool, just like single agents.
	teamRegistry map[string]*agent.TeamDef

	// Skill registry — SKILL.md files loaded from .icode/skills (user + project).
	// When set, the engine injects available skill definitions into the system
	// prompt so the model can follow them on demand (Claude Code parity).
	skillReg *skills.Registry

	// Lifecycle hooks runner — fires PreToolUse/PostToolUse/Stop external
	// commands (Claude Code parity). PreToolUse hooks can block a tool call.
	hooksRunner *hooks.Runner

	// truncDet detects responses cut off at max_tokens (finish_reason="length"
	// or a clearly mid-sentence stop) so the engine can retry with a bigger
	// output budget and a continuation hint (Claude Code parity).
	truncDet *TruncationDetector

	// prefMem remembers USER PREFERENCES (never code) across turns, injecting
	// them into the system prompt so the model respects how the user likes to
	// work. Stale entries age out automatically (prefmem.TTL). Book-inspired:
	// "remember preferences, never code."
	prefMem *prefmem.Store

	// prefSavePath is where prefMem is persisted. When set, learnPreferences
	// schedules a debounced auto-save so preferences survive a crash even if
	// App.Close() is never reached.
	prefSavePath  string
	prefSaveTimer *time.Timer
	prefSaveMu    sync.Mutex

	// tool repair budget — per-session counter of automatic tool-JSON repairs
	// spent in the current user turn. Reset in Send; caps repairBrokenToolCalls
	// so a model that keeps emitting malformed arguments cannot loop forever.
	repairMu     sync.Mutex
	repairCounts map[string]int

	// thinking, when non-nil, enables provider extended thinking (Anthropic
	// Claude). Set via SetThinking / config thinking_tokens.
	thinking *types.ThinkingConfig
	// cacheTTL overrides the ephemeral cache breakpoint TTL (Anthropic), set
	// via SetCacheTTL from config prompt_cache_ttl.
	cacheTTL string
	// pricingOverrides maps model ID → contracted price (CNY/MTok) used by
	// /cost instead of catalog prices (Claude Code modelPricing parity).
	pricingOverrides map[string]config.PricingOverride

	// compactHinted tracks sessions that already received the one-time
	// long-session /compact nudge (so it never nags on every turn).
	hintMu        sync.Mutex
	compactHinted map[string]bool

	// autoCompactPct is the threshold (percent of the model ContextWindow)
	// at which the engine runs the optimizer's compaction pipeline
	// automatically at the start of each user turn (Claude Code / Codex
	// parity). <= 0 disables auto-compaction. Default DefaultAutoCompactPct.
	autoCompactPct int
	// autoCompactMu guards autoCompactTurn, which records the user-turn
	// count at which each session last auto-compacted — the anti-thrash
	// guard (a session cannot compact again until
	// autoCompactMinTurnGapDelta turns have passed).
	autoCompactMu   sync.Mutex
	autoCompactTurn map[string]int
}

// NewEngine creates a conversation engine.
func NewEngine(
	providerReg types.ProviderRegistry,
	sessionSt types.SessionStore,
	gate *permission.Gate,
) *Engine {
	e := &Engine{
		providerReg:     providerReg,
		toolReg:         tool.NewRegistry(),
		sessionSt:       sessionSt,
		gate:            gate,
		optimizers:      make(map[string]*tokenopt.Optimizer),
		stopFns:         make(map[string]context.CancelFunc),
		diagCache:       make(map[string]string),
		permRespChans:   make(map[string]chan permission.Decision),
		doomLoops:       make(map[string]*DoomLoopDetector),
		stopHookBlocks:  make(map[string]int),
		teamRegistry:    make(map[string]*agent.TeamDef),
		budgetEnforcer:  tokenopt.NewBudgetEnforcer(tokenopt.DefaultBudgetConfig()),
		truncDet:        NewTruncationDetector(DefaultTruncationRecoveryConfig()),
		prefMem:         prefmem.New(prefmem.Options{}),
		repairCounts:    make(map[string]int),
		compactHinted:   make(map[string]bool),
		autoCompactPct:  DefaultAutoCompactPct,
		autoCompactTurn: make(map[string]int),
	}
	if gate != nil {
		slashcmd.SetShellGate(&shellGateAdapter{gate: gate})
	}
	e.toolReg.Register(tool.NewTaskTool(e))
	return e
}

// Send starts a conversation turn and returns a stream of events.
// Security level is checked here to enforce the user's privacy boundary.
// Unlike Claude Code, iCode NEVER sends data externally without the user
// knowing exactly what level is active — shown in the TUI status bar.
func (e *Engine) Send(ctx context.Context, sessionID, content string, attachments ...[]types.Attachment) (<-chan types.StreamEvent, error) {
	// New user input resets THIS session's doom loop detector.
	e.doomLoopFor(sessionID).Reset()
	e.resetToolRepairBudget(sessionID)

	sess, err := e.sessionSt.Get(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	// Track the active session for fork-mode sub-agents and route the ID into
	// the agent runner so sub-agent permission checks key on the same session.
	e.mu.Lock()
	e.curSessionID = sessionID
	if runner := e.getAgentRunner(); runner != nil {
		runner.SetSessionID(sessionID)
	}
	e.mu.Unlock()
	// A fresh user turn signals intent — clear any computer-use runaway
	// streak so the model gets a clean slate of desktop interactions.
	e.resetCUStreak(sessionID)

	// SessionStart lifecycle hook — fires on the very first user message of
	// a session (empty transcript) so external scripts can initialize state.
	if len(sess.Messages) == 0 {
		if hr := e.getHooksRunner(); hr.HasHooks(hooks.SessionStart) {
			hr.Fire(context.Background(), hooks.SessionStart, hooks.Input{SessionID: sessionID})
		}
	}
	// AgentStart lifecycle hook — brackets the main agent turn (the whole
	// user-prompt → response cycle), as opposed to sub-agent runs.
	if hr := e.getHooksRunner(); hr.HasHooks(hooks.AgentStart) {
		hr.Fire(context.Background(), hooks.AgentStart, hooks.Input{
			SessionID: sessionID, Prompt: firstN(content, 200),
		})
	}

	// Stash-and-continue audit: if the user pivots to a (different) task while
	// in-progress work exists in this session, snapshot a checkpoint + refresh
	// the archived summary so the old task remains recoverable. This is
	// best-effort — it must never block the conversation.
	e.stashIfPivot(ctx, sess, content)

	// Smart model routing: only engage when the user has NOT explicitly
	// chosen a model (session model empty or "auto"). An explicit selection
	// made in the UI/config (e.g. openrouter/free) is always respected, so
	// the router can never hijack it with its own default (which may point
	// to a provider that has no API key configured).
	modelID := sess.ModelID
	if e.modelRouter != nil && (sess.ModelID == "" || sess.ModelID == "auto") {
		route := e.modelRouter.RouteQuery(content, len(sess.Messages))
		if route.ModelID != "" {
			modelID = route.ModelID
		}
	}

	provider, modelInfo, err := e.providerReg.ResolveModel(modelID)
	if err != nil {
		// Fallback to the session's configured model
		provider, modelInfo, err = e.providerReg.ResolveModel(sess.ModelID)
		if err != nil {
			return nil, fmt.Errorf("resolve model: %w", err)
		}
	}

	// Security level enforcement: block or sanitize based on the user's
	// configured privacy boundary.
	if e.gate != nil {
		if err := e.gate.CheckProviderAccess(sess.ProviderName); err != nil {
			return nil, err
		}
	}

	// Lite-resume mode: when the session was resumed with /resume --lite, feed
	// the model only the archived summary + the most recent n messages instead
	// of the whole transcript. The summary is injected as a preset prefix (see
	// getOrCreateOptimizer), so the model keeps the gist without the cost of
	// replaying every old turn.
	//
	// Token budget guard (/budget): when a hard budget is set, shrink the
	// context to fit it (summary + recent messages) instead of growing
	// unbounded. The trim is re-computed every turn so it tracks the budget
	// as the conversation grows.
	msgs := sess.Messages
	budget := sessionum.BudgetMax(sess)
	trimmed := false
	var warnMsg string
	if budget > 0 {
		if sessionum.Get(sess) == "" {
			mode := ""
			if e.gate != nil {
				mode = string(e.gate.Mode())
			}
			discard("save session summary", sessionum.Save(e.sessionSt, sess, sessionum.Generate(sess, modelID, sess.ProviderName, mode)))
		}
		msgs, trimmed = sessionum.TrimToBudget(msgs, budget)
		if trimmed {
			discard("record budget trim", sessionum.RecordTrim(e.sessionSt, sess))
		}
		if warn, used, b := sessionum.BudgetWarning(e.sessionSt, sess); warn {
			discard("record budget warning", sessionum.RecordWarn(e.sessionSt, sess))
			warnMsg = fmt.Sprintf("ⓘ [预算护栏] 已用约 %d/%d tokens（%d%%），接近上限，即将自动压缩。", used, b, used*100/b)
		}
	} else if n := sessionum.LiteN(sess); n > 0 && n < len(msgs) {
		msgs = msgs[len(msgs)-n:]
	}

	opt := e.getOrCreateOptimizer(sessionID, modelInfo, msgs, sessionum.Get(sess))
	// A hard /budget trim computed above must be reflected in the cached
	// optimizer's log; getOrCreateOptimizer only seeds a fresh optimizer, so
	// on trimmed turns we replace the log with the trimmed subset to make the
	// budget actually bind for in-progress sessions.
	if trimmed {
		opt.ReplaceMessages(msgs)
	}

	// Long-goal mode: when the session has a goal, re-inject it into the
	// system prompt every turn so the model keeps working toward it. The
	// optimizer's SetSystemPrompt is a no-op when unchanged, keeping the
	// provider cache prefix stable.
	if goal := sessionum.GetGoal(sess); goal != "" {
		prompt := e.buildSystemPrompt(sessionID) + "\n\nCURRENT GOAL (keep working toward this until done):\n" + goal
		// Per-goal token cap (Reasonix goal_token_budget parity): once the
		// session's cumulative tokens exceed the budget, tell the model to
		// wrap up instead of iterating indefinitely.
		if budget := sessionum.GetGoalTokenBudget(sess); budget > 0 && sess.TotalTokens.TotalTokens >= budget {
			prompt += fmt.Sprintf("\n\nTOKEN BUDGET REACHED（目标 token 预算 %d 已用尽）: 请立即收尾——总结已完成的工作、未完成的项与后续建议，然后停止，不要再启动新的改动。", budget)
		}
		if verify := sessionum.GetGoalVerify(sess); verify != "" {
			prompt += "\n\nACCEPTANCE CHECK（验收命令，对标 ZCode Goal 模式）: " + verify +
				"\n每一轮代码/配置改动后，都必须运行上面的验收命令判断目标是否已达成。" +
				"\n若未通过：阅读失败输出，继续修改 → 再运行 → 再判断，如此迭代，直到验收命令通过为止。" +
				"\n达成后：向用户报告验收结果并停止，不要继续无关改动。"
			// Computer-use closed loop (CC research-preview parity): for
			// UI-shaped goals, compile-pass ≠ done — the model must LOOK at
			// the result through screen_read before declaring victory.
			if uiShapedGoal(goal + " " + verify) {
				prompt += "\n\nVISUAL VERIFICATION（UI 类目标附加要求）: 验收命令通过后，还必须调用 screen_read 工具截取当前屏幕并确认界面真实可用（页面渲染正常、无报错弹窗、关键交互可见）。" +
					"\n若需要交互验证（如登录后才能看到的页面），可用鼠标/键盘工具操作后再截图确认。" +
					"\n只有「验收命令通过 + 屏幕确认无误」两者都满足才算目标达成；截图发现问题则继续修复并重复本流程。"
			}
		}
		opt.SetSystemPrompt(prompt)
	}

	// Redact content if security level is "desensitize"
	sendContent := content
	if e.gate != nil && e.gate.SecurityLevel() == config.SecDesensitize {
		sendContent = privacy.Redact(content)
		// Also redact any past attachments in the session
	}

	// UserPromptSubmit lifecycle hook (Claude Code parity): an external
	// command can rewrite the prompt (stdout {"prompt": "..."}) before the
	// model sees it, or block the message entirely (exit code 2).
	if hr := e.getHooksRunner(); hr.HasHooks(hooks.UserPromptSubmit) {
		hres := hr.Fire(ctx, hooks.UserPromptSubmit, hooks.Input{
			Prompt:    sendContent,
			SessionID: sessionID,
		})
		if hres.Block {
			// The message is dropped — emit a system notice so the UI tells
			// the user why nothing happened.
			out := make(chan types.StreamEvent, 1)
			out <- types.StreamEvent{Type: types.EventSystem, Content: "ⓘ 用户消息已被 UserPromptSubmit 钩子拦截: " + firstN(hres.Message, 200)}
			close(out)
			return out, nil
		}
		if hres.Prompt != "" && hres.Prompt != sendContent {
			sendContent = hres.Prompt
		}
	}

	userMsg := types.Message{
		Role:      types.RoleUser,
		Content:   sendContent,
		Timestamp: time.Now(),
	}
	if len(attachments) > 0 && len(attachments[0]) > 0 {
		userMsg.Attachments = attachments[0]
	}
	// Auto-compact (threshold-triggered, Claude Code / Codex parity): run the
	// existing compaction pipeline BEFORE this turn's user message enters the
	// optimizer, so the in-flight turn is never folded into the summary. The
	// engine-side CompactRequest below would implicitly compact anyway —
	// driving it here just adds a visible event and the anti-thrash guard.
	autoSaved, autoPct := e.maybeAutoCompact(sessionID, sess, opt)
	userMsg = e.appendPersisted(sessionID, opt, userMsg)
	// A fresh user message starts a new turn: the Stop-hook block counter
	// applies per turn, so previous blocks must not leak into this one.
	e.resetStopHookBlocks(sessionID)

	// Preference memory: learn from what the user just said (only explicit
	// preference statements — never code). The block is injected into the
	// system prompt on subsequent turns.
	e.learnPreferences(sendContent)

	messages := opt.CompactRequest("")

	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.stopFns[sessionID] = cancel
	e.mu.Unlock()

	startTime := time.Now()

	// Full resilience chain (primary + fallbacks + rate-limit retry) lives in
	// the shared helper so the tool continuation rounds get the same
	// protection — a mid-task 429 fails over instead of killing the turn.
	eventCh, fallbackMsg, err := e.chatStreamWithFallback(ctx, sessionID, messages, modelID, provider, modelInfo, opt)
	if err != nil {
		cancel()
		return nil, err
	}

	out := make(chan types.StreamEvent, 64)
	if fallbackMsg != "" {
		out <- types.StreamEvent{Type: types.EventSystem, Content: fallbackMsg}
	}
	if autoSaved > 0 {
		// Visible notification so the TUI / desktop can show what happened
		// (auto-compaction is otherwise silent history rewriting).
		out <- types.StreamEvent{Type: types.EventSystem, Content: fmt.Sprintf(
			"ⓘ [自动压缩] 上下文达到阈值（%d%% × 模型窗口），已自动压缩较早对话：auto-compacted %d tokens。",
			autoPct, autoSaved)}
	} else if trimmed {
		out <- types.StreamEvent{Type: types.EventSystem, Content: "ⓘ [预算护栏] 会话上下文超出预算，已自动压缩为摘要 + 最近消息（≤ 预算）。"}
	} else if warnMsg != "" {
		out <- types.StreamEvent{Type: types.EventSystem, Content: warnMsg}
	} else if hint := e.longSessionHint(sess); hint != "" {
		out <- types.StreamEvent{Type: types.EventSystem, Content: hint}
	}
	go func() {
		defer close(out)
		defer cancel()
		// Remove the per-session cancel func once this turn ends (Stop or
		// natural completion) so long-running desktop sessions don't leak one
		// entry per message. Delete is idempotent with Stop's own delete.
		defer func() {
			e.mu.Lock()
			delete(e.stopFns, sessionID)
			e.mu.Unlock()
		}()
		// A panic in streaming/tool execution must never kill the whole CLI
		// process (the classic silent "flash close" / 闪退). Recover here, log
		// the stack, and surface a user-visible error event so the session
		// survives.
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "[engine] stream goroutine panic: %v\n%s\n", r, debug.Stack())
				select {
				case out <- types.StreamEvent{Type: types.EventError, Content: fmt.Sprintf("引擎内部错误（已自动恢复）: %v", r)}:
				default:
				}
			}
		}()

		var assistantMsg types.Message
		assistantMsg.Role = types.RoleAssistant
		assistantMsg.Timestamp = time.Now()
		var toolCalls []types.ToolCall
		// Some providers emit the FULL text so far on every chunk instead of
		// an incremental delta. Feed everything through a textAccumulator so
		// only the delta is forwarded — without this the first turn prints
		// duplicated text ("OKOK") while later turns (continueAgentLoop,
		// which already used an accumulator) don't.
		acc := &textAccumulator{}

		for {
			select {
			case <-ctx.Done():
				// User interrupted (Esc / stop button): keep whatever partial
				// output already streamed (Claude Code parity — "已完成的工作保留"),
				// persist it into the session, and tell the UI explicitly so it
				// can reset its streaming state and show a confirmation.
				e.persistPartialTurn(sessionID, opt, assistantMsg)
				msg := "⏹ 已中断生成。"
				if assistantMsg.Content != "" {
					msg += " 已生成的部分输出已保留。"
				}
				select {
				case out <- types.StreamEvent{Type: types.EventSystem, Content: msg}:
				default:
				}
				return
			case event, ok := <-eventCh:
				if !ok {
					return
				}
				switch event.Type {
				case types.EventText:
					full, delta := acc.feed(event.Content)
					assistantMsg.Content = full
					if delta == "" {
						// Nothing new (duplicate snapshot) — don't emit.
						continue
					}
					out <- types.StreamEvent{Type: types.EventText, Content: delta}
				case types.EventThinking:
					// Pass through thinking events to the UI for display
					out <- event
				case types.EventToolUse:
					tc := types.ToolCall{
						ID:        event.ToolCall.ID,
						Name:      event.ToolCall.Name,
						Arguments: event.ToolCall.Arguments,
					}
					toolCalls = append(toolCalls, tc)
					out <- event
					// NOTE: execution deferred to EventDone so the whole turn's
					// tool calls can run through executeToolBatch (read-only tools
					// in parallel — Claude Code parallel tool use parity).
				case types.EventDone:
					if len(toolCalls) > 0 {
						e.runToolTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, toolCalls, out, 0)
					} else {
						e.finishTextTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, event, startTime, out, 0)
					}
					// Stop lifecycle hook (Claude Code parity): exit 2 (or JSON
					// {"decision":"block"} / {"continue":false}) blocks the stop
					// and injects the reason as a continuation message so the
					// agent keeps working — e.g. "tests must pass before you
					// stop". Capped at 3 consecutive blocks per session so a
					// broken hook cannot loop the agent forever.
					if hr := e.getHooksRunner(); hr.HasHooks(hooks.Stop) {
						sres := hr.Fire(context.Background(), hooks.Stop, hooks.Input{SessionID: sessionID})
						if sres.Block {
							if n := e.bumpStopHookBlocks(sessionID); n > 3 {
								select {
								case out <- types.StreamEvent{Type: types.EventSystem, Content: "⚠ Stop 钩子已连续阻断 3 次仍要求继续，已强制结束本轮（防止无限循环）。"}:
								default:
								}
							} else {
								select {
								case out <- types.StreamEvent{Type: types.EventSystem, Content: "⇄ Stop 钩子要求继续工作: " + firstN(sres.Message, 200)}:
								default:
								}
								// Inject the block reason as a user-role
								// continuation so the next round has context.
								contMsg := types.Message{
									Role: types.RoleUser,
									Content: "[stop-hook] " + firstN(sres.Message, 500) +
										"\n请根据以上反馈继续完成任务，不要重复已完成的工作。",
									Timestamp: time.Now(),
								}
								contMsg = e.appendPersisted(sessionID, opt, contMsg)
								// Reset per-round state and re-open the stream.
								assistantMsg = types.Message{Role: types.RoleAssistant, Timestamp: time.Now()}
								toolCalls = nil
								acc = &textAccumulator{}
								messages := opt.CompactRequest("")
								var fbMsg string
								eventCh, fbMsg, err = e.chatStreamWithFallback(ctx, sessionID, messages, modelID, provider, modelInfo, opt)
								if err != nil {
									out <- types.StreamEvent{Type: types.EventError, Content: "Stop 钩子续跑失败: " + err.Error()}
									return
								}
								if fbMsg != "" {
									select {
									case out <- types.StreamEvent{Type: types.EventSystem, Content: fbMsg}:
									default:
									}
								}
								continue
							}
						} else {
							e.resetStopHookBlocks(sessionID)
						}
					}
					// Notification hook — completion signal for external
					// scripts (Claude Code parity).
					if hr := e.getHooksRunner(); hr.HasHooks(hooks.Notification) {
						hr.Fire(context.Background(), hooks.Notification, hooks.Input{
							SessionID: sessionID, ToolOutput: "completed",
						})
					}
					// AgentStop — the main agent turn is done (counterpart to
					// AgentStart fired at the top of Send).
					if hr := e.getHooksRunner(); hr.HasHooks(hooks.AgentStop) {
						hr.Fire(context.Background(), hooks.AgentStop, hooks.Input{
							SessionID:  sessionID,
							ToolOutput: fmt.Sprintf("%dms", time.Since(startTime).Milliseconds()),
						})
					}
					// NOTE: no second EventDone here — finishTextTurn (and the
					// continueAgentLoop chain it anchors) already emitted the
					// round's single EventDone with the model stamped on it.
					return
				case types.EventError:
					// Mid-stream failure (network drop, provider reset,
					// quota hit mid-turn): keep whatever partial output
					// the user already saw — the same guarantee as the
					// user-interrupt path above. Without this, a dropped
					// connection silently eats everything streamed before
					// the failure, and the next turn has the model redo
					// (and re-bill) work it already did.
					e.persistPartialTurn(sessionID, opt, assistantMsg)
					if assistantMsg.Content != "" {
						event.Content += "\n（已生成的部分输出已保留，输入「继续」可从断点接着生成。）"
					}
					// Notification hook — failure signal.
					if hr := e.getHooksRunner(); hr.HasHooks(hooks.Notification) {
						hr.Fire(context.Background(), hooks.Notification, hooks.Input{
							SessionID: sessionID, ToolOutput: "error: " + event.Content,
						})
					}
					out <- event
					return
				}
			}
		}
	}()
	return out, nil
}

// newMessageID returns a collision-resistant message ID. A nanosecond stamp
// alone can collide when two messages are appended in the same tick, so a
// random suffix is mixed in.
func newMessageID() string {
	return fmt.Sprintf("msg-%x-%04x", time.Now().UnixNano(), rand.Uint32()&0xffff)
}

// appendPersisted adds msg to the optimizer AND persists it to the session
// store, returning the message with its ID filled in.
//
// Root cause fixed here: engine-built messages historically carried an empty
// ID, and the SQLite store has PRIMARY KEY(id) — so only the FIRST empty-ID
// insert ever succeeded ("UNIQUE constraint failed" for every later one),
// while the error was silently discarded. Result: conversations never reached
// ~/.icode/icode.db, which surfaced as "CLI and desktop don't share history".
// Empty IDs are now generated here, and persistence errors are logged instead
// of swallowed so this failure mode can never hide again.
func (e *Engine) appendPersisted(sessionID string, opt *tokenopt.Optimizer, msg types.Message) types.Message {
	if msg.ID == "" {
		msg.ID = newMessageID()
	}
	if opt != nil {
		opt.AddMessage(msg)
	}
	if e.sessionSt != nil {
		if err := e.sessionSt.AppendMessage(sessionID, msg); err != nil {
			log.Printf("[engine] warning: persist message %q to session %s failed: %v", msg.ID, sessionID, err)
		}
	}
	return msg
}

// persistPartialTurn saves an interrupted turn's partial assistant output
// into the optimizer and the session store so the user keeps what they
// already saw on screen (Claude Code parity: "已完成的工作保留").
func (e *Engine) persistPartialTurn(sessionID string, opt *tokenopt.Optimizer, assistantMsg types.Message) {
	if assistantMsg.Content == "" {
		return
	}
	assistantMsg = e.appendPersisted(sessionID, opt, assistantMsg)
}

// chatStreamWithFallback opens the model stream with the full resilience
// chain: primary model + configured fallback models, each with rate-limit /
// quota retry (2 backoff attempts, Claude Code "continue automatically at
// usage limit" parity). Used by BOTH the first round (Send) and every tool
// continuation round (continueAgentLoop) — previously the continuation used a
// bare ChatStream call, so a mid-task 429 aborted the whole turn.
// Returns the event channel and, when a fallback model took over, a
// user-facing notice the caller should emit on its event stream.
func (e *Engine) chatStreamWithFallback(
	ctx context.Context,
	sessionID string,
	messages []types.Message,
	modelID string,
	primary types.Provider,
	modelInfo types.ModelInfo,
	opt *tokenopt.Optimizer,
) (<-chan types.StreamEvent, string, error) {
	type modelTry struct {
		modelID      string
		providerName string
		modelInfo    types.ModelInfo
		provider     types.Provider // nil = reuse primary
		isFallback   bool
		// wire is the model string sent on the API wire. For user-defined
		// custom models the registry key (modelID) is "provider/model_id"
		// while the API expects the bare model_id — WireModel() translates.
		wire string
	}
	// The primary entry MUST use the routed modelID: when the smart router
	// engaged (session model ""/auto) it differs from sess.ModelID.
	modelsToTry := []modelTry{
		{modelID: modelID, providerName: modelInfo.Provider, modelInfo: modelInfo, wire: modelInfo.WireModel()},
	}
	for _, fb := range e.fallbackModels {
		if fb == modelID {
			continue
		}
		p, mi, err := e.providerReg.ResolveModel(fb)
		if err == nil {
			modelsToTry = append(modelsToTry, modelTry{
				modelID: fb, providerName: mi.Provider, modelInfo: mi,
				provider: p, isFallback: true, wire: mi.WireModel(),
			})
		}
	}

	var lastErr error
	var fallbackMsg string
	for _, mt := range modelsToTry {
		if mt.isFallback && lastErr != nil {
			// The notice travels through the event stream (a raw-mode TUI owns
			// stdout; desktop can only show it as an event).
			fallbackMsg = fmt.Sprintf("⚠️ 主模型不可用，已切换备用模型 %s（原因：%s）", mt.modelID, friendlyModelError(lastErr))
		}
		p := mt.provider
		if p == nil {
			p = primary
		}
		build := func() (<-chan types.StreamEvent, error) {
			// Resolved per candidate: a fallback model carries its own
			// overrides, so switching models must not inherit the primary's.
			temperature, topP, modelMaxOut := e.applyModelParams(mt.modelInfo)
			return p.ChatStream(ctx, types.ChatRequest{
				SessionID:        sessionID,
				Messages:         messages,
				Model:            mt.wire,
				ProviderName:     mt.providerName,
				SystemPrompt:     opt.BuildPrefix(),
				Tools:            e.toolReg.ListDefs(),
				MaxTokens:        orMaxTokens(e.maxTokens, modelMaxOut),
				Temperature:      temperature,
				TopP:             topP,
				CacheBreakpoints: opt.BuildCacheBreakpoints(),
				Thinking:         e.thinkingConfig(),
				CacheTTL:         e.cacheTTL,
			})
		}
		eventCh, err := build()
		if err == nil {
			return eventCh, fallbackMsg, nil
		}
		if isRateLimitError(err) {
			for attempt := 1; attempt <= 2 && err != nil; attempt++ {
				// Prefer the provider's own Retry-After hint: the server knows
				// when its window resets, so waiting exactly that long beats
				// guessing — too short re-fails and deepens the limit, too long
				// stalls a turn that was already ready to go. Fall back to
				// exponential backoff with jitter when no hint was given: a
				// fixed schedule makes thundering herds the moment a provider
				// recovers.
				base := time.Duration(15*(1<<(attempt-1))) * time.Second
				if hint := rateLimitRetryAfter(err); hint > 0 {
					base = clampRetryAfter(hint)
				}
				jitter := time.Duration(rand.Int63n(int64(base / 4)))
				select {
				case <-ctx.Done():
					attempt = 99 // abort retries
				case <-time.After(base + jitter):
					eventCh, err = build()
				}
			}
			if err == nil {
				return eventCh, fallbackMsg, nil
			}
		}
		lastErr = err
	}
	if len(modelsToTry) == 1 {
		return nil, "", fmt.Errorf("chat stream: %s", friendlyModelError(lastErr))
	}
	return nil, "", fmt.Errorf("all models failed, last: %s", friendlyModelError(lastErr))
}

func (e *Engine) continueAgentLoop(
	ctx context.Context,
	sessionID string,
	provider types.Provider,
	opt *tokenopt.Optimizer,
	modelInfo types.ModelInfo,
	out chan types.StreamEvent,
	depth int,
) {
	maxRounds := e.maxToolRounds
	if maxRounds <= 0 {
		maxRounds = 25
	}
	if depth >= maxRounds {
		out <- types.StreamEvent{
			Type:    types.EventText,
			Content: fmt.Sprintf("\n[已连续执行 %d 轮工具调用，主动停止。输入 \"继续\" 可从当前进度接着做。]\n", maxRounds),
		}
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}

	messages := opt.CompactRequest("")
	startTime := time.Now()
	// Same resilience chain as the first round: rate-limit retry + fallback
	// models + the user's CacheTTL (which the old bare ChatStream call dropped
	// after round one, silently breaking prompt_cache_ttl mid-turn).
	eventCh, fallbackMsg, err := e.chatStreamWithFallback(ctx, sessionID, messages, modelInfo.ID, provider, modelInfo, opt)
	if fallbackMsg != "" {
		out <- types.StreamEvent{Type: types.EventSystem, Content: fallbackMsg}
	}
	if err != nil {
		out <- types.StreamEvent{Type: types.EventError, Content: friendlyModelError(err)}
		return
	}

	var assistantMsg types.Message
	assistantMsg.Role = types.RoleAssistant
	assistantMsg.Timestamp = time.Now()
	var toolCalls []types.ToolCall
	acc := &textAccumulator{}

	for {
		select {
		case <-ctx.Done():
			// User interrupted (Esc / stop) during a continuation round —
			// keep the partial output of this round the same way the first
			// stream does, so nothing the user already saw is silently lost.
			e.persistPartialTurn(sessionID, opt, assistantMsg)
			msg := "⏹ 已中断生成。"
			if assistantMsg.Content != "" {
				msg += " 已生成的部分输出已保留。"
			}
			select {
			case out <- types.StreamEvent{Type: types.EventSystem, Content: msg}:
			default:
			}
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			switch event.Type {
			case types.EventText:
				full, delta := acc.feed(event.Content)
				assistantMsg.Content = full
				if delta != "" {
					out <- types.StreamEvent{Type: types.EventText, Content: delta}
				}
			case types.EventThinking:
				// Pass through thinking events to the UI for display
				out <- event
			case types.EventToolUse:
				tc := types.ToolCall{
					ID:        event.ToolCall.ID,
					Name:      event.ToolCall.Name,
					Arguments: event.ToolCall.Arguments,
				}
				toolCalls = append(toolCalls, tc)
				out <- event
				// NOTE: tool execution deferred — see parallel execution below
			case types.EventDone:
				if len(toolCalls) > 0 {
					// Tool-JSON auto-resend (Claude Code parity): a turn cut off
					// at max_tokens often leaves the last tool call's arguments
					// JSON incomplete, but a model bug can also emit malformed
					// JSON with finish_reason="stop". Either way, repair the
					// broken call with a continuation call before executing —
					// bounded by the per-turn repair budget (anti-loop).
					if ctx.Err() == nil {
						if e.repairBrokenToolCalls(ctx, sessionID, provider, opt, modelInfo, toolCalls, event.Meta.FinishReason == "length", out, depth) {
							return
						}
					}
					e.runToolTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, toolCalls, out, depth)
				} else {
					e.finishTextTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, event, startTime, out, depth)
				}
				return
			case types.EventError:
				// Mid-stream failure in a continuation round: same partial-
				// output guarantee as the first round — keep what streamed.
				e.persistPartialTurn(sessionID, opt, assistantMsg)
				if assistantMsg.Content != "" {
					event.Content += "\n（已生成的部分输出已保留，输入「继续」可从断点接着生成。）"
				}
				out <- event
				return
			}
		}
	}
}

// SessionStats returns token and cache statistics for a session.
func (e *Engine) SessionStats(sessionID string) *tokenopt.Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	opt, ok := e.optimizers[sessionID]
	if !ok {
		return nil
	}
	s := opt.Stats()
	return &s
}

func (e *Engine) Stop(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cancel, ok := e.stopFns[sessionID]; ok {
		cancel()
		delete(e.stopFns, sessionID)
	}
}

func (e *Engine) getOrCreateOptimizer(sessionID string, modelInfo types.ModelInfo, existingMessages []types.Message, presetSummary string) *tokenopt.Optimizer {
	e.mu.Lock()
	defer e.mu.Unlock()
	if opt, ok := e.optimizers[sessionID]; ok {
		return opt
	}
	opt := tokenopt.New(tokenopt.Config{
		ModelInfo:     modelInfo,
		SystemPrompt:  e.buildSystemPrompt(sessionID),
		ProviderName:  modelInfo.Provider,
		PresetSummary: presetSummary,
	})
	opt.SetTools(e.toolReg.ListDefs())

	// Load existing session messages into the optimizer so the LLM has
	// full conversation context. This is essential for session sharing
	// between CLI and desktop — when switching modes, the optimizer is
	// created fresh while the session store already has the history.
	for _, msg := range existingMessages {
		opt.AddMessage(msg)
	}

	e.optimizers[sessionID] = opt
	return opt
}
