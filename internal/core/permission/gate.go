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
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

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
	mu   sync.RWMutex
	mode Mode

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

	// connectDomains is the Connect-tier silent whitelist (本书 ch.22 静默白名单):
	// hosts the user has explicitly approved at least once. In Auto mode a
	// subsequent fetch to a trusted host is granted without re-prompting —
	// the user already confirmed that destination.
	connectDomains map[string]bool // host → trusted

	// claudeSettings holds Claude Code's settings.json permission rules
	// (loaded from ~/.claude/settings.json + project .claude/settings.json).
	// Evaluated in Agent mode alongside hooks.yaml; deny wins over allow.
	claudeSettings *ClaudeSettings
}

// SetClaudeSettings installs Claude Code permission rules for Agent mode.
func (g *Gate) SetClaudeSettings(cs *ClaudeSettings) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.claudeSettings = cs
}

// LoadClaudeSettings is the public entry point for parsing Claude settings
// (cached by mtime). Exported so app bootstrap can hand it to the gate.
func LoadClaudeSettings(dir string) *ClaudeSettings {
	return loadClaudeSettings(dir)
}

// checkClaudeSettings evaluates Claude Code allow/deny patterns for the
// action. Deny always wins; an allow match auto-approves. Returns nil when
// no rule applies (fall through to the normal flow).
func (g *Gate) checkClaudeSettings(action Action) *CheckResult {
	if g.claudeSettings == nil {
		return nil
	}
	for _, pattern := range g.claudeSettings.Permissions.Deny {
		if claudeAllowMatch(action, pattern) {
			return &CheckResult{
				Decision: DecisionDeny,
				Reason:   fmt.Sprintf("Denied by .claude/settings.json: %s", pattern),
				Prompt:   g.buildPrompt(action),
			}
		}
	}
	for _, pattern := range g.claudeSettings.Permissions.Allow {
		if claudeAllowMatch(action, pattern) {
			return &CheckResult{
				Decision: DecisionAllow,
				Reason:   fmt.Sprintf("Allowed by .claude/settings.json: %s", pattern),
				Prompt:   g.buildPrompt(action),
			}
		}
	}
	return nil
}

// HooksConfig represents the hooks.yaml configuration.
type HooksConfig struct {
	Tools []ToolRule `yaml:"tools"`
}

type ToolRule struct {
	Name  string   `yaml:"name"`
	Allow []string `yaml:"allow"` // patterns that are always allowed
	Deny  []string `yaml:"deny"`  // patterns that are always denied
	Ask   []string `yaml:"ask"`   // patterns that require user approval
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

// TrustDomain records that the user has approved a Connect-tier destination.
// Pass a full URL or a bare host; the host is normalised (lowercase, port
// preserved) and remembered so Auto mode stops re-asking for it.
func (g *Gate) TrustDomain(raw string) {
	host := HostOf(raw)
	if host == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.connectDomains[host] = true
}

// UntrustDomain removes a host from the Connect-tier whitelist. A future fetch
// to it will be prompted again.
func (g *Gate) UntrustDomain(raw string) {
	host := HostOf(raw)
	if host == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.connectDomains, host)
}

// IsDomainTrusted reports whether the given URL/host is in the Connect-tier
// whitelist.
func (g *Gate) IsDomainTrusted(raw string) bool {
	host := HostOf(raw)
	if host == "" {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.connectDomains[host]
}

// TrustedDomains returns a copy of the current whitelist (for diagnostics/UI).
func (g *Gate) TrustedDomains() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.connectDomains))
	for h := range g.connectDomains {
		out = append(out, h)
	}
	return out
}

