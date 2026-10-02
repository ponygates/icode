// Package permission provides the tool execution approval system for iCode.
//
// Three operational modes:
//   - Plan (只读): survey only, no modifications allowed. LLM can read/search but
//     cannot write, execute, or delete. All mutating operations are blocked.
//   - Agent (确认): each tool call requests user approval before execution.
//     Supports single-allow, session-allow, and deny decisions.
//   - YOLO (自动): auto-approve all operations within configured bounds.
//
// Security levels (merged from config.SecurityLevel) add a privacy layer
// on top of the operational mode:
//   - local:        no external API calls at all
//   - desensitize:  PII sanitized before sending to any API
//   - local-llm:    only local models (Ollama, llama.cpp, etc.)
//   - foreign-llm:  international API providers allowed
//   - unrestricted: all providers, no additional restrictions
//
// Unlike Claude Code, iCode NEVER sends telemetry, analytics, or usage data
// to any external service. The security level is always visible in the TUI
// status bar and config panel so the user knows exactly what is happening.
package permission

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ponygates/icode/internal/config"
)

// Mode defines the operational permission level.
type Mode string

const (
	ModePlan  Mode = "plan"
	ModeAgent Mode = "agent"
	ModeAuto  Mode = "auto" // read-only auto-approved, mutating ops ask
	ModeYOLO  Mode = "yolo"
)

// AccessLevel classifies a tool action into Claude Code's four-tier
// permission model (本书 ch.22):
//
//	Read    — low risk, changes nothing (read_file, grep, git_diff…)
//	Write   — mid/high risk, changes files or workspace (edit, write_file…)
//	Execute — mid/high risk, runs commands / changes system state (bash)
//	Connect — reaches external networks / services (fetch, web_search, …)
//
// Read is safe to auto-approve in Auto mode. Write/Execute/Connect are
// treated as mutating-or-external and require confirmation.
type AccessLevel int

const (
	AccessRead    AccessLevel = iota // observe only, no side effects
	AccessWrite                      // mutates local files/workspace
	AccessExecute                    // runs commands, changes system state
	AccessConnect                    // touches external network/services
)

// String returns a short human-readable label for a level.
func (l AccessLevel) String() string {
	switch l {
	case AccessRead:
		return "Read"
	case AccessWrite:
		return "Write"
	case AccessExecute:
		return "Execute"
	case AccessConnect:
		return "Connect"
	default:
		return "?"
	}
}

// Decision represents the outcome of a permission check.
type Decision string

const (
	DecisionAllow    Decision = "allow"
	DecisionDeny     Decision = "deny"
	DecisionAllowAll Decision = "allow_all_session"
	DecisionAsk      Decision = "ask" // UI needs to prompt the user
)

