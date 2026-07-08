package tencent

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "tencent"
	DefaultBase  = "https://api.hunyuan.cloud.tencent.com/v1"
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
			ID:              "hunyuan-pro",
			Name:            "混元 Pro",
			Description:     "腾讯混元大模型旗舰版，多轮对话与代码生成能力均衡",
			Provider:        ProviderName,
			ContextWindow:   32000,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
					InputPrice:  0.0,
					OutputPrice: 0.0,
					FreeTier: &types.FreeTier{
						DailyTokens:   10000000,
						DailyRequests: 100,
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
		{
			ID:              "hunyuan-lite",
			Name:            "混元 Lite",
			Description:     "腾讯混元轻量版，响应快速，适合日常编程辅助",
			Provider:        ProviderName,
			ContextWindow:   32000,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "轻量Token计划（免费）",
					InputPrice:  0.0,
					OutputPrice: 0.0,
					FreeTier: &types.FreeTier{
						DailyTokens:   10000000,
						DailyRequests: 100,
					},
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
	}
}
