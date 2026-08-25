// Package sensenova implements the SenseNova Provider (商汤日日新).
//
// Models available via the SenseNova API (OpenAI-compatible):
//   - sensenova-u1             SenseNova U1 旗舰，长上下文复杂任务
//   - sensenova-u1-fast        SenseNova U1 Fast，速度与质量均衡
//   - sensenova-6.8            SenseNova 6.8 旗舰
//   - sensenova-6.8-flash-lite SenseNova 6.8 Flash Lite，低成本快速响应
//   - sensenova-6.7-flash-lite SenseNova 6.7 Flash Lite（公测入口常用）
//
// NOTE: Agnes (agnes-ai.com) is a SEPARATE provider — see the "agnes" package.
// The API base URL is configurable in ~/.icode/config.yaml under
// providers.sensenova.api_base. The default is the public SenseNova gateway.
package sensenova

import (
	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "sensenova"
	DefaultBase  = "https://api.sensenova.cn/v1"
)

// New creates a SenseNova provider (OpenAI-compatible API, prefix-cache enabled).
func New(apiKey, apiBase string) types.Provider {
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name: ProviderName, DefaultBase: DefaultBase,
		TimeoutSec: 120, CacheSupport: true,
	}, apiKey, apiBase, DefaultModels())
}

// DefaultModels returns the current SenseNova model list. Users can override
// or add models via config (providers.sensenova.api_base, models.*).
func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "sensenova-u1",
			Name:            "SenseNova U1",
			Description:     "SenseNova U1 旗舰，长上下文复杂任务",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
		},
		{
			ID:              "sensenova-u1-fast",
			Name:            "SenseNova U1 Fast",
			Description:     "SenseNova U1 Fast，速度与质量均衡",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
		},
		{
			ID:              "sensenova-6.8",
			Name:            "SenseNova 6.8",
			Description:     "SenseNova 6.8 旗舰模型",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
		},
		{
			ID:              "sensenova-6.8-flash-lite",
			Name:            "SenseNova 6.8 Flash Lite",
			Description:     "SenseNova 6.8 Flash Lite，低成本快速响应",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
		},
		{
			ID:              "sensenova-6.7-flash-lite",
			Name:            "SenseNova 6.7 Flash Lite",
			Description:     "SenseNova 6.7 Flash Lite（公测入口常用）",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
		},
	}
}