// HostOf extracts a normalised host (lowercase, port preserved) from a full
// URL or bare host string. Returns "" for unparseable input.
func HostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := u.Host
	if i := strings.IndexByte(host, '@'); i >= 0 {
		host = host[i+1:]
	}
	host = strings.ToLower(host)
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "]") {
		// Trailing :port, but never split a bracketed IPv6 literal like "[::1]".
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// connectDomainTrusted is the non-locking form used inside Check (which already
// holds the read lock). Empty-URL tools (web_search by query) have no fixed
// destination and therefore never match.
func (g *Gate) connectDomainTrusted(action Action) bool {
	if action.URL == "" {
		return false
	}
	host := HostOf(action.URL)
	if host == "" {
		return false
	}
	return g.connectDomains[host]
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
		// Four-tier permission (本书 ch.22): Read is auto-approved; Write,
		// Execute, and Connect all require confirmation — Connect tools
		// (fetch/web_search) touch external networks, so even though they
		// don't mutate local state they are NOT silently auto-approved.
		level := AccessLevelOf(action.Tool)
		if level == AccessRead && g.isReadOnly(action) {
			return CheckResult{Decision: DecisionAllow, Reason: "Auto mode: Read-tier operation", Prompt: prompt}
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
		// Connect-tier silent whitelist (本书 ch.22 静默白名单): a destination
		// the user approved once is auto-allowed on subsequent visits without a
		// re-prompt. Requires the URL field (web_search-by-query never matches).
		if level == AccessConnect && g.connectDomainTrusted(action) {
			return CheckResult{Decision: DecisionAllow, Reason: "Connect tier: domain already approved by user", Prompt: prompt}
		}
		return CheckResult{
			Decision: DecisionAsk,
			Prompt:   prompt,
			Reason:   fmt.Sprintf("Auto mode: %s-tier operation needs confirmation", level),
		}

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

		// Check Claude Code settings.json rules (deny wins over allow)
		if decision := g.checkClaudeSettings(action); decision != nil {
			return *decision
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
		"read_image": true,
		"ls":         true,
		"grep":       true,
		"glob":       true,
		"git_diff":   true,
		"git_status": true,
		"fetch":      true,
		"disk_usage": true,
		// code_search only reads the in-memory symbol index.
		"code_search": true,
		// task_output only inspects the user's own background tasks
		// (kill included — the task was started via an approved bash call).
		"task_output": true,
		// todo_write only mutates in-process session state, never the
		// filesystem or external systems — safe to auto-approve.
		"todo_write": true,
	}
	return readOnlyTools[action.Tool]
}

// AccessLevelOf classifies a tool into the four-tier permission model
// (Read / Write / Execute / Connect). Used by the gate to decide how a tool
// is treated in Auto mode: Connect tools (fetch, web_search) reach external
// networks, so they are NOT silently auto-approved even though they are
// logically "read-only" — the model must justify the network call (本书
// ch.22: "Connect 涉及外部交互").
func AccessLevelOf(toolName string) AccessLevel {
	switch toolName {
	// Connect — external network / services. Even though fetch and web_search
	// don't mutate local state, they exfiltrate a URL/keyword to a remote
	// service, so they get their own tier above Read.
	case "fetch", "web_search", "web_fetch", "search_web",
		"image_gen", "video_gen", "mcp_call":
		return AccessConnect

	// Execute — runs commands / changes system state.
	case "bash", "run_command", "cmd", "git_commit",
		// Computer-use control: mouse/keyboard drives the user's real desktop,
		// so it is gated like command execution — never auto-approved in Auto
		// mode (本书 ch.22 高敏感工具).
		"mouse_move", "mouse_click", "mouse_scroll", "type_text", "key_press":
		return AccessExecute

	// Write — mutates local files or workspace.
	case "write_file", "edit", "search_replace", "git_branch",
		"disk_cleanup", "todo_write":
		return AccessWrite

	// Screenshot is observation-only but classified as Read (not auto-approved
	// in Auto mode because a capture may contain private on-screen data).
	case "screenshot":
		return AccessRead

	// Everything else is observation-only.
	default:
		return AccessRead
	}
}

// outsideAllowedPaths reports whether a path-bearing tool targets a location
// outside the configured AllowedPaths sandbox. An empty allowlist means no
// restriction. Containment is enforced for every file tool — not just bash —
// so an agent cannot escape the workspace with write_file/edit/read_file/ls/
// grep/glob in YOLO mode (see isDenied).
func (g *Gate) outsideAllowedPaths(action Action) bool {
	if len(g.AllowedPaths) == 0 || action.Path == "" {
		return false
	}

	switch action.Tool {
	case "bash", "read_file", "write_file", "edit", "search_replace", "ls", "grep", "glob":
	default:
		return false
	}

	abs, err := filepath.Abs(action.Path)
	if err != nil {
		return true
	}
	for _, ap := range g.AllowedPaths {
		apAbs, err := filepath.Abs(ap)
		if err != nil {
			continue
		}
		// Match on a path-boundary so allowlist /home/u/proj does not also
		// permit /home/u/project2.
		if abs == apAbs || strings.HasPrefix(abs, apAbs+string(os.PathSeparator)) {
			return false
		}
	}
	return true
}

func (g *Gate) isDenied(action Action) bool {
	// Sandbox containment applies to all file tools, not just bash.
	if g.outsideAllowedPaths(action) {
		return true
	}

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

	return false
}

// ClaudeSettings models the Claude Code settings.json permission block that
// iCode reads for ecosystem compatibility. Users who already maintain
// .claude/settings.json rules should not have to duplicate them for iCode.
type ClaudeSettings struct {
	Permissions ClaudePermissions `json:"permissions"`
}

// ClaudePermissions mirrors Claude Code's permissions object.
type ClaudePermissions struct {
	// Allow lists tool-call patterns that are always auto-approved, e.g.
	// "Bash(git status:*)", "Edit(**.md)", or "*" for everything.
	Allow []string `json:"allow"`
	// Deny lists tool-call patterns that are always rejected.
	Deny []string `json:"deny"`
}

// claudeSettingsCache caches parsed Claude settings per project dir (key =
// directory). Re-parsed when the file mtime changes.
type claudeSettingsCache struct {
	mu      sync.Mutex
	entries map[string]*claudeSettingsEntry
}

type claudeSettingsEntry struct {
	mtime time.Time
	cfg   *ClaudeSettings
}

var claudeSettingsCacheInst = &claudeSettingsCache{entries: make(map[string]*claudeSettingsEntry)}

// loadClaudeSettings returns the merged Claude Code permission rules for the
// project at dir. Merging order (later wins on conflict): user home
// settings, then project .claude/settings.json, then .claude/settings.local.json.
// Returns nil when no settings file exists.
func loadClaudeSettings(dir string) *ClaudeSettings {
	if dir == "" {
		dir = "."
	}
	cacheKey := dir
	cache := claudeSettingsCacheInst

	// Check cache
	cache.mu.Lock()
	if ent, ok := cache.entries[cacheKey]; ok {
		if newest := newestSettingsMtime(dir); !newest.After(ent.mtime) {
			cache.mu.Unlock()
			return ent.cfg
		}
	}
	cache.mu.Unlock()

	// Parse fresh
	var merged *ClaudeSettings
	paths := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".claude", "settings.json"))
	}
	paths = append(paths,
		filepath.Join(dir, ".claude", "settings.json"),
		filepath.Join(dir, ".claude", "settings.local.json"),
	)
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cs ClaudeSettings
		if err := json.Unmarshal(data, &cs); err != nil {
			continue
		}
		if merged == nil {
			merged = &cs
		} else {
			merged.Permissions.Allow = append(merged.Permissions.Allow, cs.Permissions.Allow...)
			merged.Permissions.Deny = append(merged.Permissions.Deny, cs.Permissions.Deny...)
		}
	}

	cache.mu.Lock()
	cache.entries[cacheKey] = &claudeSettingsEntry{
		mtime: newestSettingsMtime(dir),
		cfg:   merged,
	}
	cache.mu.Unlock()
	return merged
}

