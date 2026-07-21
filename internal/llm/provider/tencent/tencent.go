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
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name: ProviderName, DefaultBase: DefaultBase, TimeoutSec: 120,
	}, apiKey, apiBase, DefaultModels())
}

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "hunyuan-turbos",
			Name:            "混元 TurboS",
			Description:     "腾讯混元旗舰快速模型，超大上下文，代码生成能力强，每日免费额度丰富",
			Provider:        ProviderName,
			ContextWindow:   256000,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划（丰富免费额度）",
					InputPrice:  0.0,
					OutputPrice: 0.0,
					Currency:    "CNY",
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
			ID:              "hunyuan-t1",
			Name:            "混元 T1",
			Description:     "腾讯混元深度推理模型，长思维链+检索增强，擅长复杂逻辑与架构设计",
			Provider:        ProviderName,
			ContextWindow:   256000,
			MaxOutputTokens: 32768,
			Plans: []types.TokenPlan{
				{
					Name:        "reasoning-plan",
					Description: "推理增强计划",
					InputPrice:  1.0,
					OutputPrice: 4.0,
					Currency:    "CNY",
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				Reasoning: true,
			},
			UpdatedAt: time.Now(),
		},
	}
}
