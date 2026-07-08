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
	TUI       TUICfg              `yaml:"tui" json:"tui"`
	Tools     ToolsCfg            `yaml:"tools" json:"tools"`
	Server    ServerCfg           `yaml:"server" json:"server"`
	Update    UpdateCfg           `yaml:"update" json:"update"`
}

type ProviderCfg struct {
	APIKey    string `yaml:"api_key" json:"-"`
	APIBase   string `yaml:"api_base" json:"api_base,omitempty"`
	Timeout   int    `yaml:"timeout_sec" json:"timeout_sec,omitempty"`
	Disabled  bool   `yaml:"disabled" json:"disabled,omitempty"`
}

type ModelCfg struct {
	Provider string `yaml:"provider" json:"provider"`
	ModelID  string `yaml:"model_id" json:"model_id"`
	Plan     string `yaml:"plan,omitempty" json:"plan,omitempty"`
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
