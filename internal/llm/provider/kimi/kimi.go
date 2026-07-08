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
			ID:              "moonshot-v1-8k",
			Name:            "Moonshot v1 8K",
			Description:     "Kimi 标准模型，8K 上下文窗口",
			Provider:        ProviderName,
			ContextWindow:   8192,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
					InputPrice:  12.0,
					OutputPrice: 12.0,
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "moonshot-v1-128k",
			Name:            "Moonshot v1 128K",
			Description:     "Kimi 长上下文模型，128K 窗口",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "长上下文计划",
					InputPrice:  60.0,
					OutputPrice: 60.0,
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
