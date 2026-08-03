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
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name: ProviderName, DefaultBase: DefaultBase, TimeoutSec: 120,
	}, apiKey, apiBase, DefaultModels())
}

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "kimi-k3",
			Name:            "Kimi K3",
			Description:     "Kimi最强旗舰模型，2.8万亿参数，原生视觉理解，1M上下文窗口，面向软件工程与深度推理",
			Provider:        ProviderName,
			ContextWindow:   1048576,
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
			ID:              "kimi-k2.7-code",
			Name:            "Kimi K2.7 Code",
			Description:     "Kimi编程模型，256K上下文，长上下文指令遵循，编程任务成功率高",
			Provider:        ProviderName,
			ContextWindow:   262144,
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
			ID:              "kimi-k2.7-code-highspeed",
			Name:            "Kimi K2.7 Code Highspeed",
			Description:     "Kimi K2.7 Code高速版，输出约180 Tokens/s，短上下文可达260 Tokens/s",
			Provider:        ProviderName,
			ContextWindow:   262144,
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
			ID:              "kimi-k2.6",
			Name:            "Kimi K2.6",
			Description:     "Kimi通用模型，256K上下文，支持视觉/思考/Agent任务",
			Provider:        ProviderName,
			ContextWindow:   262144,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "kimi-k2.5",
			Name:            "Kimi K2.5",
			Description:     "Kimi开源SoTA模型，Agent/代码/视觉理解领先，256K上下文(8月31日下线)",
			Provider:        ProviderName,
			ContextWindow:   262144,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "moonshot-v1-128k",
			Name:            "Moonshot V1 128K",
			Description:     "Moonshot V1长上下文版，128K上下文窗口(8月31日下线)",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "moonshot-v1-32k",
			Name:            "Moonshot V1 32K",
			Description:     "Moonshot V1标准版，32K上下文窗口(8月31日下线)",
			Provider:        ProviderName,
			ContextWindow:   32768,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "moonshot-v1-8k",
			Name:            "Moonshot V1 8K",
			Description:     "Moonshot V1基础版，8K上下文窗口(8月31日下线)",
			Provider:        ProviderName,
			ContextWindow:   8192,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "moonshot-v1-128k-vision-preview",
			Name:            "Moonshot V1 128K Vision",
			Description:     "Moonshot V1视觉模型128K版，理解图片内容并输出文本(8月31日下线)",
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
			ID:              "moonshot-v1-32k-vision-preview",
			Name:            "Moonshot V1 32K Vision",
			Description:     "Moonshot V1视觉模型32K版(8月31日下线)",
			Provider:        ProviderName,
			ContextWindow:   32768,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
		{
			ID:              "moonshot-v1-8k-vision-preview",
			Name:            "Moonshot V1 8K Vision",
			Description:     "Moonshot V1视觉模型8K版(8月31日下线)",
			Provider:        ProviderName,
			ContextWindow:   8192,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			SupportsVision: true,
			UpdatedAt:      time.Now(),
		},
	}
}
