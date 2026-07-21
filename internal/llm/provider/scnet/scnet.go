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
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name: ProviderName, DefaultBase: DefaultBase, TimeoutSec: 180,
	}, apiKey, apiBase, DefaultModels())
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
					Name:        "codingplan",
					Description: "编程计划（codingplan，国有算力补贴低价）",
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
			ID:              "MiniMax-m2.5",
			Name:            "MiniMax M2.5",
			Description:     "MiniMax M2.5 大模型，经国家超算中心 SCNET 提供，通用编程与对话，国产超算算力支撑",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "codingplan",
					Description: "编程计划（codingplan，国有算力补贴低价）",
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
			Description:     "国家超算中心代码专用模型，针对大型工程编程任务深度优化，256K 上下文，支持推理",
			Provider:        ProviderName,
			ContextWindow:   262144,
			MaxOutputTokens: 32768,
			Plans: []types.TokenPlan{
				{
					Name:        "tokenplan",
					Description: "令牌计划（tokenplan，国有算力补贴）",
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
		{
			ID:              "deepseek-v4-flash",
			Name:            "DeepSeek V4 Flash",
			Description:     "DeepSeek V4 Flash，经国家超算中心 SCNET 提供，高性价比编程与对话模型",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{
					Name:        "tokenplan",
					Description: "令牌计划（tokenplan，国有算力补贴）",
					InputPrice:  0.12,
					OutputPrice: 0.25,
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
			ID:              "deepseek-v4-pro",
			Name:            "DeepSeek V4 Pro",
			Description:     "DeepSeek V4 Pro，经国家超算中心 SCNET 提供，旗舰推理模型，适合复杂工程任务",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "tokenplan",
					Description: "令牌计划（tokenplan，国有算力补贴）",
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
