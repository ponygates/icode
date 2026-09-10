// Package agent — sub-agent runner with independent Optimizer isolation.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/types"
)

// Runner executes sub-agents in isolated Optimizer contexts. Each sub-agent
// call spins up a fresh Optimizer with its own system prompt, message log,
// and tool subset — nothing flows into the main conversation's context.
type Runner struct {
	providerReg types.ProviderRegistry
	toolReg     *tool.Registry
	gate        *permission.Gate
	mu          sync.Mutex

	sessionID string

	// projectDir anchors project/local memory scopes; set by the host.
	projectDir string

	// params resolves per-model generation overrides. Installed by the host
	// (the conversation engine forwards its own resolver); nil = none.
	params ParamsResolver
}

// ParamsResolver mirrors conversation.ModelParamsResolver. It returns the
// user's per-model generation overrides; a nil temperature means "not set"
// (keep the sub-agent default) while a non-nil pointer, even to 0, wins.
type ParamsResolver func(provider, modelID string) (temperature *float64, topP float64, maxOutput int, ok bool)

func NewRunner(reg types.ProviderRegistry, tr *tool.Registry) *Runner {
	return &Runner{
		providerReg: reg,
		toolReg:     tr,
	}
}

func NewRunnerWithGate(reg types.ProviderRegistry, tr *tool.Registry, gate *permission.Gate) *Runner {
	return &Runner{
		providerReg: reg,
		toolReg:     tr,
		gate:        gate,
	}
}

// SetProjectDir records the workspace root used to anchor project-scoped
// agent memory directories.
func (r *Runner) SetProjectDir(dir string) {
	r.mu.Lock()
	r.projectDir = dir
	r.mu.Unlock()
}

// projectDirSafe returns the recorded project dir (cwd when unset).
func (r *Runner) projectDirSafe() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.projectDir != "" {
		return r.projectDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// SetSessionID records the current session ID so it is injected into the
// context for every tool call the sub-agent makes. Called by the Engine
// before dispatching a Task tool invocation.
func (r *Runner) SetSessionID(sessionID string) {
	r.mu.Lock()
	r.sessionID = sessionID
	r.mu.Unlock()
}

// SetModelParamsResolver installs the per-model generation override source so
// sub-agents honour the same ⚙️ settings as the main conversation. Passing nil
// leaves sub-agents on their built-in defaults.
//
// Only temperature and top_p are applied. MaxTokens deliberately stays the
// agent definition's budget (AgentDef.MaxTokens): that is a per-agent design
// choice, not a per-model one.
func (r *Runner) SetModelParamsResolver(fn ParamsResolver) {
	r.mu.Lock()
	r.params = fn
	r.mu.Unlock()
}

// HasModelParamsResolver reports whether a per-model resolver has been
// installed. Hosts and tests use it to confirm the wiring actually reached the
// runner.
func (r *Runner) HasModelParamsResolver() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.params != nil
}

// subAgentDefaultTemp is the sampling temperature sub-agents use when the user
// has not pinned a per-model value: keeping it low makes tool-call arguments
// predictable.
const subAgentDefaultTemp = 0.1

// resolveGeneration returns the effective temperature and top_p for one
// sub-agent request. The sub-agent default applies unless the host's per-model
// override pins the value — including an explicit 0 ("精确").
func (r *Runner) resolveGeneration(provider, modelID string) (temperature *float64, topP float64) {
	temperature = types.Temp(subAgentDefaultTemp)

	r.mu.Lock()
	fn := r.params
	r.mu.Unlock()
	if fn == nil {
		return temperature, 0
	}

	t, p, _, ok := fn(provider, modelID)
	if !ok {
		return temperature, 0
	}
	if t != nil {
		temperature = t
	}
	if p > 0 {
		topP = p
	}
	return temperature, topP
}

// Run executes a sub-agent in its own Optimizer with the given agent
// definition and input prompt. Returns the agent's final response text and
// the total tokens used (for cost tracking).
func (r *Runner) Run(ctx context.Context, def *AgentDef, input string) (string, int, error) {
	return r.RunWithPrefix(ctx, def, input, nil)
}

