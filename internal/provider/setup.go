package provider

import (
	"os"
	"strings"

	"github.com/ponygates/icode/internal/config"
)

type ProviderKey struct {
	Name  string
	Model string
}

func ParseModelKey(key string) ProviderKey {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) == 2 {
		return ProviderKey{Name: parts[0], Model: parts[1]}
	}
	return ProviderKey{Name: "openai", Model: parts[0]}
}

func InitRegistry(cfg *config.Config) *Registry {
	reg := NewRegistry()

	settings := cfg.Provider.Settings
	if settings == nil {
		settings = make(map[string]config.ProviderSetting)
	}

	// OpenAI
	if s, ok := settings["openai"]; ok {
		apiKey := resolveEnv(s.APIKey)
		if apiKey != "" {
			reg.Register(NewOpenAIProvider(OpenAIProviderConfig{
				Name:    "openai",
				BaseURL: "https://api.openai.com/v1",
				APIKey:  apiKey,
				Models:  []string{"gpt-5.5", "gpt-5.4", "o3", "o4-mini"},
			}))
		}
	}

	// DeepSeek (OpenAI-compatible)
	if s, ok := settings["deepseek"]; ok {
		apiKey := resolveEnv(s.APIKey)
		if apiKey != "" {
			reg.Register(NewOpenAIProvider(OpenAIProviderConfig{
				Name:    "deepseek",
				BaseURL: "https://api.deepseek.com",
				APIKey:  apiKey,
				Models:  []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-chat"},
			}))
		}
	}

	// Anthropic
	if s, ok := settings["anthropic"]; ok {
		apiKey := resolveEnv(s.APIKey)
		if apiKey != "" {
			reg.Register(NewAnthropicProvider(apiKey, []string{"claude-sonnet-4.6", "claude-opus-4.6"}))
		}
	}

	// Zhipu (GLM)
	if s, ok := settings["zhipu"]; ok {
		apiKey := resolveEnv(s.APIKey)
		if apiKey != "" {
			models := []string{"glm-4.7-flash", "glm-4.7", "glm-5", "glm-5.1"}
			if s.CodingPlan != "" {
				models = append(models, "glm-4.7-flash"+s.CodingPlan)
			}
			reg.Register(NewZhipuProvider(apiKey, models))
		}
	}

	// SiliconFlow (OpenAI-compatible)
	if s, ok := settings["siliconflow"]; ok {
		apiKey := resolveEnv(s.APIKey)
		if apiKey != "" {
			models := []string{"Qwen2.5-72B", "DeepSeek-V3", "GLM-4-9B"}
			reg.Register(NewOpenAIProvider(OpenAIProviderConfig{
				Name:    "siliconflow",
				BaseURL: "https://api.siliconflow.cn/v1",
				APIKey:  apiKey,
				Models:  models,
			}))
		}
	}

	// NVIDIA NIM (OpenAI-compatible)
	if s, ok := settings["nvidia"]; ok {
		apiKey := resolveEnv(s.APIKey)
		if apiKey != "" {
			reg.Register(NewOpenAIProvider(OpenAIProviderConfig{
				Name:    "nvidia",
				BaseURL: "https://integrate.api.nvidia.com/v1",
				APIKey:  apiKey,
				Models:  []string{"qwen3-coder-480b", "minimax-m2.7", "deepseek-v4-pro", "nemotron-3"},
			}))
		}
	}

	// OpenRouter (OpenAI-compatible)
	if s, ok := settings["openrouter"]; ok {
		apiKey := resolveEnv(s.APIKey)
		if apiKey != "" {
			reg.Register(NewOpenAIProvider(OpenAIProviderConfig{
				Name:    "openrouter",
				BaseURL: "https://openrouter.ai/api/v1",
				APIKey:  apiKey,
				Headers: map[string]string{
					"HTTP-Referer": "https://github.com/ponygates/icode",
					"X-Title":      "iCode",
				},
				Models: []string{"auto", "free"},
			}))
		}
	}

	return reg
}

func resolveEnv(val string) string {
	if strings.HasPrefix(val, "${") && strings.HasSuffix(val, "}") {
		envName := strings.TrimSuffix(strings.TrimPrefix(val, "${"), "}")
		return os.Getenv(envName)
	}
	return val
}
