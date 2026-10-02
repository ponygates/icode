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
)

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
