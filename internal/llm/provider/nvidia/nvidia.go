// Package nvidia implements the NVIDIA NIM Provider.
//
// NVIDIA NIM (build.nvidia.com) exposes 100+ top open models through a single
// OpenAI-compatible endpoint at https://integrate.api.nvidia.com/v1. A single
// free API key (format nvapi-xxxx) unlocks every model below — no credit card,
// no per-model billing (all free tier). This provider registers the chat /
// instruct / code / reasoning / vision-language LLMs from that free catalog.
//
// Note: the NIM catalogue changes over time. The IDs below reflect the free
// endpoint catalogue as of mid-2026; validate against https://build.nvidia.com/models
// if a model 404s.
package nvidia

import (
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	ProviderName = "nvidia"
	DefaultBase  = "https://integrate.api.nvidia.com/v1"
)

// New creates an NVIDIA NIM provider (OpenAI-compatible, no prefix-cache API).
func New(apiKey, apiBase string) types.Provider {
	if apiBase == "" {
		apiBase = DefaultBase
	}
	return openai_compat.New(openai_compat.Config{
		Name:         ProviderName,
		APIBase:      apiBase,
		APIKey:       apiKey,
		TimeoutSec:   180,
		CacheSupport: false,
		Models:       DefaultModels(),
	})
}

// freePlan is the pricing plan for every NIM free-endpoint model ($0).
func freePlan(name, desc string) types.TokenPlan {
	return types.TokenPlan{
		Name:        name,
		Description: desc,
		InputPrice:  0,
		OutputPrice: 0,
		CachePrice:  0,
		Currency:    "USD",
		FreeTier: &types.FreeTier{
			DailyTokens:   1000000000,
			DailyRequests: 200000,
		},
	}
}

