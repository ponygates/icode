// Package volcengine implements the Volcengine (火山方舟) Ark Provider.
// Volcengine provides Doubao Seed (豆包) models via OpenAI-compatible API.
//
// Latest models (July 2026): Doubao Seed 2.1 series (released June 2026).
package volcengine

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "volcengine"
	DefaultBase  = "https://ark.cn-beijing.volces.com/api/v3"
)

func New(apiKey, apiBase string) types.Provider {
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name: ProviderName, DefaultBase: DefaultBase, TimeoutSec: 120,
	}, apiKey, apiBase, DefaultModels())
}

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "doubao-seed-2.1-pro",
			Name:            "豆包 Seed 2.1 Pro",
			Description:     "字节跳动旗舰大模型(2026.6)，智能体与代码工程能力领先，多模态理解",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
					InputPrice:  0.8,
					OutputPrice: 2.0,
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
			ID:              "doubao-seed-2.1-turbo",
			Name:            "豆包 Seed 2.1 Turbo",
			Description:     "豆包轻量版(2026.6)，速度快成本低，适合高频日常编程场景",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "轻量Token计划",
					InputPrice:  0.3,
					OutputPrice: 0.6,
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
