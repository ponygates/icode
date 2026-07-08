// Package scnet implements the SCNET (国家超级计算中心) Provider.
// SCNET provides access to large-scale AI models hosted on China's national
// supercomputing infrastructure via an OpenAI-compatible API.
//
// Latest models (July 2026): upgraded context windows and improved capabilities.
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
			Name:            "SCNET Chat Pro",
			Description:     "国家超算中心通用对话模型，国产高性能算力支撑，128K 长上下文",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "coding-plan",
					Description: "标准编程计划（国有算力补贴低价）",
					InputPrice:  0.08,
					OutputPrice: 0.15,
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
			ID:              "scnet-code",
			Name:            "SCNET Code Pro",
			Description:     "国家超算中心代码专用模型，针对大型工程编程任务深度优化，256K 上下文",
			Provider:        ProviderName,
			ContextWindow:   262144,
			MaxOutputTokens: 32768,
			Plans: []types.TokenPlan{
				{
					Name:        "code-plan",
					Description: "代码专用计划（国有算力补贴）",
					InputPrice:  0.12,
					OutputPrice: 0.25,
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