// newestSettingsMtime returns the latest modification time among the Claude
// settings files for dir (zero time when none exist).
func newestSettingsMtime(dir string) time.Time {
	var newest time.Time
	paths := []string{
		filepath.Join(dir, ".claude", "settings.json"),
		filepath.Join(dir, ".claude", "settings.local.json"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".claude", "settings.json"))
	}
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	return newest
}

// claudeAllowMatch evaluates one Claude Code allow/deny pattern against the
// action. Patterns follow Claude Code's "Tool(payload-substring)" syntax:
//
//	"Bash(git status:*)"  — any bash command starting with "git status"
//	"Edit(**)"            — any edit tool call
//	"Read(**.md)"         — any read-tier tool whose path ends with .md
//	"*"                   — everything
//
// Tool names are case-insensitive and the four-tier groups (Read/Write/
// Edit/Bash) map onto iCode's AccessLevel tiers. Unknown tool names never
// match.
func claudeAllowMatch(action Action, pattern string) bool {
	p := strings.TrimSpace(pattern)
	if p == "*" {
		return true
	}
	open := strings.IndexByte(p, '(')
	if open <= 0 || !strings.HasSuffix(p, ")") {
		return false
	}
	toolPart := strings.ToLower(p[:open])
	argPart := p[open+1 : len(p)-1]

	// Tier groups cover whole families: Read = read_file/grep/glob/ls,
	// Write = write_file/edit/search_replace, Edit = edit/search_replace.
	group := func(tool string) string {
		switch tool {
		case "read_file", "grep", "glob", "ls", "git_diff", "git_status", "git_log", "git_branch", "disk_usage", "code_search":
			return "read"
		case "write_file", "edit", "search_replace":
			return "write"
		case "bash":
			return "bash"
		case "fetch", "web_search":
			return "fetch"
		default:
			return tool
		}
	}
	actionGroup := group(action.Tool)
	switch toolPart {
	case "read":
		if actionGroup != "read" {
			return false
		}
	case "write", "edit":
		if actionGroup != "write" {
			return false
		}
	case "bash":
		if actionGroup != "bash" {
			return false
		}
	case "fetch":
		if actionGroup != "fetch" {
			return false
		}
	default:
		// Exact tool name (case-insensitive), e.g. "TodoWrite" or "web_search".
		if toolPart != action.Tool && !strings.HasPrefix(action.Tool, toolPart) {
			return false
		}
	}
	return claudeArgMatch(action, argPart)
}

