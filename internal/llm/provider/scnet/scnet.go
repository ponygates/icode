// Package scnet implements the SCNET (国家超级计算中心) Provider.
// SCNET provides access to large-scale AI models hosted on China's national
// supercomputing infrastructure via an OpenAI-compatible API.
package scnet

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "scnet"
	DefaultBase  = "https://api.scnet.cn/v1"
)

func New(apiKey, apiBase string) types.Provider {
	if apiBase == "" {
		apiBase = DefaultBase
	}

	return openai_compat.New(openai_compat.Config{
		Name:       ProviderName,
		APIBase:    apiBase,
		APIKey:     apiKey,
		TimeoutSec: 180,
		Models:     DefaultModels(),
	})
}

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "scnet-chat",
			Name:            "SCNET Chat",
			Description:     "国家超算中心通用对话模型，中国自主高性能算力支撑",
			Provider:        ProviderName,
			ContextWindow:   32768,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划（国有算力补贴）",
					InputPrice:  0.1,
					OutputPrice: 0.2,
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
			ID:              "scnet-code",
			Name:            "SCNET Code",
			Description:     "国家超算中心代码专用模型，针对编程任务优化",
			Provider:        ProviderName,
			ContextWindow:   65536,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "code-plan",
					Description: "代码专用计划",
					InputPrice:  0.15,
					OutputPrice: 0.3,
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
