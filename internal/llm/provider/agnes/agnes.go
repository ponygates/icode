// Package agnes implements the Agnes AI provider — an independent, free
// omni-modal AI API from a "Global Top 10" AI lab. Official website:
// https://agnes-ai.cn (国内官网，维护中时联系 support@agnes-ai.com)；
// international site: https://agnes-ai.com. Text, image, and video models are
// offered free via an OpenAI-compatible endpoint.
//
// Models (text/chat, verified on agnes-ai.com 2026-08):
//   - agnes-2.5-flash   主力文本模型：代码生成、复杂推理、agentic coding
//   - agnes-2.0-flash   上一代快速模型，日常对话与轻量任务
//   - agnes-large       通用对话模型（较早系列，8k 上下文）
//
// The API base URL is configurable in ~/.icode/config.yaml under
// providers.agnes.api_base. Default: https://api.agnes-ai.cn/v1 (CN gateway).
package agnes

import (
	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "agnes"
	DefaultBase  = "https://api.agnes-ai.cn/v1"
)

// New creates an Agnes AI provider (OpenAI-compatible API).
func New(apiKey, apiBase string) types.Provider {
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name: ProviderName, DefaultBase: DefaultBase,
		TimeoutSec: 120, CacheSupport: true,
	}, apiKey, apiBase, DefaultModels())
}

// DefaultModels returns the current Agnes model list. Users can override or
// add models via config (providers.agnes.api_base, models.*).
func DefaultModels() []types.ModelInfo {
	return []types.ModelInfo{
		{
			ID:              "agnes-2.5-flash",
			Name:            "Agnes 2.5 Flash",
			Description:     "Agnes 主力文本模型：代码生成、复杂推理与 agentic coding",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
		},
		{
			ID:              "agnes-2.0-flash",
			Name:            "Agnes 2.0 Flash",
			Description:     "Agnes 快速模型：日常对话与轻量任务",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
		},
		{
			ID:              "agnes-large",
			Name:            "Agnes Large",
			Description:     "Agnes 通用对话模型（较早系列）",
			Provider:        ProviderName,
			ContextWindow:   8192,
			MaxOutputTokens: 4096,
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
			},
		},
	}
}
