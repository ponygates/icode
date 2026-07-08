// Package deepseek implements the DeepSeek Provider.
// DeepSeek uses the standard OpenAI-compatible API with prefix-cache support.
package deepseek

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "deepseek"
	DefaultBase  = "https://api.deepseek.com/v1"
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
		TimeoutSec:   120,
		CacheSupport: true,
		Models:       DefaultModels(),
	})
}

// DefaultModels returns the standard DeepSeek model list.
func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:             "deepseek-chat",
			Name:           "DeepSeek-V3",
			Description:    "通用对话模型，适合代码生成与日常编程",
			Provider:       ProviderName,
			ContextWindow:  65536,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
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
		{
			ID:             "deepseek-reasoner",
			Name:           "DeepSeek-R1",
			Description:    "深度推理模型，适合复杂逻辑和架构设计",
			Provider:       ProviderName,
			ContextWindow:  65536,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "reasoning-plan",
					Description: "推理增强计划",
					InputPrice:  0.55,
					OutputPrice: 2.19,
					CachePrice:  0.14,
				},
			},
			Capabilities: types.ModelCap{
				Tools:      true,
				Streaming:  true,
				Reasoning:  true,
			},
			UpdatedAt: time.Now(),
		},
	}
}
