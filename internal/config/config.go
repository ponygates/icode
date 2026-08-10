// Package config manages iCode runtime configuration from files, environment, and CLI flags.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/ponygates/icode/internal/secure"
)

// SecurityLevel controls how data is handled when sent to external services.
// Higher levels = more restrictive. This addresses Claude Code's opaque
// telemetry problem — iCode NEVER sends data externally without the user
// knowing exactly what level is active.
type SecurityLevel string

const (
	SecLocal        SecurityLevel = "local"        // 本地处理: no API calls at all, pure local
	SecDesensitize  SecurityLevel = "desensitize"  // 脱敏处理: sanitize PII before sending
	SecLocalLLM     SecurityLevel = "local-llm"    // 本地大模型: local models only (Ollama etc)
	SecForeignLLM   SecurityLevel = "foreign-llm"  // 国外大模型: international API providers allowed
	SecUnrestricted SecurityLevel = "unrestricted" // 无限制: all providers, no restrictions
)

func ParseSecurityLevel(s string) SecurityLevel {
	switch s {
	case "local":
		return SecLocal
	case "desensitize":
		return SecDesensitize
	case "local-llm":
		return SecLocalLLM
	case "foreign-llm":
		return SecForeignLLM
	case "unrestricted":
		return SecUnrestricted
	default:
		return SecLocal // safest default
	}
}

// boolPtr returns a pointer to b (handy for optional bool config fields whose
// zero value is meaningful).
func boolPtr(b bool) *bool { return &b }

// Config is the root configuration object.
type Config struct {
	mu sync.RWMutex

	Language      string                 `yaml:"language" json:"language"`
	SecurityLevel SecurityLevel          `yaml:"security_level" json:"security_level"`
	Providers     map[string]ProviderCfg `yaml:"providers" json:"providers"`
	Models        []ModelCfg             `yaml:"models" json:"models"`
	Defaults      DefaultCfg             `yaml:"defaults" json:"defaults"`
	TUI           TUICfg                 `yaml:"tui" json:"tui"`
	Tools         ToolsCfg               `yaml:"tools" json:"tools"`
	Server        ServerCfg              `yaml:"server" json:"server"`
	Update        UpdateCfg              `yaml:"update" json:"update"`
	LSP           LSPCfg                 `yaml:"lsp" json:"lsp"`
	MCP           []MCPServerCfg         `yaml:"mcp" json:"mcp"`
	// MCPImportWorkBuddy controls whether MCP servers configured in
	// WorkBuddy's ~/.workbuddy/mcp.json are auto-imported at startup
	// (explicit iCode entries always win on name conflicts). Default: true.
	MCPImportWorkBuddy *bool `yaml:"mcp_import_workbuddy,omitempty" json:"mcp_import_workbuddy,omitempty"`
	// Routing configures the "auto" model router.
	Routing RoutingCfg `yaml:"routing" json:"routing"`
	// Multimodal configures the image/video generation backend used by the
	// image_gen and video_gen tools.
	Multimodal MultimodalCfg `yaml:"multimodal" json:"multimodal"`
	// Hooks maps lifecycle event names (PreToolUse/PostToolUse/
	// UserPromptSubmit/Stop) to hook rules — external commands fired during
	// the agent loop.
	Hooks map[string][]HookRule `yaml:"hooks" json:"hooks"`
	// Autostart, when true, registers iCode to launch in desktop mode on OS
	// login (Windows: HKCU Run key; macOS: LaunchAgent; Linux: autostart
	// desktop file). Default false — iCode never auto-starts without an
	// explicit opt-in from the user.
	Autostart bool `yaml:"autostart,omitempty" json:"autostart,omitempty"`
	// Proxy is an optional HTTP(S) proxy URL (e.g. http://127.0.0.1:7890)
	// applied to all model/provider traffic. Empty means the standard
	// HTTP_PROXY / HTTPS_PROXY environment variables are honoured instead.
	// It is applied to the process environment at server start and on each
	// settings save, so it takes effect without a restart.
	Proxy string `yaml:"proxy,omitempty" json:"proxy,omitempty"`
}

// HookRule mirrors hooks.Rule but lives here so config stays dependency-free.
type HookRule struct {
	Matcher string `yaml:"matcher" json:"matcher"`
	Command string `yaml:"command" json:"command"`
	Timeout int    `yaml:"timeout" json:"timeout"`
}

