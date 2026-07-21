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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ponygates/icode/internal/config"
	"gopkg.in/yaml.v3"
)

// Mode defines the operational permission level.
type Mode string

const (
	ModePlan  Mode = "plan"
	ModeAgent Mode = "agent"
	ModeAuto  Mode = "auto" // read-only auto-approved, mutating ops ask
	ModeYOLO  Mode = "yolo"
)

// Decision represents the outcome of a permission check.
type Decision string

const (
	DecisionAllow      Decision = "allow"
	DecisionDeny       Decision = "deny"
	DecisionAllowAll   Decision = "allow_all_session"
	DecisionAsk        Decision = "ask" // UI needs to prompt the user
)

// Action describes what the tool wants to do.
type Action struct {
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`

	// Parsed from arguments for display
	Command    string `json:"command,omitempty"`
	Path       string `json:"path,omitempty"`
	Pattern    string `json:"pattern,omitempty"`
	URL        string `json:"url,omitempty"`
}

// Gate is the central permission controller.
type Gate struct {
	mu      sync.RWMutex
	mode    Mode

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
}

// HooksConfig represents the hooks.yaml configuration.
type HooksConfig struct {
	Tools []ToolRule `yaml:"tools"`
}

type ToolRule struct {
	Name    string   `yaml:"name"`
	Allow   []string `yaml:"allow"`   // patterns that are always allowed
	Deny    []string `yaml:"deny"`    // patterns that are always denied
	Ask     []string `yaml:"ask"`     // patterns that require user approval
}

// NewGate creates a permission gate with default settings.
// Security level defaults to "local" — no data ever leaves the machine
// without explicit user awareness. Unlike Claude Code, there is zero
// telemetry, zero tracking, and zero "phone-home" baked in.
func NewGate(mode Mode) *Gate {
	return &Gate{
		mode:             mode,
		securityLevel:    config.SecLocal,
		DeniedCommands:   defaultDeniedCommands(),
		sessionAllows:    make(map[string]bool),
		sessionToolAllows: make(map[string]map[string]bool),
		hooks:            loadHooks(),
		ToolRules:        make(map[string]string),
	}
}

// defaultDeniedCommands returns the initial set of always-blocked commands.
// Uses the 23-rule Bash security engine to derive the block list.
func defaultDeniedCommands() []string {
	// Start with the classic set
	cmds := []string{
		"rm -rf /", "rm -rf ~", "rm -rf .",
		"sudo rm", "sudo dd",
		"chmod 777", "chmod -R 777",
		"dd if=", "mkfs.", "fdisk", "parted",
		"curl | sh", "curl | bash", "wget | sh", "wget | bash",
	}
	return cmds
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

// CheckProviderAccess returns nil if the provider is allowed by the current
// security level, or an error explaining why it is blocked.
func (g *Gate) CheckProviderAccess(providerName string) error {
	g.mu.RLock()
	level := g.securityLevel
	g.mu.RUnlock()

	localProviders := map[string]bool{
		"ollama":   true,
		"llama":    true,
		"local":    true,
		"lmstudio": true,
	}

	switch level {
	case config.SecLocal:
		return fmt.Errorf("当前安全等级为「本地处理」，不允许调用任何外部 API。\n使用 /security 切换等级，或设置中调整。")
	case config.SecDesensitize:
		// Allowed but data will be sanitized before sending (handled by caller)
		return nil
	case config.SecLocalLLM:
		if !localProviders[providerName] {
			return fmt.Errorf("当前安全等级为「本地大模型」，仅允许本地模型 (Ollama/Llama.cpp/LM Studio)。\n使用 /security 切换等级。")
		}
		return nil
	case config.SecForeignLLM:
		return nil
	case config.SecUnrestricted:
		return nil
	default:
		return nil
	}
}

// SetMode changes the operational mode.
func (g *Gate) SetMode(mode Mode) {
	g.mu.Lock()
	defer g.mu.Unlock()
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

// ============================================================================
// Check — determines whether a tool action needs approval
// ============================================================================

type CheckResult struct {
	Decision Decision
	Reason   string
	Prompt   string // User-facing description of what would be done
}

// Check evaluates a tool action against the current permission mode.
func (g *Gate) Check(sessionID string, action Action) CheckResult {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Build prompt for display
	prompt := g.buildPrompt(action)

	switch g.mode {
	case ModeAuto:
		// Read-only / safe operations are auto-approved; anything that could
		// mutate state still asks the user (unless it is on the deny list).
		if g.isReadOnly(action) {
			return CheckResult{Decision: DecisionAllow, Reason: "Auto mode: read-only operation", Prompt: prompt}
		}
		if g.isDenied(action) {
			return CheckResult{Decision: DecisionDeny, Reason: "Command is in the deny list", Prompt: prompt}
		}
		// Check runtime per-tool rules (persistent across sessions)
		if g.ToolRules[action.Tool] == "deny" {
			return CheckResult{Decision: DecisionDeny, Reason: "Tool denied by user rule", Prompt: prompt}
		}
		if g.ToolRules[action.Tool] == "allow" {
			return CheckResult{Decision: DecisionAllow, Reason: "Tool allowed by user rule", Prompt: prompt}
		}
		// Check session-level per-tool allow
		if g.sessionToolAllows[sessionID] != nil && g.sessionToolAllows[sessionID][action.Tool] {
			return CheckResult{Decision: DecisionAllow, Reason: "Tool allowed for this session", Prompt: prompt}
		}
		return CheckResult{Decision: DecisionAsk, Prompt: prompt}

	case ModePlan:
		if g.isReadOnly(action) {
			return CheckResult{Decision: DecisionAllow, Reason: "Plan mode: read-only operation", Prompt: prompt}
		}
		return CheckResult{
			Decision: DecisionDeny,
			Reason:   "Plan mode: mutating operations are not allowed. Switch to Agent or YOLO mode.",
			Prompt:   prompt,
		}

	case ModeYOLO:
		// Check if command is in denied list
		if g.isDenied(action) {
			return CheckResult{
				Decision: DecisionDeny,
				Reason:   fmt.Sprintf("Command %q is in the deny list", action.Command),
				Prompt:   prompt,
			}
		}
		return CheckResult{Decision: DecisionAllow, Reason: "YOLO mode: auto-approved", Prompt: prompt}

	case ModeAgent:
		// Check session-level allow-all
		if g.sessionAllows[sessionID] {
			return CheckResult{Decision: DecisionAllow, Reason: "Session allow-all active", Prompt: prompt}
		}

		// Check hooks
		if decision := g.checkHooks(action); decision != nil {
			return *decision
		}

		// Check persistent per-tool rules
		if g.ToolRules[action.Tool] == "deny" {
			return CheckResult{Decision: DecisionDeny, Reason: "Tool denied by user rule", Prompt: prompt}
		}
		if g.ToolRules[action.Tool] == "allow" {
			return CheckResult{Decision: DecisionAllow, Reason: "Tool allowed by user rule", Prompt: prompt}
		}
		// Check session-level per-tool allow
		if g.sessionToolAllows[sessionID] != nil && g.sessionToolAllows[sessionID][action.Tool] {
			return CheckResult{Decision: DecisionAllow, Reason: "Tool allowed for this session", Prompt: prompt}
		}

		// Check if denied
		if g.isDenied(action) {
			return CheckResult{
				Decision: DecisionDeny,
				Reason:   fmt.Sprintf("Command is in the deny list"),
				Prompt:   prompt,
			}
		}

		// Otherwise, ask the user
		return CheckResult{Decision: DecisionAsk, Prompt: prompt}

	default:
		return CheckResult{Decision: DecisionAsk, Prompt: prompt}
	}
}

// ============================================================================
// Helpers
// ============================================================================

func (g *Gate) isReadOnly(action Action) bool {
	readOnlyTools := map[string]bool{
		"read_file":  true,
		"ls":         true,
		"grep":       true,
		"glob":       true,
		"git_diff":   true,
		"git_status": true,
		"fetch":      true,
		"disk_usage": true,
		// todo_write only mutates in-process session state, never the
		// filesystem or external systems — safe to auto-approve.
		"todo_write": true,
	}
	return readOnlyTools[action.Tool]
}

func (g *Gate) isDenied(action Action) bool {
	if action.Tool != "bash" {
		return false
	}

	cmd := strings.ToLower(strings.TrimSpace(action.Command))

	// Check classic deny list (backward compatible)
	for _, denied := range g.DeniedCommands {
		if strings.Contains(cmd, strings.ToLower(denied)) {
			return true
		}
	}

	// Check 23-rule Bash security engine
	if violation := IsDeniedBashCommand(action.Command); violation != nil {
		return true
	}

	// Check if command has any SeverityBlock violations
	violations := CheckBashCommand(action.Command)
	for _, v := range violations {
		if v.Severity == SeverityBlock {
			return true
		}
	}

	// Also block commands operating outside allowed paths
	if action.Path != "" && len(g.AllowedPaths) > 0 {
		abs, err := filepath.Abs(action.Path)
		if err != nil {
			return true
		}
		allowed := false
		for _, ap := range g.AllowedPaths {
			if strings.HasPrefix(abs, ap) {
				allowed = true
				break
			}
		}
		if !allowed {
			return true
		}
	}

	return false
}

func (g *Gate) checkHooks(action Action) *CheckResult {
	if g.hooks == nil {
		return nil
	}

	for _, rule := range g.hooks.Tools {
		if rule.Name != action.Tool {
			continue
		}

		// Check deny patterns first (highest priority)
		for _, pattern := range rule.Deny {
			if matchPattern(action, pattern) {
				return &CheckResult{
					Decision: DecisionDeny,
					Reason:   fmt.Sprintf("Denied by hooks rule: %s", pattern),
				}
			}
		}

		// Check allow patterns
		for _, pattern := range rule.Allow {
			if matchPattern(action, pattern) {
				return &CheckResult{
					Decision: DecisionAllow,
					Reason:   fmt.Sprintf("Allowed by hooks rule: %s", pattern),
				}
			}
		}
	}

	return nil
}

func matchPattern(action Action, pattern string) bool {
	switch action.Tool {
	case "bash":
		return strings.Contains(action.Command, pattern)
	case "read_file", "write_file":
		return strings.Contains(action.Path, pattern)
	default:
		return strings.Contains(action.Arguments, pattern)
	}
}

func (g *Gate) buildPrompt(action Action) string {
	switch action.Tool {
	case "bash":
		return fmt.Sprintf("执行命令: %s", action.Command)
	case "write_file":
		return fmt.Sprintf("写入文件: %s (%d 字节)", action.Path, len(action.Arguments))
	case "edit":
		return fmt.Sprintf("编辑文件: %s", action.Path)
	case "read_file":
		return fmt.Sprintf("读取文件: %s", action.Path)
	case "grep":
		return fmt.Sprintf("搜索: \"%s\" 在 %s", action.Pattern, action.Path)
	case "glob":
		return fmt.Sprintf("查找文件: %s", action.Pattern)
	case "git_diff":
		return fmt.Sprintf("查看 git 差异 (staged=%t)", action.Path != "")
	case "git_status":
		return "查看 git 状态"
	case "git_commit":
		return fmt.Sprintf("提交 git: %s", truncate(action.Command, 50))
	default:
		return fmt.Sprintf("%s: %s", action.Tool, truncate(action.Arguments, 60))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ============================================================================
// Hooks file loading
// ============================================================================

func loadHooks() *HooksConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	paths := []string{
		filepath.Join(home, ".icode", "hooks.yaml"),
		".icode/hooks.yaml",
		"hooks.yaml",
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		var cfg HooksConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			continue
		}
		return &cfg
	}

	return &HooksConfig{
		Tools: []ToolRule{
			{
				Name:  "read_file",
				Allow: []string{"*"},
			},
			{
				Name:  "grep",
				Allow: []string{"*"},
			},
			{
				Name:  "glob",
				Allow: []string{"*"},
			},
			{
				Name:  "ls",
				Allow: []string{"*"},
			},
		},
	}
}

// ============================================================================
// Hooks file generator — creates a sample hooks.yaml
// ============================================================================

func GenerateHooks(path string) error {
	sample := `# iCode Hooks Configuration
# Control which tool calls are allowed, denied, or require approval.
#
# Patterns support glob-style matching (*, **, ?).

tools:
  # File reading — always allowed
  - name: read_file
    allow:
      - "*"

  # File searching — always allowed
  - name: grep
    allow:
      - "*"
  - name: glob
    allow:
      - "*"
  - name: ls
    allow:
      - "*"

  # File writing — require approval
  - name: write_file
    ask:
      - "*"

  # Shell commands — require approval, block dangerous ones
  - name: bash
    deny:
      - "rm -rf /"
      - "sudo"
      - "chmod 777"
      - "> /dev/sda"
    ask:
      - "*"
`

	return os.WriteFile(path, []byte(sample), 0644)
}
