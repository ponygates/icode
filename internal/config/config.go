// Package config manages iCode runtime configuration from files, environment, and CLI flags.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration object.
type Config struct {
	mu sync.RWMutex

	Language  string              `yaml:"language" json:"language"`
	Providers map[string]ProviderCfg `yaml:"providers" json:"providers"`
	Models    []ModelCfg          `yaml:"models" json:"models"`
	Defaults  DefaultCfg          `yaml:"defaults" json:"defaults"`
	TUI       TUICfg              `yaml:"tui" json:"tui"`
	Tools     ToolsCfg            `yaml:"tools" json:"tools"`
	Server    ServerCfg           `yaml:"server" json:"server"`
	Update    UpdateCfg           `yaml:"update" json:"update"`
}

// DefaultCfg holds the user's preferred model / provider / permission mode,
// persisted by the settings interface and applied on chat startup.
type DefaultCfg struct {
	Model    string `yaml:"model" json:"model"`
	Provider string `yaml:"provider" json:"provider"`
	Mode     string `yaml:"mode" json:"mode"`
}

type ProviderCfg struct {
	APIKey    string `yaml:"api_key" json:"-"`
	APIBase   string `yaml:"api_base" json:"api_base,omitempty"`
	Timeout   int    `yaml:"timeout_sec" json:"timeout_sec,omitempty"`
	Disabled  bool   `yaml:"disabled" json:"disabled,omitempty"`
}

// ModelCfg describes a (possibly user-defined) model entry. Built-in models
// come from the provider registry; users can add custom models or override the
// display name of a built-in one. `ID` is the stable key "provider/model_id".
type ModelCfg struct {
	ID            string `yaml:"id" json:"id"`                                     // stable key: provider/model_id
	Provider      string `yaml:"provider" json:"provider"`
	ModelID       string `yaml:"model_id" json:"model_id"`
	Name          string `yaml:"name" json:"name"`                                 // editable display name
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
	Theme     string `yaml:"theme" json:"theme"`
	SyntaxHL  bool   `yaml:"syntax_highlight" json:"syntax_highlight"`
	DiffMode  string `yaml:"diff_mode" json:"diff_mode"`
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

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		Language: "zh-CN",
		Defaults: DefaultCfg{
			Model:    "deepseek-v4-flash",
			Provider: "deepseek",
			Mode:     "agent",
		},
		Providers: map[string]ProviderCfg{
			"deepseek":   {APIBase: "https://api.deepseek.com/v1", Timeout: 120},
			"openrouter": {APIBase: "https://openrouter.ai/api/v1", Timeout: 120},
			"zhipu":      {APIBase: "https://open.bigmodel.cn/api/paas/v4", Timeout: 120},
			"kimi":       {APIBase: "https://api.moonshot.cn/v1", Timeout: 120},
		},
		TUI: TUICfg{
			Theme:    "auto",
			SyntaxHL: true,
			DiffMode: "unified",
		},
		Tools: ToolsCfg{
			BashTimeout: 120,
		},
		Server: ServerCfg{
			Port: 0,
			Host: "127.0.0.1",
		},
		Update: UpdateCfg{
			AutoUpdate: true,
			Channel:    "github",
			IntervalH:  24,
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

	// 1. Try local project config
	paths := []string{
		".icoderc.yaml",
		".icoderc.yml",
		".icode/config.yaml",
	}

	for _, p := range paths {
		if err := mergeFile(cfg, p); err != nil && !os.IsNotExist(err) {
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

	return cfg, nil
}

func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, cfg)
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

// Save writes the current config to disk.
func (c *Config) Save(path string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0644)
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
