// Package permission provides the tool execution approval system for iCode.
//
// Three operational modes:
//   - Plan (只读): survey only, no modifications allowed. LLM can read/search but
//     cannot write, execute, or delete. All mutating operations are blocked.
//   - Agent (确认): each tool call requests user approval before execution.
//     Supports single-allow, session-allow, and deny decisions.
//   - YOLO (自动): auto-approve all operations within configured bounds.
package permission

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Mode defines the operational permission level.
type Mode string

const (
	ModePlan  Mode = "plan"
	ModeAgent Mode = "agent"
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

	// Allowed paths — tools can only read/write within these directories
	AllowedPaths []string

	// Denied commands — shell commands that are always blocked
	DeniedCommands []string

	// Per-session allow-all state
	sessionAllows map[string]bool // sessionID → true if all-tool allow is active

	// Hooks is a per-tool/per-path allowlist loaded from $ICODE_HOME/hooks.yaml
	hooks *HooksConfig
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
func NewGate(mode Mode) *Gate {
	return &Gate{
		mode:          mode,
		DeniedCommands: []string{"rm -rf", "sudo", "chmod 777", "dd if=", "mkfs."},
		sessionAllows: make(map[string]bool),
		hooks:         loadHooks(),
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
	}
	return readOnlyTools[action.Tool]
}

func (g *Gate) isDenied(action Action) bool {
	if action.Tool != "bash" {
		return false
	}

	cmd := strings.ToLower(strings.TrimSpace(action.Command))
	for _, denied := range g.DeniedCommands {
		if strings.Contains(cmd, strings.ToLower(denied)) {
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
