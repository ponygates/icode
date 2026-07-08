// Package volcengine implements the Volcengine (火山方舟) Ark Provider.
// Volcengine provides Doubao (豆包) models via OpenAI-compatible API.
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
			ID:              "doubao-pro-32k",
			Name:            "豆包 Pro 32K",
			Description:     "字节跳动豆包大模型旗舰版，编程能力优秀",
			Provider:        ProviderName,
			ContextWindow:   32768,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划",
					InputPrice:  0.8,
					OutputPrice: 2.0,
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
			ID:              "doubao-lite-32k",
			Name:            "豆包 Lite 32K",
			Description:     "豆包轻量版，速度快，成本低",
			Provider:        ProviderName,
			ContextWindow:   32768,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{
					Name:        "token-plan",
					Description: "轻量Token计划",
					InputPrice:  0.3,
					OutputPrice: 0.6,
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