// RunWithPrefix is Run with an optional conversation prefix seeded into the
// sub-agent's context before the user input — the fork mode. Replaying the
// parent's exact message bytes keeps the provider prompt-cache prefix intact,
// so forking costs only the cache-read price instead of re-ingesting history.
func (r *Runner) RunWithPrefix(ctx context.Context, def *AgentDef, input string, prefix []types.Message) (string, int, error) {
	// Live-progress relay: the engine attaches a ToolProgressFunc via
	// tool.WithProgress on the Task tool's context, so we forward the
	// sub-agent's tool calls/rounds to the host UI in real time.
	progress := tool.ProgressFromContext(ctx)
	report := func(format string, a ...any) {
		if progress != nil {
			progress(fmt.Sprintf(format, a...))
		}
	}
	var finalText strings.Builder

	// Resolve model
	provider, modelInfo, err := r.providerReg.ResolveModel(def.Model)
	if err != nil {
		return "", 0, fmt.Errorf("resolve model for sub-agent %q: %w", def.Name, err)
	}

	// Worktree isolation: run the agent against a temporary checkout so its
	// writes never touch the user's repo. No changes → discard silently;
	// changes → append the patch to the output for the caller to apply.
	isolated := strings.EqualFold(def.Isolation, "worktree")
	if isolated {
		wt, wtErr := checkpoint.CreateWorktree(r.projectDirSafe())
		if wtErr == nil {
			defer func() {
				if !wt.HasChanges() {
					_ = wt.Discard()
				}
			}()
			r.mu.Lock()
			oldDir := r.projectDir
			r.projectDir = wt.Dir
			r.mu.Unlock()
			defer func() {
				r.mu.Lock()
				r.projectDir = oldDir
				r.mu.Unlock()
			}()
			// Surface the isolation branch so path-based tools can cd into it.
			input = fmt.Sprintf("[工作目录已隔离至 git worktree：%s（分支 %s）]\n\n%s", wt.Dir, wt.Branch, input)
			defer func() {
				if wt.HasChanges() {
					if diff, derr := wt.Diff(); derr == nil && strings.TrimSpace(diff) != "" {
						finalText.WriteString("\n\n<worktree-patch branch=\"" + wt.Branch + "\">\n" + diff + "\n</worktree-patch>")
					} else {
						_ = wt.Commit("iCode isolated agent run")
						finalText.WriteString("\n\n[worktree 变更已提交至分支 " + wt.Branch + "，可用 git cherry-pick 应用]")
					}
				}
			}()
		} else {
			report("⚠ worktree 隔离创建失败，回退到普通执行：%v\n", wtErr)
		}
	}

	// Per-agent persistent memory: inject the MEMORY.md head plus maintenance
	// instructions so the agent builds institutional knowledge across runs.
	systemPrompt := def.SystemPrompt
	if scope := NormalizeMemoryScope(def.Memory); scope != "" {
		r.mu.Lock()
		pdir := r.projectDir
		r.mu.Unlock()
		if block, merr := memoryBlock(scope.String(), def.Name, pdir); merr == nil {
			systemPrompt += block
		}
	}

	// Build a dedicated Optimizer for this sub-agent run.
	optCfg := tokenopt.DefaultConfig(modelInfo)
	optCfg.SystemPrompt = systemPrompt
	optCfg.ProviderName = modelInfo.Provider
	opt := tokenopt.New(optCfg)

	// Filter tool definitions to the allowed subset.
	toolDefs := r.toolReg.ListDefs()
	if len(def.Tools) > 0 {
		allowed := make(map[string]bool, len(def.Tools))
		for _, t := range def.Tools {
			allowed[strings.ToLower(t)] = true
		}
		filtered := make([]types.ToolDef, 0, len(allowed))
		for _, td := range toolDefs {
			if allowed[td.Name] {
				filtered = append(filtered, td)
			}
		}
		toolDefs = filtered
	}
	opt.SetTools(toolDefs)

	// Fork prefix: replay the parent conversation (same bytes → cache hits).
	for _, m := range prefix {
		if m.Content == "" && len(m.ToolCalls) == 0 {
			continue
		}
		pm := m
		pm.Timestamp = time.Time{} // normalise; timestamps don't affect caching
		opt.AddMessage(pm)
	}

	// One-shot prompt: just add the user message and go.
	opt.AddMessage(types.Message{
		Role:      types.RoleUser,
		Content:   input,
		Timestamp: time.Now(),
	})

	// Agentic loop with its own context and token budget.
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	startTime := time.Now()
	const maxRounds = 12
	depth := def.MaxRounds
	if depth <= 0 || depth > maxRounds {
		depth = maxRounds
	}

	doomCounter := make(map[string]int)
	const doomThreshold = 4

	for round := 0; round < depth; round++ {
		select {
		case <-subCtx.Done():
			return finalText.String(), 0, subCtx.Err()
		default:
		}

		messages := opt.CompactRequest("")
		temperature, topP := r.resolveGeneration(modelInfo.Provider, modelInfo.ID)
		eventCh, err := provider.ChatStream(subCtx, types.ChatRequest{
			SessionID:    r.sessionID,
			Messages:     messages,
			Model:        modelInfo.ID,
			ProviderName: modelInfo.Provider,
			SystemPrompt: opt.BuildPrefix(),
			Tools:        toolDefs,
			MaxTokens:    def.MaxTokens,
			Temperature:  temperature,
			TopP:         topP,
		})
		if err != nil {
			if finalText.Len() == 0 {
				return "", 0, fmt.Errorf("sub-agent %q chat stream: %w", def.Name, err)
			}
			return finalText.String(), 0, nil
		}

		var assistantMsg types.Message
		assistantMsg.Role = types.RoleAssistant
		assistantMsg.Timestamp = time.Now()
		var toolCalls []types.ToolCall

		for event := range eventCh {
			switch event.Type {
			case types.EventText:
				assistantMsg.Content += event.Content

			case types.EventToolUse:
				tc := types.ToolCall{
					ID:        event.ToolCall.ID,
					Name:      event.ToolCall.Name,
					Arguments: event.ToolCall.Arguments,
				}
				toolCalls = append(toolCalls, tc)
				report("🔧 %s 调用工具 %s\n", def.Name, tc.Name)

			case types.EventDone:
				if len(toolCalls) == 0 {
					finalText.WriteString(assistantMsg.Content)
					opt.AddMessage(assistantMsg)
					opt.RecordUsage(event.Meta.Usage, 0, startTime)
					stats := opt.Stats()
					return finalText.String(), stats.TotalTokens, nil
				}

				assistantMsg.ToolCalls = toolCalls
				opt.AddMessage(assistantMsg)

				for i, tc := range toolCalls {
					doomCounter[tc.Name]++
					if doomCounter[tc.Name] >= doomThreshold {
						tc.Result = &types.ToolResult{
							Success: false,
							Error:   fmt.Sprintf("doom loop detected: tool %q called %d times consecutively, stopping", tc.Name, doomCounter[tc.Name]),
						}
						toolCalls[i] = tc
					} else {
						tc.Result = r.executeTool(subCtx, tc)
						toolCalls[i] = tc
					}
					if tc.Result != nil {
						if tc.Result.Success {
							report("✓ %s 工具 %s 完成\n", def.Name, tc.Name)
						} else {
							report("⚠ %s 工具 %s 失败\n", def.Name, tc.Name)
						}
						opt.AddMessage(types.Message{
							Role:      types.RoleTool,
							Content:   tc.Result.Content,
							ToolID:    tc.ID,
							Timestamp: time.Now(),
						})
					}
				}
				toolCalls = nil

			case types.EventError:
				if finalText.Len() == 0 {
					return "", 0, fmt.Errorf("sub-agent %q error: %s", def.Name, event.Content)
				}
				return finalText.String(), 0, nil
			}
		}

		allDoomed := true
		for _, c := range doomCounter {
			if c < doomThreshold {
				allDoomed = false
				break
			}
		}
		if allDoomed && len(doomCounter) > 0 {
			finalText.WriteString(fmt.Sprintf("\n[sub-agent %q stopped: doom loop detected]", def.Name))
			break
		}
	}

	return finalText.String(), 0, nil
}

// executeTool runs a single tool call for the sub-agent.
func (r *Runner) executeTool(ctx context.Context, tc types.ToolCall) *types.ToolResult {
	if r.gate != nil {
		action := permission.Action{
			Tool:      tc.Name,
			Arguments: tc.Arguments,
		}
		if cmd := extractCmdFromArgs(tc.Arguments); cmd != "" {
			action.Command = cmd
		}
		res := r.gate.Check(r.sessionID, action)
		switch res.Decision {
		case permission.DecisionDeny:
			return &types.ToolResult{Success: false, Error: permission.HumanizeDeny(action, res.Reason)}
		case permission.DecisionAsk:
			return &types.ToolResult{Success: false, Error: "Sub-agent tool calls require auto-approve or YOLO mode"}
		}
	}
	toolCtx := tool.WithSessionID(ctx, r.sessionID)
	res, err := r.toolReg.Execute(toolCtx, tc.Name, tc.Arguments)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}
	}
	return res
}

func extractCmdFromArgs(args string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	if raw, ok := m["command"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return ""
}
