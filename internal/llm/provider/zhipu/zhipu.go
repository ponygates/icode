package zhipu

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "zhipu"
	DefaultBase  = "https://open.bigmodel.cn/api/paas/v4"
)

func New(apiKey, apiBase string) types.Provider {
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name: ProviderName, DefaultBase: DefaultBase, TimeoutSec: 120,
	}, apiKey, apiBase, DefaultModels())
}

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "glm-5.2",
			Name:            "GLM-5.2",
			Description:     "智谱最新旗舰模型，1M无损上下文，Coding能力开源SOTA，从代码生成走向工程交付",
			Provider:        ProviderName,
			ContextWindow:   1048576,
			MaxOutputTokens: 131072,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
				Reasoning: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "glm-5.1",
			Name:            "GLM-5.1",
			Description:     "Coding能力对齐Claude Opus 4.6，长程任务显著提升，可自主工作长达8小时",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 131072,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
				Reasoning: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "glm-5",
			Name:            "GLM-5",
			Description:     "智谱旗舰模型，编程能力对齐Claude Opus 4.5，擅长Agentic长程规划与执行",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 131072,
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
			ID:              "glm-5-turbo",
			Name:            "GLM-5 Turbo",
			Description:     "长任务核心需求专项优化，复杂长任务执行连续性好",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 131072,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "glm-4.7",
			Name:            "GLM-4.7",
			Description:     "通用对话推理智能体全面升级，编程更强更稳审美更好",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 131072,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "glm-4.7-flashx",
			Name:            "GLM-4.7 FlashX",
			Description:     "轻量高速，小尺寸强能力，适用中文写作翻译角色扮演等通用场景",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 131072,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "glm-4.6",
			Name:            "GLM-4.6",
			Description:     "上下文提升至200K，擅长高级编码复杂推理与工具调用",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 131072,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "glm-4.5-air",
			Name:            "GLM-4.5 Air",
			Description:     "高性价比轻量模型，推理编码与智能体任务表现稳定",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 98304,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "glm-4-long",
			Name:            "GLM-4 Long",
			Description:     "支持1M上下文长度，处理超长文本和记忆型任务",
			Provider:        ProviderName,
			ContextWindow:   1048576,
			MaxOutputTokens: 4096,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "glm-4.7-flash",
			Name:            "GLM-4.7 Flash",
			Description:     "免费模型，延续GLM-4.7基座通用能力，普惠体验",
			Provider:        ProviderName,
			ContextWindow:   200000,
			MaxOutputTokens: 131072,
			Plans: []types.TokenPlan{
				{
					Name:        "free-plan",
					Description: "免费计划",
					InputPrice:  0,
					OutputPrice: 0,
					Currency:    "CNY",
				},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "glm-4-flash-250414",
			Name:            "GLM-4 Flash",
			Description:     "免费高速模型，支持长上下文处理，适合多语言理解与工具调用场景",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "free-plan",
					Description: "免费计划",
					InputPrice:  0,
					OutputPrice: 0,
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
			ID:              "codegeex-4",
			Name:            "CodeGeeX-4",
			Description:     "智谱代码补全模型，适用于代码自动补全与开发辅助",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 32768,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
	}
}