// DefaultModels returns the NVIDIA NIM free-endpoint chat model catalogue.
func DefaultModels() []types.ModelInfo {
	// Helper to keep the list below terse.
	mk := func(id, name, desc string, cw int, vision, reasoning bool) types.ModelInfo {
		return types.ModelInfo{
			ID:              id,
			Name:            name,
			Description:     desc,
			Provider:        ProviderName,
			ContextWindow:   cw,
			MaxOutputTokens: 4096,
			Plans:           []types.TokenPlan{freePlan("free", "NVIDIA NIM 免费额度")},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
				Reasoning: reasoning,
			},
			SupportsVision: vision,
			UpdatedAt:      time.Now(),
		}
	}

	return []types.ModelInfo{
		// ── Meta Llama ───────────────────────────────────────────────
		mk("meta/llama-3.1-8b-instruct", "Llama 3.1 8B Instruct", "Meta 轻量对话模型，8B 参数，适合快速本地级任务", 131072, false, false),
		mk("meta/llama-3.1-70b-instruct", "Llama 3.1 70B Instruct", "Meta 70B 对话模型，平衡质量与速度", 131072, false, false),
		mk("meta/llama-3.1-405b-instruct", "Llama 3.1 405B Instruct", "Meta 旗舰 405B 对话模型，逼近闭源能力", 32768, false, false),
		mk("meta/llama-3.2-1b-instruct", "Llama 3.2 1B Instruct", "Meta 超轻量 1B 模型，极低延迟", 131072, false, false),
		mk("meta/llama-3.2-3b-instruct", "Llama 3.2 3B Instruct", "Meta 3B 轻量对话模型", 131072, false, false),
		mk("meta/llama-3.2-11b-vision-instruct", "Llama 3.2 11B Vision", "Meta 11B 多模态视觉语言模型", 131072, true, false),
		mk("meta/llama-3.2-90b-vision-instruct", "Llama 3.2 90B Vision", "Meta 90B 多模态视觉语言模型", 131072, true, false),
		mk("meta/llama-3.3-70b-instruct", "Llama 3.3 70B Instruct", "Meta 3.3 代 70B，多语言能力突出", 131072, false, false),
		mk("meta/llama-4-scout-17b-16e-instruct", "Llama 4 Scout 17B", "Meta Llama 4 Scout 多模态 MoE，16 专家", 131072, true, false),
		mk("meta/llama-4-maverick-17b-128e-instruct", "Llama 4 Maverick 17B", "Meta Llama 4 Maverick 多模态 MoE，128 专家", 131072, true, false),

		// ── NVIDIA Nemotron ──────────────────────────────────────────
		mk("nvidia/llama-3.1-nemotron-70b-instruct", "Nemotron 70B", "NVIDIA 基于 Llama 3.1 微调的 70B 指令模型", 131072, false, false),
		mk("nvidia/llama-3.1-nemotron-ultra-253b-v1", "Nemotron Ultra 253B", "NVIDIA Nemotron Ultra 253B，推理与代理能力强", 131072, false, true),
		mk("nvidia/nemotron-4-340b-instruct", "Nemotron 4 340B", "NVIDIA 自研 340B 旗舰指令模型", 4096, false, false),
		mk("nvidia/llama-3.3-nemotron-super-49b-v1", "Nemotron Super 49B", "NVIDIA Nemotron Super 49B，高吞吐推理", 131072, false, true),
		mk("nvidia/nemotron-3-super-120b-a12b", "Nemotron 3 Super 120B", "NVIDIA 自研 120B MoE，代理/推理/编码", 131072, false, true),
		mk("nvidia/nemotron-3-ultra-550b-a55b", "Nemotron 3 Ultra 550B", "NVIDIA 旗舰 550B MoE，1M 上下文，强推理", 1048576, false, true),
		mk("nvidia/nemotron-3-nano-omni-30b-a3b-reasoning", "Nemotron Nano Omni 30B", "NVIDIA 全模态推理模型，理解图像/视频/语音/文本", 131072, true, true),

		// ── Qwen ─────────────────────────────────────────────────────
		mk("qwen/qwen3-0.6b", "Qwen3 0.6B", "阿里通义千问 3 代 0.6B 轻量模型", 32768, false, true),
		mk("qwen/qwen3-1.7b", "Qwen3 1.7B", "阿里通义千问 3 代 1.7B 轻量模型", 32768, false, true),
		mk("qwen/qwen3-4b", "Qwen3 4B", "阿里通义千问 3 代 4B 模型", 32768, false, true),
		mk("qwen/qwen3-8b", "Qwen3 8B", "阿里通义千问 3 代 8B 模型", 32768, false, true),
		mk("qwen/qwen3-14b", "Qwen3 14B", "阿里通义千问 3 代 14B 模型", 32768, false, true),
		mk("qwen/qwen3-32b", "Qwen3 32B", "阿里通义千问 3 代 32B 模型", 32768, false, true),
		mk("qwen/qwen3-235b-a22b", "Qwen3 235B", "阿里通义千问 3 代 235B MoE 旗舰", 32768, false, true),
		mk("qwen/qwen2.5-7b-instruct", "Qwen2.5 7B", "阿里通义千问 2.5 代 7B 指令模型", 32768, false, false),
		mk("qwen/qwen2.5-14b-instruct", "Qwen2.5 14B", "阿里通义千问 2.5 代 14B 指令模型", 32768, false, false),
		mk("qwen/qwen2.5-32b-instruct", "Qwen2.5 32B", "阿里通义千问 2.5 代 32B 指令模型", 32768, false, false),
		mk("qwen/qwen2.5-72b-instruct", "Qwen2.5 72B", "阿里通义千问 2.5 代 72B 指令模型", 32768, false, false),
		mk("qwen/qwen2.5-coder-32b-instruct", "Qwen2.5 Coder 32B", "阿里通义千问代码专用 32B 模型", 32768, false, false),
		mk("qwen/qwen3.5", "Qwen3.5", "阿里通义千问 3.5 代旗舰对话模型", 32768, false, true),

		// ── DeepSeek ─────────────────────────────────────────────────
		mk("deepseek-ai/deepseek-r1", "DeepSeek R1", "DeepSeek 推理模型，671B MoE，强推理", 65536, false, true),
		mk("deepseek-ai/deepseek-r1-distill-llama-70b", "DeepSeek R1 Distill Llama 70B", "R1 蒸馏到 Llama 70B，保留推理能力", 131072, false, true),
		mk("deepseek-ai/deepseek-r1-distill-llama-8b", "DeepSeek R1 Distill Llama 8B", "R1 蒸馏到 Llama 8B，轻量推理", 131072, false, true),
		mk("deepseek-ai/deepseek-r1-distill-qwen-14b", "DeepSeek R1 Distill Qwen 14B", "R1 蒸馏到 Qwen 14B", 131072, false, true),
		mk("deepseek-ai/deepseek-r1-distill-qwen-32b", "DeepSeek R1 Distill Qwen 32B", "R1 蒸馏到 Qwen 32B", 131072, false, true),
		mk("deepseek-ai/deepseek-v3", "DeepSeek V3", "DeepSeek V3 671B MoE 对话模型", 65536, false, false),
		mk("deepseek-ai/deepseek-v3.2", "DeepSeek V3.2", "DeepSeek V3.2 671B MoE，编程之王", 163840, false, false),
		mk("deepseek-ai/deepseek-v4-flash", "DeepSeek V4 Flash", "DeepSeek V4 Flash 284B MoE，1M 上下文", 1048576, false, false),
		mk("deepseek-ai/deepseek-v4-pro", "DeepSeek V4 Pro", "DeepSeek V4 Pro 1.6T MoE，1M 上下文", 1048576, false, true),

		// ── Google Gemma ────────────────────────────────────────────
		mk("google/gemma-2-2b-it", "Gemma 2 2B", "Google Gemma 2 代 2B 指令模型", 8192, false, false),
		mk("google/gemma-2-9b-it", "Gemma 2 9B", "Google Gemma 2 代 9B 指令模型", 8192, false, false),
		mk("google/gemma-2-27b-it", "Gemma 2 27B", "Google Gemma 2 代 27B 指令模型", 8192, false, false),
		mk("google/gemma-3-4b-it", "Gemma 3 4B", "Google Gemma 3 代 4B 多语言模型", 131072, false, false),
		mk("google/gemma-3-12b-it", "Gemma 3 12B", "Google Gemma 3 代 12B 多语言模型", 131072, false, false),
		mk("google/gemma-3-27b-it", "Gemma 3 27B", "Google Gemma 3 代 27B 多语言模型", 131072, false, false),
		mk("google/gemma-4-31b-it", "Gemma 4 31B", "Google Gemma 4 代 31B，Agentic 能力强", 131072, false, false),

		// ── Mistral ──────────────────────────────────────────────────
		mk("mistralai/mistral-7b-instruct-v0.3", "Mistral 7B v0.3", "Mistral 7B 指令模型 v0.3", 32768, false, false),
		mk("mistralai/mixtral-8x7b-instruct-v0.1", "Mixtral 8x7B", "Mistral MoE 8x7B 指令模型", 32768, false, false),
		mk("mistralai/mistral-nemo-12b-instruct", "Mistral Nemo 12B", "Mistral Nemo 12B 指令模型", 131072, false, false),
		mk("mistralai/mistral-small-24b-instruct", "Mistral Small 24B", "Mistral Small 24B 指令模型", 32768, false, false),
		mk("mistralai/mistral-medium-3.5-128b", "Mistral Medium 3.5 128B", "Mistral Medium 3.5 128B，文本/编码/代理", 131072, false, false),

		// ── Microsoft Phi ────────────────────────────────────────────
		mk("microsoft/phi-3.5-mini-instruct", "Phi 3.5 Mini", "Microsoft Phi 3.5 Mini 指令模型", 128000, false, false),
		mk("microsoft/phi-4-mini-instruct", "Phi 4 Mini", "Microsoft Phi 4 Mini 指令模型", 128000, false, false),
		mk("microsoft/phi-4", "Phi 4", "Microsoft Phi 4 指令模型", 128000, false, false),

		// ── Other free-endpoint flagships ───────────────────────────
		mk("minimaxai/minimax-m2.7", "MiniMax M2.7", "MiniMax M2.7 230B，编程/推理/办公全能", 131072, false, true),
		mk("minimaxai/minimax-m3", "MiniMax M3", "MiniMax M3 多模态 MoE 视觉语言模型", 131072, true, true),
		mk("moonshotai/kimi-k2.5", "Kimi K2.5", "月之暗面 Kimi K2.5 MoE，1M 上下文，中文顶级", 1048576, false, true),
		mk("z-ai/glm-5", "GLM-5", "智谱 GLM-5 旗舰对话/代理模型", 131072, false, true),
		mk("z-ai/glm-5.2", "GLM-5.2", "智谱 GLM-5.2 旗舰，Agentic/编码/长程推理", 8388608, false, true),
		mk("stepfun-ai/step-3.5-flash", "Step 3.5 Flash", "阶跃星辰 Step 3.5 Flash 高速 MoE 推理", 131072, false, true),
		mk("stepfun-ai/step-3.7-flash", "Step 3.7 Flash", "阶跃星辰 Step 3.7 Flash 稀疏 MoE 多模态推理", 131072, true, true),
	}
}
