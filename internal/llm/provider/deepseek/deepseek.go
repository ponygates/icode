// Package deepseek implements the DeepSeek Provider.
// DeepSeek uses the standard OpenAI-compatible API with prefix-cache support.
//
// Latest models (July 2026):
//   - deepseek-v4-flash: 284B/13B active, 1M context, $0.14/$0.28 per 1M
//   - deepseek-v4-pro:   1.6T/49B active, 1M context, $0.44/$0.87 per 1M
//   - Prefix cache: auto-enabled, 1024 token minimum, ~98% discount on cache hits.
//   - Note: deepseek-chat/deepseek-reasoner aliases will stop working 2026-07-24.
package deepseek

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "deepseek"
	DefaultBase  = "https://api.deepseek.com"
)

// New creates a DeepSeek provider with prefix-cache support enabled.
func New(apiKey, apiBase string) types.Provider {
	if apiBase == "" {
		apiBase = DefaultBase
	}

	return openai_compat.New(openai_compat.Config{
		Name:         ProviderName,
		APIBase:      apiBase,
		APIKey:       apiKey,
		TimeoutSec:   180,
		CacheSupport: true,
		Models:       DefaultModels(),
	})
}

// DefaultModels returns the current DeepSeek model list (July 2026).
func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "deepseek-v4-flash",
			Name:            "DeepSeek V4 Flash",
			Description:     "旗舰轻量模型，284B 总参数 / 13B 激活，适合日常编程和快速响应",
			Provider:        ProviderName,
			ContextWindow:   1048576,
			MaxOutputTokens: 65536,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
					InputPrice:  0.14,
					OutputPrice: 0.28,
					CachePrice:  0.0028,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
				Reasoning: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "deepseek-v4-pro",
			Name:            "DeepSeek V4 Pro",
			Description:     "旗舰专业模型，1.6T 总参数 / 49B 激活，擅长复杂架构设计和深度推理",
			Provider:        ProviderName,
			ContextWindow:   1048576,
			MaxOutputTokens: 65536,
			Plans: []types.TokenPlan{
				{
					Name:        "reasoning-plan",
					Description: "推理增强计划",
					InputPrice:  0.44,
					OutputPrice: 0.87,
					CachePrice:  0.0036,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
				Reasoning: true,
			},
			UpdatedAt: time.Now(),
		},
		// Keep legacy aliases for backward compatibility (will be auto-routed by DeepSeek until 2026-07-24)
		{
			ID:              "deepseek-chat",
			Name:            "DeepSeek V3 (legacy → V4 Flash)",
			Description:     "旧版别名，已自动路由到 V4 Flash。请迁移到 deepseek-v4-flash",
			Provider:        ProviderName,
			ContextWindow:   65536,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "legacy-plan",
					Description: "旧版计划（请迁移）",
					InputPrice:  0.27,
					OutputPrice: 1.10,
					CachePrice:  0.07,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
	}
}
