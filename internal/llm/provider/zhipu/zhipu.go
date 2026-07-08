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
			ID:              "glm-4-plus",
			Name:            "GLM-4-Plus",
			Description:     "智谱旗舰模型，综合能力最强",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
					InputPrice:  50.0,
					OutputPrice: 50.0,
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
			ID:              "glm-4-flash",
			Name:            "GLM-4-Flash",
			Description:     "智谱轻量模型，速度快，免费使用",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "免费计划",
					InputPrice:  0,
					OutputPrice: 0,
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
