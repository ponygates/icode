package tencent

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "tencent"
	DefaultBase  = "https://api.hunyuan.cloud.tencent.com/v1"
)

func New(apiKey, apiBase string) types.Provider {
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name: ProviderName, DefaultBase: DefaultBase, TimeoutSec: 120,
	}, apiKey, apiBase, DefaultModels())
}

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "hunyuan-turbos",
			Name:            "混元 TurboS",
			Description:     "腾讯混元旗舰快速模型，超大上下文，代码生成能力强，每日免费额度丰富",
			Provider:        ProviderName,
			ContextWindow:   256000,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{
					Name:        "free-plan",
					Description: "免费计划（10M tokens/日）",
					InputPrice:  0,
					OutputPrice: 0,
					Currency:    "CNY",
					FreeTier: &types.FreeTier{
						DailyTokens:   10000000,
						DailyRequests: 100,
					},
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
			ID:              "hunyuan-t1",
			Name:            "混元 T1",
			Description:     "腾讯混元深度推理模型，长思维链+检索增强，擅长复杂逻辑与架构设计",
			Provider:        ProviderName,
			ContextWindow:   256000,
			MaxOutputTokens: 32768,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				Reasoning: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "hunyuan-a13b",
			Name:            "混元 A13B",
			Description:     "混元MoE混合推理模型，80B总参/13B激活，快慢思考切换，224K上下文，数学/科学/Agent大幅提升",
			Provider:        ProviderName,
			ContextWindow:   229376,
			MaxOutputTokens: 32768,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
				Reasoning: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "hunyuan-pro",
			Name:            "混元 Pro",
			Description:     "腾讯混元Pro模型，通用能力强，适合复杂对话与代码生成",
			Provider:        ProviderName,
			ContextWindow:   256000,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "hunyuan-standard",
			Name:            "混元 Standard",
			Description:     "腾讯混元标准模型，平衡性能与成本，适合日常编程与对话(建议升级至a13b)",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "hunyuan-lite",
			Name:            "混元 Lite",
			Description:     "腾讯混元轻量模型，极致性价比，适合简单任务与高并发场景",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "hunyuan-code",
			Name:            "混元 Code",
			Description:     "腾讯混元代码专用模型，编程场景深度优化",
			Provider:        ProviderName,
			ContextWindow:   256000,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "hunyuan-functioncall",
			Name:            "混元 FunctionCall",
			Description:     "腾讯混元工具调用专用模型，FunctionCalling能力优化",
			Provider:        ProviderName,
			ContextWindow:   256000,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "hunyuan-turbos-vision",
			Name:            "混元 TurboS Vision",
			Description:     "混元TurboS视觉理解模型，图片识别/分析/OCR/创作",
			Provider:        ProviderName,
			ContextWindow:   256000,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "hunyuan-t1-vision",
			Name:            "混元 T1 Vision",
			Description:     "混元T1视觉深度思考模型，视觉定位/OCR/图表/拍题解题/看图创作",
			Provider:        ProviderName,
			ContextWindow:   28672,
			MaxOutputTokens: 20480,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				Reasoning: true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "hunyuan-vision-1.5-instruct",
			Name:            "混元 Vision 1.5 Instruct",
			Description:     "混元最新图生文快思考模型，图像识别/分析推理全面提升",
			Provider:        ProviderName,
			ContextWindow:   24576,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
	}
}
