package huawei

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "huawei"
	DefaultBase  = "https://nlp-api.cloud.huawei.com/v1"
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
			ID:              "pangu-5.0-pro",
			Name:            "盘古 5.0 Pro",
			Description:     "华为盘古大模型旗舰版(2026.6 WAIC)，专注行业场景，代码理解与安全护栏",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
					InputPrice:  3.0,
					OutputPrice: 3.0,
					Currency:    "CNY",
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
			ID:              "pangu-5.0-code",
			Name:            "盘古 5.0 Code",
			Description:     "华为盘古代码专用模型(2026.6)，编程场景深度优化，支持安全护栏审核",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "code-plan",
					Description: "代码专用计划",
					InputPrice:  2.0,
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
			UpdatedAt: time.Now(),
		},
	}
}
