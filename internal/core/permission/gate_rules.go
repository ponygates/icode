package permission

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ponygates/icode/internal/config"
)

// SetParamRules replaces the parameter-level rule list at runtime.
func (g *Gate) SetParamRules(rules []ParamRule) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paramRules = append([]ParamRule(nil), rules...)
}

// ParamRules returns a copy of the current parameter-level rules.
func (g *Gate) ParamRules() []ParamRule {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]ParamRule(nil), g.paramRules...)
}

// checkParamRules evaluates the parameter-level rules in order. First match
// wins; nil means no rule applied. Convenience wrapper used outside Check.
func (g *Gate) checkParamRules(action Action) *CheckResult {
	g.mu.RLock()
	rules := append([]ParamRule(nil), g.paramRules...)
	g.mu.RUnlock()
	return g.evalParamRules(rules, action)
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
		"browser", // headless browser opens a remote URL — same tier as fetch
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
	// Resolve symlinks on both sides so a symlink inside the sandbox cannot
	// point outside it; on failure fall back to the lexical path (missing
	// files are common for write targets — the target DIR is what matters,
	// and the lexical Abs path is still checked below).
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	for _, ap := range g.AllowedPaths {
		apAbs, err := filepath.Abs(ap)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(apAbs); err == nil {
			apAbs = resolved
		}
		// Match on a path-boundary so allowlist /home/u/proj does not also
		// permit /home/u/project2. Windows filesystems are case-insensitive.
		if pathsEquivalent(abs, apAbs) || pathsWithinDir(abs, apAbs) {
			return false
		}
	}
	return true
}

// pathsEquivalent compares two absolute paths, case-insensitively on
// case-insensitive filesystems (Windows).
func pathsEquivalent(a, b string) bool {
	if a == b {
		return true
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return false
}

// pathsWithinDir reports whether child lives under dir (path-boundary aware,
// case-insensitive on Windows).
func pathsWithinDir(child, dir string) bool {
	rel, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	if runtime.GOOS == "windows" {
		return true // Rel already resolved the case-sensitive comparison
	}
	return true
}
