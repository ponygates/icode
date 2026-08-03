package huawei

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "huawei"
	DefaultBase  = "https://maas-console.huawei.com/api/v1"
)

func New(apiKey, apiBase string) types.Provider {
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name: ProviderName, DefaultBase: DefaultBase, TimeoutSec: 120,
	}, apiKey, apiBase, DefaultModels())
}

func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "pangu-5.0-pro",
			Name:            "盘古 5.0 Pro",
			Description:     "华为盘古大模型旗舰版(2026.6 WAIC)，专注行业场景，代码理解与安全护栏",
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
			ID:              "pangu-5.0-code",
			Name:            "盘古 5.0 Code",
			Description:     "华为盘古代码专用模型(2026.6)，编程场景深度优化，支持安全护栏审核",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
				Reasoning: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "pangu-5.0-lite",
			Name:            "盘古 5.0 Lite",
			Description:     "华为盘古轻量模型，高性价比，适合日常编程与对话场景",
			Provider:        ProviderName,
			ContextWindow:   65536,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
		{
			ID:              "qwen2.5-72b-instruct",
			Name:            "Qwen2.5-72B (华为云)",
			Description:     "华为云ModelArts代理通义千问72B模型",
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
			ID:              "deepseek-v3-0324",
			Name:            "DeepSeek-V3 (华为云)",
			Description:     "华为云ModelArts代理DeepSeek-V3模型",
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
			ID:              "glm-4-0520",
			Name:            "GLM-4 (华为云)",
			Description:     "华为云ModelArts代理智谱GLM-4模型",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
			UpdatedAt: time.Now(),
		},
	}
}