// claudeArgMatch matches the parenthesised argument part of a Claude pattern
// against the action's primary payload, following Claude Code's semantics:
//
//   - A trailing ":*" is the "anything after this" idiom — the pattern becomes
//     a prefix match ("Bash(git status:*)" allows any command starting with
//     "git status").
//   - "*" wildcards are glob-like; "**" spans path separators ("Edit(**.md)"
//     matches any .md file at any depth).
//   - For fetch, the pattern matches the URL's host (Claude Code treats
//     Fetch(domain:*) as a domain allowlist).
func claudeArgMatch(action Action, argPart string) bool {
	if argPart == "" || argPart == "*" {
		return true
	}
	var target string
	switch action.Tool {
	case "bash":
		target = action.Command
	case "read_file", "write_file", "edit", "search_replace", "glob":
		target = action.Path
	case "grep":
		target = action.Path + " " + action.Pattern
	case "fetch":
		if u, err := url.Parse(action.URL); err == nil && u.Host != "" {
			target = u.Host
		} else {
			target = action.URL
		}
	default:
		target = action.Arguments
	}
	if target == "" {
		return false
	}
	// "git status:*" / "example.com:*" — Claude Code's prefix idiom.
	trimmed := strings.TrimSuffix(argPart, ":*")
	if trimmed != argPart {
		return strings.HasPrefix(target, trimmed)
	}
	re := globToRegexp(argPart)
	if re == nil {
		return false
	}
	return re.MatchString(target)
}

// globToRegexp converts a glob pattern into a compiled regexp. "*" matches
// any run of characters that does not cross a path separator; "**" matches
// anything (recursive); "?" matches a single non-separator character; a
// trailing ":*" collapses to a bare prefix match (Claude Code idiom).
func globToRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '*':
			// Trailing ":*" — prefix match without requiring the colon.
			if i == len(runes)-1 && i > 0 && runes[i-1] == ':' {
				b.WriteString(":?")
				b.WriteString(".*")
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/\\\\]*")
			}
		case '?':
			b.WriteString("[^/\\\\]")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
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
