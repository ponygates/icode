package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Privacy    PrivacyConfig    `yaml:"privacy"`
	Provider   ProviderConfig   `yaml:"provider"`
	Cache      CacheConfig      `yaml:"cache"`
	Permission PermissionConfig `yaml:"permission"`
	Schedule   ScheduleConfig   `yaml:"schedule"`
}

type PrivacyConfig struct {
	Mode       string `yaml:"mode"`
	AuditLog   bool   `yaml:"audit_log"`
	SanitizeEnv bool  `yaml:"sanitize_env"`
}

type ProviderConfig struct {
	Default  string              `yaml:"default"`
	Fallback string              `yaml:"fallback"`
	Settings map[string]ProviderSetting `yaml:"settings,omitempty"`
}

type ProviderSetting struct {
	APIKey      string `yaml:"api_key,omitempty"`
	CodingPlan  string `yaml:"coding_plan,omitempty"`
	PreferFree  bool   `yaml:"prefer_free,omitempty"`
}

type CacheConfig struct {
	Enabled         bool   `yaml:"enabled"`
	Strategy        string `yaml:"strategy"`
	MaxCacheSizeMB  int    `yaml:"max_cache_size_mb"`
}

type PermissionConfig struct {
	Mode           string   `yaml:"mode"`
	ReadOnlyDirs   []string `yaml:"read_only_dirs"`
	DenyDirs       []string `yaml:"deny_dirs"`
	BashDenyCmds   []string `yaml:"bash_deny_commands"`
	MaxTurns       int      `yaml:"max_turns"`
}

type ScheduleConfig struct {
	Enabled bool          `yaml:"enabled"`
	Peaks   []PeakHours   `yaml:"peak_hours"`
	Low     LowHours      `yaml:"low_hours"`
	Normal  NormalHours   `yaml:"normal_hours"`
}

type PeakHours struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
	Days  []string `yaml:"days"`
	Mode  string `yaml:"mode"`
}

type LowHours struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
	Mode  string `yaml:"mode"`
}

type NormalHours struct {
	Mode string `yaml:"mode"`
}

func DefaultConfig() *Config {
	return &Config{
		Privacy: PrivacyConfig{
			Mode:        "smart",
			AuditLog:    true,
			SanitizeEnv: true,
		},
		Provider: ProviderConfig{
			Default:  "deepseek/deepseek-v4-flash",
			Fallback: "openrouter/auto",
			Settings: make(map[string]ProviderSetting),
		},
		Cache: CacheConfig{
			Enabled:        true,
			Strategy:       "append-only",
			MaxCacheSizeMB: 500,
		},
		Permission: PermissionConfig{
			Mode:     "ask",
			MaxTurns: 200,
		},
		Schedule: ScheduleConfig{
			Enabled: false,
			Normal:  NormalHours{Mode: "balanced"},
		},
	}
}

func Load() (*Config, error) {
	cfg := DefaultConfig()

	paths := []string{
		filepath.Join(os.Getenv("HOME"), ".icode", "config.yaml"),
		filepath.Join(os.Getenv("USERPROFILE"), ".icode", "config.yaml"),
		".icode.yaml",
		"icode.yaml",
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
		break
	}

	return cfg, nil
}
