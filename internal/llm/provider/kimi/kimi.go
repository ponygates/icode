package kimi

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "kimi"
	DefaultBase  = "https://api.moonshot.cn/v1"
)

func New(apiKey, apiBase string) types.Provider {
	if apiBase == "" {
		apiBase = DefaultBase
	}

	return openai_compat.New(openai_compat.Config{
		Name:       ProviderName,
		APIBase:    apiBase,
		APIKey:     apiKey,
		TimeoutSec: 120,
		Models:     DefaultModels(),
	})
}

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "kimi-k2.7-code",
			Name:            "Kimi K2.7 Code",
			Description:     "Kimi 编程旗舰模型，SWE-Bench Pro 领先，256K 超长上下文，代码生成卓越",
			Provider:        ProviderName,
			ContextWindow:   262144,
			MaxOutputTokens: 32768,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "编程计划",
					InputPrice:  12.0,
					OutputPrice: 12.0,
					Currency:    "CNY",
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
				Reasoning: true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "kimi-k2.6",
			Name:            "Kimi K2.6",
			Description:     "Kimi 通用旗舰模型，256K 上下文，超长文档理解，智能对话",
			Provider:        ProviderName,
			ContextWindow:   262144,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "通用Token计划",
					InputPrice:  8.0,
					OutputPrice: 8.0,
					Currency:    "CNY",
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
	}
}
