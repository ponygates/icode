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
			ID:              "pangu-4-pro",
			Name:            "盘古 4.0 Pro",
			Description:     "华为盘古大模型专业版，代码生成与理解能力强",
			Provider:        ProviderName,
			ContextWindow:   32768,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
					InputPrice:  5.0,
					OutputPrice: 5.0,
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
			ID:              "pangu-4-code",
			Name:            "盘古 4.0 Code",
			Description:     "华为盘古代码专用模型，专注于编程场景",
			Provider:        ProviderName,
			ContextWindow:   32768,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "code-plan",
					Description: "代码专用计划",
					InputPrice:  3.0,
					OutputPrice: 3.0,
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
