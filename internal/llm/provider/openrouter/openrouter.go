package openrouter

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "openrouter"
	DefaultBase  = "https://openrouter.ai/api/v1"
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
			ID:              "auto",
			Name:            "OpenRouter Auto",
			Description:     "自动选择最优模型路由，平衡质量与成本",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "auto-plan",
					Description: "智能路由计划，根据任务复杂度自动选模型",
					InputPrice:  0.0,
					OutputPrice: 0.0,
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
			ID:              "free",
			Name:            "OpenRouter Free",
			Description:     "免费模型聚合，速率有限但零成本",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{
					Name:        "free-plan",
					Description: "免费计划，每日有限额",
					InputPrice:  0,
					OutputPrice: 0,
					FreeTier: &types.FreeTier{
						DailyTokens:   200000,
						DailyRequests: 200,
					},
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "anthropic/claude-sonnet-4",
			Name:            "Claude Sonnet 4 (via OpenRouter)",
			Description:     "Anthropic 最新编程模型，代码生成能力卓越",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "编程计划",
					InputPrice:  3.0,
					OutputPrice: 15.0,
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
			ID:              "openai/gpt-4o",
			Name:            "GPT-4o (via OpenRouter)",
			Description:     "OpenAI 多模态旗舰模型",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "标准Token计划",
					InputPrice:  2.5,
					OutputPrice: 10.0,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "google/gemini-2.0-flash-exp:free",
			Name:            "Gemini 2.0 Flash (Free)",
			Description:     "Google 免费模型，速度快",
			Provider:        ProviderName,
			ContextWindow:   1048576,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "free-plan",
					Description: "免费计划",
					InputPrice:  0,
					OutputPrice: 0,
					FreeTier: &types.FreeTier{
						DailyTokens:   200000,
						DailyRequests: 200,
					},
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
