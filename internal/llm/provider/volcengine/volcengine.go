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
			ID:              "doubao-seed-evolving",
			Name:            "豆包 Seed Evolving",
			Description:     "字节跳动最新自进化模型(2026.8)，持续学习进化，能力随时间提升",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
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
			ID:              "doubao-seed-2.1-pro-32k",
			Name:            "豆包 Seed 2.1 Pro 32K",
			Description:     "字节跳动旗舰大模型32K输出版(2026.6)，智能体与代码工程能力领先，多模态理解",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 32768,
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
			ID:              "doubao-seed-2.1-pro",
			Name:            "豆包 Seed 2.1 Pro",
			Description:     "字节跳动旗舰大模型(2026.6)，智能体与代码工程能力领先，多模态理解",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
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
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "doubao-seed-2.1-lite",
			Name:            "豆包 Seed 2.1 Lite",
			Description:     "豆包超轻量版，极致性价比，适合简单对话和快速补全",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "doubao-pro-32k",
			Name:            "豆包 Pro 32K",
			Description:     "豆包Pro模型32K输出版，通用对话与代码生成",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 32768,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "doubao-pro-128k",
			Name:            "豆包 Pro 128K",
			Description:     "豆包Pro模型128K上下文版，长文档理解与生成",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
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
			Description:     "豆包轻量模型32K输出版，高性价比日常任务",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 32768,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "doubao-lite-128k",
			Name:            "豆包 Lite 128K",
			Description:     "豆包轻量模型128K上下文版，高性价比长文本处理",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
	}
}