// MCPServerCfg describes a single Model Context Protocol server connection.
// It mirrors mcp.ServerConfig so the desktop settings UI can fully manage it.
type MCPServerCfg struct {
	Name    string            `yaml:"name" json:"name"`
	Type    string            `yaml:"type" json:"type"` // stdio | sse
	Command string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env     []string          `yaml:"env,omitempty" json:"env,omitempty"`
	URL     string            `yaml:"url,omitempty" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// HeadersEnc holds the DPAPI-encrypted form of Headers (which may carry
	// Authorization bearer tokens) for disk persistence. Only ciphertext
	// reaches the disk; the plaintext map stays in memory at runtime.
	HeadersEnc string `yaml:"headers_enc,omitempty" json:"-"`
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	TrustMode  string `yaml:"trust_mode,omitempty" json:"trust_mode,omitempty"` // ask | readonly | all
}

// DefaultCfg holds the user's preferred model / provider / permission mode,
// persisted by the settings interface and applied on chat startup.
type DefaultCfg struct {
	Model          string   `yaml:"model" json:"model"`
	Provider       string   `yaml:"provider" json:"provider"`
	Mode           string   `yaml:"mode" json:"mode"`
	Temperature    float64  `yaml:"temperature" json:"temperature"`
	MaxTokens      int      `yaml:"max_tokens" json:"max_tokens"`
	Cache          bool     `yaml:"cache" json:"cache"`
	SystemPrompt   string   `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
	FallbackModels []string `yaml:"fallback_models,omitempty" json:"fallback_models,omitempty"`
	// OutputStyle controls answer verbosity injected into the system prompt:
	// concise | normal | verbose (empty = normal).
	OutputStyle string `yaml:"output_style,omitempty" json:"output_style,omitempty"`
	// ExtraDirs are additional working directories (beyond cwd) the agent may
	// reference, surfaced in the system prompt (Claude Code /add-dir parity).
	ExtraDirs []string `yaml:"extra_dirs,omitempty" json:"extra_dirs,omitempty"`
	// WorkingDir is the directory /cd persists so subsequent launches of the
	// CLI/TUI/server start there instead of the process launch directory.
	WorkingDir string `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
	// Smart model routing: cheap model for simple queries, powerful for complex
	CheapModel    string `yaml:"cheap_model,omitempty" json:"cheap_model,omitempty"`
	CheapProv     string `yaml:"cheap_provider,omitempty" json:"cheap_provider,omitempty"`
	PowerfulModel string `yaml:"powerful_model,omitempty" json:"powerful_model,omitempty"`
	PowerfulProv  string `yaml:"powerful_provider,omitempty" json:"powerful_provider,omitempty"`
}

type ProviderCfg struct {
	APIKey string `yaml:"api_key,omitempty" json:"-"`
	// APIKeyEnc holds the DPAPI-encrypted form of APIKey for disk persistence.
	// It is the only key representation written to disk; APIKey stays in memory.
	APIKeyEnc string `yaml:"api_key_enc,omitempty" json:"-"`
	APIBase   string `yaml:"api_base" json:"api_base,omitempty"`
	Timeout   int    `yaml:"timeout_sec" json:"timeout_sec,omitempty"`
	Disabled  bool   `yaml:"disabled" json:"disabled,omitempty"`
}

// ModelCfg describes a (possibly user-defined) model entry. Built-in models
// come from the provider registry; users can add custom models or override the
// display name of a built-in one. `ID` is the stable key "provider/model_id".
type ModelCfg struct {
	ID            string `yaml:"id" json:"id"` // stable key: provider/model_id
	Provider      string `yaml:"provider" json:"provider"`
	ModelID       string `yaml:"model_id" json:"model_id"`
	Name          string `yaml:"name" json:"name"` // editable display name
	BaseURL       string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	ContextWindow int    `yaml:"context_window,omitempty" json:"context_window,omitempty"`
	MaxOutput     int    `yaml:"max_output_tokens,omitempty" json:"max_output_tokens,omitempty"`
	FreeTier      bool   `yaml:"free_tier,omitempty" json:"free_tier,omitempty"`
	Custom        bool   `yaml:"custom,omitempty" json:"custom,omitempty"` // true for user-added models
}

// ModelKey builds the stable model key "provider/model_id".
func ModelKey(provider, modelID string) string {
	return provider + "/" + modelID
}

type TUICfg struct {
	Theme    string `yaml:"theme" json:"theme"`
	SyntaxHL bool   `yaml:"syntax_highlight" json:"syntax_highlight"`
	DiffMode string `yaml:"diff_mode" json:"diff_mode"`
	// Vim toggles vi-style key bindings in the CLI TUI (mirrors Claude Code's
	// /vim). Off by default.
	Vim bool `yaml:"vim" json:"vim"`
	// ShowStatusLine controls the bottom status bar. Defaults to true; a nil
	// pointer means "unset" → treated as true by consumers.
	ShowStatusLine *bool `yaml:"show_status_line,omitempty" json:"show_status_line,omitempty"`
}

type ToolsCfg struct {
	BashTimeout    int      `yaml:"bash_timeout_sec" json:"bash_timeout_sec"`
	AllowedPaths   []string `yaml:"allowed_paths" json:"allowed_paths"`
	DeniedCommands []string `yaml:"denied_commands" json:"denied_commands"`
}

type ServerCfg struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Port    int    `yaml:"port" json:"port"`
	Host    string `yaml:"host" json:"host"`
}

type UpdateCfg struct {
	AutoUpdate bool   `yaml:"auto_update" json:"auto_update"`
	Channel    string `yaml:"channel" json:"channel"`
	IntervalH  int    `yaml:"interval_hours" json:"interval_hours"`
}

// LSPCfg controls language-server-backed code intelligence (diagnostics
// injected after tool execution). When Enabled, the engine lazily starts the
// matching language server (gopls/pyright/...) on first file edit.
type LSPCfg struct {
	Enabled   bool     `yaml:"enabled" json:"enabled"`
	AutoStart []string `yaml:"auto_start" json:"auto_start"` // language IDs to start eagerly (e.g. ["go","python"])
}

// RoutingCfg configures the "auto" model router.
//   - Mode "embedding" (DEFAULT): local, offline, zero-token semantic classifier
//     (nearest-centroid cosine over hashed features) that refines the
//     keyword baseline only when confident — smarter routing without spending
//     any tokens on classification. An empty mode also maps to embedding.
//   - Mode "keyword": zero-cost keyword/length heuristic only (legacy/fallback).
//   - Mode "llm": grade complexity with a cheap LLM call (3s budget,
//     falls back to keyword heuristic on error/timeout).
type RoutingCfg struct {
	Mode            string `yaml:"mode" json:"mode"`                                             // keyword | embedding | llm (default: embedding)
	ClassifierModel string `yaml:"classifier_model,omitempty" json:"classifier_model,omitempty"` // model for llm mode; defaults to cheap model
}

// MultimodalCfg configures the backend for the image_gen / video_gen tools.
// The default backend speaks the OpenAI-compatible images API, which most
// providers (OpenAI, Zhipu, volcengine, and WorkBuddy's multimodal gateway)
// expose. When unset the tools return a friendly "not configured" hint rather
// than an error, so the model can gracefully explain the missing setup.
type MultimodalCfg struct {
	// ImageBaseURL is the OpenAI-compatible images endpoint base
	// (e.g. https://api.openai.com/v1). The tool POSTs to {base}/images/generations.
	ImageBaseURL string `yaml:"image_base_url,omitempty" json:"image_base_url,omitempty"`
	// ImageModel is the image model id (e.g. "dall-e-3", "cogview-3").
	ImageModel string `yaml:"image_model,omitempty" json:"image_model,omitempty"`
	// VideoBaseURL is the video generation endpoint base. The tool POSTs to
	// {base}/videos/generations and, when the response is async, polls the
	// returned task id.
	VideoBaseURL string `yaml:"video_base_url,omitempty" json:"video_base_url,omitempty"`
	// VideoModel is the video model id (e.g. "cogvideox", "sora").
	VideoModel string `yaml:"video_model,omitempty" json:"video_model,omitempty"`
	// APIKey authenticates both endpoints (Bearer). Falls back to the
	// ICODE_MULTIMODAL_API_KEY / OPENAI_API_KEY environment variables.
	APIKey string `yaml:"api_key,omitempty" json:"-"`
	// APIKeyEnc holds the DPAPI-encrypted form of APIKey for disk persistence.
	APIKeyEnc string `yaml:"api_key_enc,omitempty" json:"-"`
	// OutputDir is where generated media is saved. Defaults to ./.icode/generated.
	OutputDir string `yaml:"output_dir,omitempty" json:"output_dir,omitempty"`
}

// Default returns a Config populated with sensible defaults.
// The default security level is "local" — iCode NEVER sends data externally
// without explicit user consent. No telemetry, no tracking, no phone-home.
// OutputStyleDirective returns the behavioral directive appended to the system
// prompt for a given output style. Empty for "normal" (default).
func OutputStyleDirective(style string) string {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "concise":
		return "Answer style: be extremely concise. Get to the point in as few words as possible; skip preamble, restating the question, and closing summaries unless the user asks for detail."
	case "verbose":
		return "Answer style: be thorough and explanatory. Show your reasoning, include relevant examples, and give step-by-step detail."
	default:
		return ""
	}
}

// EffectiveSystemPrompt composes the runtime system prompt from the user's
// base prompt plus the output-style directive and any extra working dirs
// (/output-style, /add-dir). Centralized so CLI startup and live slash-command
// changes stay consistent.
func EffectiveSystemPrompt(c *Config) string {
	if c == nil {
		return ""
	}
	base := strings.TrimSpace(c.Defaults.SystemPrompt)
	var parts []string
	if base != "" {
		parts = append(parts, base)
	}
	if d := OutputStyleDirective(c.Defaults.OutputStyle); d != "" {
		parts = append(parts, d)
	}
	if len(c.Defaults.ExtraDirs) > 0 {
		parts = append(parts, "Additional working directories you may read and reference beyond the current directory:\n"+strings.Join(c.Defaults.ExtraDirs, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func Default() *Config {
	return &Config{
		Language:      "zh-CN",
		SecurityLevel: SecForeignLLM,
		Defaults: DefaultCfg{
			Model:    "openrouter/free",
			Provider: "openrouter",
			Mode:     "agent",
			// 默认温度 0（本书 ch.23）：代码与工具判定任务需要确定性
			// （相同输入 → 相同输出）。用户可在配置或 /config 中调高。
			Temperature: 0,
			MaxTokens:   0,
			Cache:       true,
		},
		Providers: map[string]ProviderCfg{
			"deepseek":   {APIBase: "https://api.deepseek.com", Timeout: 120},
			"openrouter": {APIBase: "https://openrouter.ai/api/v1", Timeout: 120},
			"zhipu":      {APIBase: "https://open.bigmodel.cn/api/paas/v4", Timeout: 120},
			"kimi":       {APIBase: "https://api.moonshot.cn/v1", Timeout: 120},
			"nvidia":     {APIBase: "https://integrate.api.nvidia.com/v1", Timeout: 120},
		},
		TUI: TUICfg{
			Theme:          "auto",
			SyntaxHL:       true,
			DiffMode:       "unified",
			ShowStatusLine: boolPtr(true),
		},
		Tools: ToolsCfg{
			BashTimeout: 120,
		},
		Routing: RoutingCfg{
			Mode: "embedding", // local, zero-token semantic routing by default
		},
		Server: ServerCfg{
			Port: 0,
			Host: "127.0.0.1",
		},
		Autostart: false,
		Update: UpdateCfg{
			AutoUpdate: true,
			Channel:    "github",
			IntervalH:  24,
		},
		LSP: LSPCfg{
			Enabled: true,
		},
	}
}

// DefaultPath returns the canonical location for the user config file
// (~/.icode/config.yaml), used by the settings interface to persist changes.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".icode/config.yaml"
	}
	return filepath.Join(home, ".icode", "config.yaml")
}

// LoadOrCreate reads config from disk, returning defaults (and NOT writing)
// when no file exists yet.
func LoadOrCreate() (*Config, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// Load reads config from the standard locations.
// Priority: env → local file → home directory → defaults.
func Load() (*Config, error) {
	cfg := Default()

	// 1. Try local project config (YAML and TOML)
	yamlPaths := []string{
		".icoderc.yaml",
		".icoderc.yml",
		".icode/config.yaml",
		".icode/config.yml",
		"icode.yaml",
		"icode.yml",
	}

	for _, p := range yamlPaths {
		if err := mergeFile(cfg, p); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("load %s: %w", p, err)
		}
	}

	tomlPaths := []string{
		".icoderc.toml",
		"icode.toml",
	}

	for _, p := range tomlPaths {
		if err := mergeTomlFile(cfg, p); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("load %s: %w", p, err)
		}
	}

	// 2. Try home directory config
	home, err := os.UserHomeDir()
	if err == nil {
		homePaths := []string{
			filepath.Join(home, ".icoderc.yaml"),
			filepath.Join(home, ".icode", "config.yaml"),
			filepath.Join(home, ".config", "icode", "config.yaml"),
		}
		for _, p := range homePaths {
			if err := mergeFile(cfg, p); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("load %s: %w", p, err)
			}
		}
	}

	// 3. Environment variable overrides
	applyEnvOverrides(cfg)

	// 4. Restore plaintext API keys from their encrypted disk form. Runs after
	// env overrides so an explicit env key wins over a persisted one.
	decryptConfigKeys(cfg)

	return cfg, nil
}

func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, cfg)
}

func mergeTomlFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return toml.Unmarshal(data, cfg)
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("ICODE_LANG"); v != "" {
		cfg.Language = v
	}
	if v := os.Getenv("DEEPSEEK_API_KEY"); v != "" {
		p := cfg.Providers["deepseek"]
		p.APIKey = v
		cfg.Providers["deepseek"] = p
	}
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
		p := cfg.Providers["openrouter"]
		p.APIKey = v
		cfg.Providers["openrouter"] = p
	}
	if v := os.Getenv("ZHIPU_API_KEY"); v != "" {
		p := cfg.Providers["zhipu"]
		p.APIKey = v
		cfg.Providers["zhipu"] = p
	}
	if v := os.Getenv("KIMI_API_KEY"); v != "" {
		p := cfg.Providers["kimi"]
		p.APIKey = v
		cfg.Providers["kimi"] = p
	}
}

// Save writes the current config to disk under the write lock. Prefer
// WithLock + SaveLocked when mutating several fields and persisting atomically.
func (c *Config) Save(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.SaveLocked(path)
}

// SaveLocked writes the current config to disk WITHOUT taking the lock — the
// caller MUST already hold the write lock (e.g. inside WithLock). Used so a
// batch mutation + persist is atomic; calling it outside a held lock races.
func (c *Config) SaveLocked(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data, err = encryptSecretFields(data)
	if err != nil {
		return fmt.Errorf("encrypt secrets: %w", err)
	}

	// 0600: config.yaml holds encrypted API keys. Group-readable (0640) or
	// world-readable (0644) would expose them on shared machines; DPAPI also
	// binds them to the current user, so owner-only mode matches the trust.
	return os.WriteFile(path, data, 0600)
}

// encryptSecretFields rewrites every plaintext api_key in the marshalled YAML
// into its DPAPI-encrypted api_key_enc form. It operates on the marshalled
// bytes rather than the in-memory Config so the plaintext keys stay available
// at runtime while only ciphertext reaches the disk.
func encryptSecretFields(data []byte) ([]byte, error) {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if providers, ok := root["providers"].(map[string]any); ok {
		for name, v := range providers {
			if pm, ok := v.(map[string]any); ok {
				encryptKey(pm)
				providers[name] = pm
			}
		}
	}
	if mm, ok := root["multimodal"].(map[string]any); ok {
		encryptKey(mm)
	}
	if mcpList, ok := root["mcp"].([]any); ok {
		for _, item := range mcpList {
			if mm, ok := item.(map[string]any); ok {
				encryptHeaders(mm)
			}
		}
	}
	return yaml.Marshal(root)
}

func encryptKey(m map[string]any) {
	key, ok := m["api_key"].(string)
	if !ok || key == "" {
		return
	}
	enc, err := secure.Encrypt(key)
	if err != nil {
		return
	}
	m["api_key_enc"] = enc
	delete(m, "api_key")
}

// encryptHeaders encrypts an MCP server's request headers map (frequently
// holds Authorization bearer tokens) into the headers_enc ciphertext field.
func encryptHeaders(m map[string]any) {
	hdr, ok := m["headers"].(map[string]any)
	if !ok || len(hdr) == 0 {
		return
	}
	data, err := json.Marshal(hdr)
	if err != nil {
		return
	}
	enc, err := secure.Encrypt(string(data))
	if err != nil {
		return
	}
	m["headers_enc"] = enc
	delete(m, "headers")
}

// decryptConfigKeys restores plaintext API keys from their encrypted disk form
// after loading. Keys already set (e.g. by env overrides) are left untouched.
func decryptConfigKeys(cfg *Config) {
	for name, p := range cfg.Providers {
		if p.APIKey == "" && p.APIKeyEnc != "" {
			if dec, err := secure.Decrypt(p.APIKeyEnc); err == nil {
				p.APIKey = dec
			}
			cfg.Providers[name] = p
		}
	}
	if cfg.Multimodal.APIKey == "" && cfg.Multimodal.APIKeyEnc != "" {
		if dec, err := secure.Decrypt(cfg.Multimodal.APIKeyEnc); err == nil {
			cfg.Multimodal.APIKey = dec
		}
	}
	for i := range cfg.MCP {
		mc := &cfg.MCP[i]
		if len(mc.Headers) == 0 && mc.HeadersEnc != "" {
			if dec, err := secure.Decrypt(mc.HeadersEnc); err == nil {
				var h map[string]string
				if json.Unmarshal([]byte(dec), &h) == nil && len(h) > 0 {
					mc.Headers = h
				}
			}
		}
	}
}

// WithLock runs fn while holding the config write lock. Mutations to config
// fields (especially the Providers/Models maps) plus any persistence inside fn
// are therefore atomic with respect to readers and other writers.
//
// NOTE: fn must mutate the Config via direct field assignment (e.g.
// c.Providers[name] = pc) — calling a method that takes the lock again inside
// fn (SetProvider, UpsertMCP, UpsertModel, Save) deadlocks, since Go's write
// lock is not reentrant. SaveLocked is the exception (it is lock-free by
// design and meant to be called here).
func (c *Config) WithLock(fn func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fn()
}

// UpsertMCP adds or replaces an MCP server configuration entry.
func (c *Config) UpsertMCP(m MCPServerCfg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.MCP {
		if c.MCP[i].Name == m.Name {
			c.MCP[i] = m
			return
		}
	}
	c.MCP = append(c.MCP, m)
}

// RemoveMCP deletes an MCP server configuration entry by name.
func (c *Config) RemoveMCP(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	filtered := c.MCP[:0]
	for _, mc := range c.MCP {
		if mc.Name != name {
			filtered = append(filtered, mc)
		}
	}
	c.MCP = filtered
}

// APIKey returns the API key for a provider (config → env fallback).
func (c *Config) APIKey(provider string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if p, ok := c.Providers[provider]; ok && p.APIKey != "" {
		return p.APIKey
	}
	return ""
}

// Provider returns a copy of a provider config entry.
func (c *Config) Provider(name string) (ProviderCfg, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	pc, ok := c.Providers[name]
	return pc, ok
}

// SetProvider inserts or replaces a provider config entry. Use inside
// WithLock when you also need to persist atomically.
func (c *Config) SetProvider(name string, pc ProviderCfg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Providers[name] = pc
}

// DeleteProvider removes a provider config entry.
func (c *Config) DeleteProvider(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Providers, name)
}

// ProvidersCopy returns a shallow copy of the provider map, safe for iteration
// while other goroutines mutate the config.
func (c *Config) ProvidersCopy() map[string]ProviderCfg {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]ProviderCfg, len(c.Providers))
	for k, v := range c.Providers {
		out[k] = v
	}
	return out
}

// ModelsCopy returns a copy of the custom-models slice, safe for iteration.
func (c *Config) ModelsCopy() []ModelCfg {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ModelCfg, len(c.Models))
	copy(out, c.Models)
	return out
}

// WithRLock runs fn while holding the config read lock.
func (c *Config) WithRLock(fn func()) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fn()
}

// UpsertModel adds or updates a model entry, persisting the change.
// The ID is derived from Provider+ModelID; a matching entry is replaced.
func (c *Config) UpsertModel(m ModelCfg) {
	if m.Provider == "" || m.ModelID == "" {
		return
	}
	if m.ID == "" {
		m.ID = ModelKey(m.Provider, m.ModelID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Models {
		if c.Models[i].ID == m.ID {
			c.Models[i] = m
			return
		}
	}
	c.Models = append(c.Models, m)
}

// DeleteModel removes a model entry by its stable ID.
func (c *Config) DeleteModel(id string) bool {
	if id == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Models {
		if c.Models[i].ID == id {
			c.Models = append(c.Models[:i], c.Models[i+1:]...)
			return true
		}
	}
	return false
}

// FindModel returns a model entry by stable ID.
func (c *Config) FindModel(id string) (ModelCfg, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range c.Models {
		if m.ID == id {
			return m, true
		}
	}
	return ModelCfg{}, false
}

// ModelDisplayName returns the user-overridden display name for a
// provider/model_id pair, or "" when no override exists.
func (c *Config) ModelDisplayName(provider, modelID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range c.Models {
		if m.Provider == provider && m.ModelID == modelID && m.Name != "" {
			return m.Name
		}
	}
	return ""
}
