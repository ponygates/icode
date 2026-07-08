package zhipu

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "zhipu"
	DefaultBase  = "https://open.bigmodel.cn/api/paas/v4"
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
			ID:              "glm-5",
			Name:            "GLM-5",
			Description:     "智谱旗舰模型，全球TOP4，Coding能力逼近Claude Opus，擅长复杂系统工程",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 32768,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
					InputPrice:  4.0,
					OutputPrice: 4.0,
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
			ID:              "glm-4-flash",
			Name:            "GLM-4 Flash",
			Description:     "智谱高速免费模型，2M tokens/日免费，适合日常编程辅助",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "免费计划",
					InputPrice:  0,
					OutputPrice: 0,
					Currency:    "CNY",
					FreeTier: &types.FreeTier{
						DailyTokens:   2000000,
						DailyRequests: 2000,
					},
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