// Action describes what the tool wants to do.
type Action struct {
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`

	// Parsed from arguments for display
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
	Pattern string `json:"pattern,omitempty"`
	URL     string `json:"url,omitempty"`
}

// Gate is the central permission controller.
type Gate struct {
	// mcpPolicy maps MCP tool name → owning server's trust mode. Guarded by
	// its own lock: refreshMCPTools swaps it while turns may be in flight.
	mcpMu     sync.RWMutex
	mcpPolicy map[string]string
	mu        sync.RWMutex
	mode      Mode

	// SecurityLevel controls data handling when communicating with external
	// services. Always visible in the status bar — no hidden telemetry.
	// Default is "local" (safest).
	securityLevel config.SecurityLevel

	// Allowed paths — tools can only read/write within these directories
	AllowedPaths []string

	// Denied commands — shell commands that are always blocked
	DeniedCommands []string

	// Per-session allow-all state
	sessionAllows map[string]bool // sessionID → true if all-tool allow is active

	// Per-session per-tool allow — selective "always allow X for this session"
	sessionToolAllows map[string]map[string]bool // sessionID → toolName → allowed

	// Hooks is a per-tool/per-path allowlist loaded from $ICODE_HOME/hooks.yaml
	hooks *HooksConfig

	// ToolRules are persistent per-tool preferences configured by the user.
	// "allow" → always allow, "deny" → always deny, "" or "ask" → use normal flow.
	// Saved to config.yaml and survives restarts.
	ToolRules map[string]string // toolName → "allow" | "deny" | "ask"

	// classifier, when set, evaluates Write/Execute/Connect calls in auto
	// mode (Claude Code auto-mode classifier parity): a cheap model judges
	// whether the call is safe to auto-approve, sparing the user most
	// prompts. nil = rule-based auto (mutating ops ask).
	classifier Classifier

	// connectDomains is the Connect-tier silent whitelist (本书 ch.22 静默白名单):
	// hosts the user has explicitly approved at least once. In Auto mode a
	// subsequent fetch to a trusted host is granted without re-prompting —
	// the user already confirmed that destination.
	connectDomains map[string]bool // host → trusted

	// claudeSettings holds Claude Code's settings.json permission rules
	// (loaded from ~/.claude/settings.json + project .claude/settings.json).
	// Evaluated in Agent mode alongside hooks.yaml; deny wins over allow.
	claudeSettings *ClaudeSettings

	// strikes counts consecutive non-allow decisions per session (the
	// "分类器兜底" escalation counter). An allow resets it; N consecutive
	// ask/deny decisions (strikeThreshold) force the session into manual mode.
	strikes map[string]int
	// escalated marks sessions that have been forced into manual mode after
	// too many consecutive blocks. Once set, every action in that session
	// requires explicit confirmation (DecisionAsk) regardless of mode.
	escalated map[string]bool
	// strikeThreshold is the consecutive-block count that triggers escalation
	// back to manual mode. 0 disables the feature.
	strikeThreshold int

	// paramRules are parameter-level rules (Tool(payload) patterns) evaluated
	// before any mode logic. First match wins and overrides every other
	// decision path — the "hard deny / hard ask" layer.
	paramRules []ParamRule
}

// ParamRule is a parameter-level permission rule evaluated before any mode
// logic. The pattern uses Claude Code's Tool(payload) syntax — e.g.
// "Bash(git push:*)" or "Edit(**.md)" — matched with the same engine as
// .claude/settings.json patterns. A matched rule overrides every other
// decision path (mode defaults, tool rules, session allow-all), so users can
// hard-deny destructive commands even in YOLO mode.
type ParamRule struct {
	Pattern  string
	Decision Decision // allow | deny | ask
}

// NewGate creates a permission gate with default settings.
// Security level defaults to "local" — no data ever leaves the machine
// without explicit user awareness. Unlike Claude Code, there is zero
// telemetry, zero tracking, and zero "phone-home" baked in.
func NewGate(mode Mode) *Gate {
	return &Gate{
		mode:              mode,
		securityLevel:     config.SecLocal,
		DeniedCommands:    defaultDeniedCommands(),
		sessionAllows:     make(map[string]bool),
		sessionToolAllows: make(map[string]map[string]bool),
		hooks:             loadHooks(),
		ToolRules:         make(map[string]string),
		connectDomains:    make(map[string]bool),
		strikes:           make(map[string]int),
		escalated:         make(map[string]bool),
		strikeThreshold:   3,
	}
}

// SetSecurityLevel updates the privacy boundary at runtime. The level is
// displayed in the TUI status bar so it is never invisible.
func (g *Gate) SetSecurityLevel(level config.SecurityLevel) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.securityLevel = level
}

// SecurityLevel returns the current privacy boundary.
func (g *Gate) SecurityLevel() config.SecurityLevel {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.securityLevel
}

// SecurityLabel returns a human-readable label for the current security level.
func SecurityLabel(level config.SecurityLevel) string {
	switch level {
	case config.SecLocal:
		return "🔒 本地处理"
	case config.SecDesensitize:
		return "🛡 脱敏处理"
	case config.SecLocalLLM:
		return "💻 本地大模型"
	case config.SecForeignLLM:
		return "🌐 国外大模型"
	case config.SecUnrestricted:
		return "⚠ 无限制"
	default:
		return "🔒 本地处理"
	}
}

// SetMode changes the operational mode.
func (g *Gate) SetMode(mode Mode) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Normalise the desktop/CLI "ask" vocabulary onto ModeAgent so every
	// caller (config PUT, /api/permission/mode, slashui) behaves identically.
	if mode == Mode("ask") {
		mode = ModeAgent
	}
	g.mode = mode
}

// Mode returns the current operational mode.
func (g *Gate) Mode() Mode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.mode
}

// SetAllowedPaths replaces the directory allowlist at runtime. An empty slice
// clears the restriction (all paths allowed). Called by the desktop settings
// UI so changes take effect without a restart.
func (g *Gate) SetAllowedPaths(paths []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			if abs, err := filepath.Abs(p); err == nil {
				clean = append(clean, abs)
			} else {
				clean = append(clean, p)
			}
		}
	}
	g.AllowedPaths = clean
}

// SetDeniedCommands replaces the always-blocked command substrings at runtime.
// Called by the desktop settings UI so changes take effect without a restart.
func (g *Gate) SetDeniedCommands(commands []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	clean := make([]string, 0, len(commands))
	for _, c := range commands {
		if c = strings.TrimSpace(c); c != "" {
			clean = append(clean, c)
		}
	}
	g.DeniedCommands = clean
}

// SetSessionAllow records that a session has been granted allow-all.
func (g *Gate) SetSessionAllow(sessionID string, allow bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if allow {
		g.sessionAllows[sessionID] = true
	} else {
		delete(g.sessionAllows, sessionID)
	}
}

// SetSessionToolAllow selectively allows a specific tool for a session.
func (g *Gate) SetSessionToolAllow(sessionID, toolName string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessionToolAllows[sessionID] == nil {
		g.sessionToolAllows[sessionID] = make(map[string]bool)
	}
	g.sessionToolAllows[sessionID][toolName] = true
}

// ClearSession drops all per-session allow state (allow-all + per-tool allows)
// for a session. Called when a session is deleted so long-lived servers don't
// leak one entry per session.
func (g *Gate) ClearSession(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.sessionAllows, sessionID)
	delete(g.sessionToolAllows, sessionID)
}

// SetToolRule sets a persistent rule for a tool: "allow", "deny", or "" to clear.
func (g *Gate) SetToolRule(toolName, rule string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if rule == "" || rule == "ask" {
		delete(g.ToolRules, toolName)
	} else {
		g.ToolRules[toolName] = rule
	}
}

// GetToolRules returns a copy of all persistent tool rules.
func (g *Gate) GetToolRules() map[string]string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make(map[string]string, len(g.ToolRules))
	for k, v := range g.ToolRules {
		out[k] = v
	}
	return out
}

type CheckResult struct {
	Decision Decision
	Reason   string
	Prompt   string // User-facing description of what would be done
	// Escalated is true when this decision forced the session into manual mode
	// (strike counter hit the threshold). The UI shows a "已退回手动" notice.
	Escalated bool
	// Severity rates the risk of an ask decision for approval UIs: "high"
	// (destructive: rm -rf, git push --force) turns the TUI box red, "medium"
	// (regular file writes / unmatched shell commands) keeps the familiar
	// yellow, "low" (read-tier tools that only ask due to escalation or an
	// explicit rule) renders calm cyan. Empty on allow/deny.
	Severity string
}

// Risk severity levels stamped onto ask decisions by Gate.Check.
const (
	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"
)

// Check evaluates a tool action against the current permission mode, then
// applies the strike-counter escalation: consecutive ask/deny decisions count
// up (an allow resets them), and reaching the threshold forces the session
// into manual mode (every later action must be confirmed by the user).
// Classifier evaluates whether a tool call is safe to auto-approve in Auto
// mode (Claude Code's auto-mode classifier parity). Implemented by the engine
// with a cheap model; nil disables classification (rule-based fallback).
type Classifier interface {
	// Classify returns allow=true when the tool call is judged safe. reason
	// explains the verdict for the user-facing decision.
	Classify(ctx context.Context, toolName, toolInput string) (allow bool, reason string, err error)
}

// SetClassifier attaches the auto-mode classifier (see Classifier).
func (g *Gate) SetClassifier(c Classifier) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.classifier = c
}

// SetMCPPolicy installs the tool-name → trust-mode map ("ask"|"readonly"|"all")
// derived from the MCP server configs. Nil clears it.
func (g *Gate) SetMCPPolicy(policy map[string]string) {
	g.mcpMu.Lock()
	g.mcpPolicy = policy
	g.mcpMu.Unlock()
}

// mcpTrustMode returns the trust mode for a tool, "" when unrestricted.
func (g *Gate) mcpTrustMode(tool string) string {
	g.mcpMu.RLock()
	defer g.mcpMu.RUnlock()
	return g.mcpPolicy[tool]
}

// mcpReadVerbTool is the read-only heuristic for "readonly"-trust servers:
// only tool names whose verb suggests a pure read are auto-approvable.
func mcpReadVerbTool(tool string) bool {
	t := strings.ToLower(tool)
	for _, v := range []string{"get", "list", "read", "search", "query", "fetch", "find", "show", "describe", "status", "health", "view", "check"} {
		if strings.Contains(t, v) {
			return true
		}
	}
	return false
}

// recordStrike updates the per-session strike counter based on the decision
// and escalates to manual mode when the threshold is crossed.
func (g *Gate) recordStrike(sessionID string, result *CheckResult) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.strikeThreshold <= 0 {
		return
	}
	switch result.Decision {
	case DecisionAllow, DecisionAllowAll:
		g.strikes[sessionID] = 0
	case DecisionAsk, DecisionDeny:
		g.strikes[sessionID]++
		if g.strikes[sessionID] >= g.strikeThreshold {
			g.escalated[sessionID] = true
			result.Escalated = true
			result.Decision = DecisionAsk
			if result.Reason != "" {
				result.Reason += "；"
			}
			result.Reason += fmt.Sprintf("已连续 %d 次拦截，自动退回手动模式", g.strikes[sessionID])
		}
	}
}

// SetStrikeThreshold sets the consecutive-block count that escalates a session
// to manual mode. 0 disables the feature.
func (g *Gate) SetStrikeThreshold(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.strikeThreshold = n
}

// StrikeThreshold returns the current escalation threshold.
func (g *Gate) StrikeThreshold() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.strikeThreshold
}

// EscalationState reports whether the session has been force-escalated to
// manual mode after consecutive blocks, plus its current strike count
// (surfaced in the UI's permission bar so the user sees the "N 次后退回手动"
// progress, Claude Code parity).
func (g *Gate) EscalationState(sessionID string) (escalated bool, strikes int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.escalated[sessionID], g.strikes[sessionID]
}
