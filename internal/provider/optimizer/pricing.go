package optimizer

import (
	"fmt"
	"strings"
)

type Currency string

const (
	CNY Currency = "CNY"
	USD Currency = "USD"
)

type PlanTier struct {
	Name        string
	MonthlyCost float64
	Requests    int
	Speed       string
}

type TokenPlan struct {
	Provider      string
	Model         string
	Currency      Currency
	Input1M       float64
	Output1M      float64
	HasFreeTier   bool
	HasCodingPlan bool
	PlanTiers     []PlanTier
	CacheDiscount float64
	Notes         string
}

var tokenPlans = []TokenPlan{
	// ============================================================
	// DeepSeek — 按量，无套餐，原生 prefix-cache
	// ============================================================
	{
		Provider: "deepseek", Model: "deepseek-v4-flash",
		Currency: USD, Input1M: 0.14, Output1M: 0.28,
		HasFreeTier: false, HasCodingPlan: false,
		CacheDiscount: 0.90,
		Notes:         "原生prefix-cache, 输入缓存命中≈$0.014/M",
	},
	{
		Provider: "deepseek", Model: "deepseek-v4-pro",
		Currency: USD, Input1M: 0.35, Output1M: 0.70,
		HasFreeTier: false, HasCodingPlan: false,
		CacheDiscount: 0.90,
		Notes:         "Pro版更高精度",
	},

	// ============================================================
	// OpenAI
	// ============================================================
	{
		Provider: "openai", Model: "gpt-5.5",
		Currency: USD, Input1M: 2.50, Output1M: 10.00,
		HasFreeTier: false, HasCodingPlan: false,
		CacheDiscount: 0.50,
		Notes:         "Prompt Caching 50%折扣",
	},
	{
		Provider: "openai", Model: "gpt-5.4",
		Currency: USD, Input1M: 5.00, Output1M: 20.00,
		HasFreeTier: false, HasCodingPlan: false,
		CacheDiscount: 0.50,
	},

	// ============================================================
	// Anthropic
	// ============================================================
	{
		Provider: "anthropic", Model: "claude-sonnet-4.6",
		Currency: USD, Input1M: 3.00, Output1M: 15.00,
		HasFreeTier: false, HasCodingPlan: false,
		CacheDiscount: 0.90,
		Notes:         "Prompt Caching 90%折扣, 长上下文越用越省",
	},
	{
		Provider: "anthropic", Model: "claude-opus-4.6",
		Currency: USD, Input1M: 15.00, Output1M: 75.00,
		HasFreeTier: false, HasCodingPlan: false,
		CacheDiscount: 0.90,
	},

	// ============================================================
	// 智谱 GLM — Coding Plan
	// ============================================================
	{
		Provider: "zhipu", Model: "glm-4.7-flash",
		Currency: CNY, Input1M: 0.50, Output1M: 0.50,
		HasFreeTier: true, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "Lite", MonthlyCost: 49, Requests: 18000, Speed: "standard"},
			{Name: "Pro", MonthlyCost: 149, Requests: 90000, Speed: "priority"},
		},
		Notes: "Coding Plan Lite ¥49/月≈18K次, Pro ¥149/月≈90K次; 免费额度: glm-4.7-flash 100万token/日",
	},
	{
		Provider: "zhipu", Model: "glm-5",
		Currency: CNY, Input1M: 2.00, Output1M: 8.00,
		HasFreeTier: false, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "Lite", MonthlyCost: 49, Requests: 18000, Speed: "standard"},
			{Name: "Pro", MonthlyCost: 149, Requests: 90000, Speed: "priority"},
		},
	},

	// ============================================================
	// 阿里 Qwen — 百炼平台
	// ============================================================
	{
		Provider: "qwen", Model: "qwen-turbo",
		Currency: CNY, Input1M: 0.30, Output1M: 0.60,
		HasFreeTier: true, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "基础版", MonthlyCost: 28, Requests: 5000, Speed: "standard"},
			{Name: "标准版", MonthlyCost: 78, Requests: 20000, Speed: "standard"},
			{Name: "企业版", MonthlyCost: 238, Requests: 100000, Speed: "priority"},
		},
		Notes: "百炼平台, qwen-turbo免费100万token/月; qwen-plus ¥0.8/1M输入",
	},
	{
		Provider: "qwen", Model: "qwen-plus",
		Currency: CNY, Input1M: 0.80, Output1M: 2.00,
		HasFreeTier: false, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "基础版", MonthlyCost: 28, Requests: 5000, Speed: "standard"},
			{Name: "标准版", MonthlyCost: 78, Requests: 20000, Speed: "standard"},
			{Name: "企业版", MonthlyCost: 238, Requests: 100000, Speed: "priority"},
		},
	},
	{
		Provider: "qwen", Model: "qwen3-coder-next",
		Currency: CNY, Input1M: 4.00, Output1M: 12.00,
		HasFreeTier: false, HasCodingPlan: true,
		Notes: "代码专精最新版, 百炼企业套餐可用",
	},

	// ============================================================
	// 火山方舟 豆包 — Coding Plan
	// ============================================================
	{
		Provider: "doubao", Model: "doubao-seed-2.0-code",
		Currency: CNY, Input1M: 0.50, Output1M: 1.00,
		HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "Lite", MonthlyCost: 39, Requests: 18000, Speed: "standard"},
			{Name: "Pro", MonthlyCost: 149, Requests: 90000, Speed: "priority"},
		},
		Notes: "Coding Plan Lite ¥39/月≈18K次, Pro ¥149/月≈90K次; 自动切换最新代码模型",
	},
	{
		Provider: "doubao", Model: "doubao-seed-2.0-pro",
		Currency: CNY, Input1M: 0.80, Output1M: 2.00,
		HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "Lite", MonthlyCost: 39, Requests: 18000, Speed: "standard"},
			{Name: "Pro", MonthlyCost: 149, Requests: 90000, Speed: "priority"},
		},
	},
	{
		Provider: "doubao", Model: "doubao-seed-2.0-lite",
		Currency: CNY, Input1M: 0.30, Output1M: 0.60,
		HasFreeTier: true, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "Lite", MonthlyCost: 39, Requests: 18000, Speed: "standard"},
		},
		Notes: "免费额度: 100万token/日",
	},

	// ============================================================
	// 腾讯混元 — Hy Token Plan
	// ============================================================
	{
		Provider: "hunyuan", Model: "hy3-preview",
		Currency: CNY, Input1M: 1.20, Output1M: 4.00,
		HasFreeTier: true, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "Lite", MonthlyCost: 28, Speed: "standard"},
			{Name: "Standard", MonthlyCost: 78, Speed: "standard"},
			{Name: "Pro", MonthlyCost: 238, Speed: "priority"},
			{Name: "Max", MonthlyCost: 468, Speed: "dedicated"},
		},
		Notes: "Hy Token Plan ¥28-468/月; hy3-preview 295B MoE; hunyuan-lite ¥0.5/M极低价",
	},
	{
		Provider: "hunyuan", Model: "hunyuan-lite",
		Currency: CNY, Input1M: 0.50, Output1M: 0.50,
		HasFreeTier: true, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "Lite", MonthlyCost: 28, Speed: "standard"},
			{Name: "Standard", MonthlyCost: 78, Speed: "standard"},
		},
		Notes: "混元Lite, 性价比最高",
	},

	// ============================================================
	// 百度 ERNIE — 千帆
	// ============================================================
	{
		Provider: "ernie", Model: "ernie-4.5",
		Currency: CNY, Input1M: 1.20, Output1M: 4.80,
		HasFreeTier: false, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "基础版", MonthlyCost: 19, Requests: 5000, Speed: "standard"},
			{Name: "标准版", MonthlyCost: 79, Requests: 25000, Speed: "standard"},
			{Name: "企业版", MonthlyCost: 299, Requests: 100000, Speed: "priority"},
		},
		Notes: "千帆平台, ernie-speed有免费额度",
	},
	{
		Provider: "ernie", Model: "ernie-speed",
		Currency: CNY, Input1M: 0, Output1M: 0,
		HasFreeTier: true, HasCodingPlan: false,
		Notes: "完全免费! 适合日常编码辅助",
	},

	// ============================================================
	// 月之暗面 Kimi
	// ============================================================
	{
		Provider: "kimi", Model: "kimi-k2.5",
		Currency: CNY, Input1M: 1.00, Output1M: 4.00,
		HasFreeTier: true, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "轻量版", MonthlyCost: 29, Requests: 10000, Speed: "standard"},
			{Name: "标准版", MonthlyCost: 99, Requests: 50000, Speed: "priority"},
			{Name: "专业版", MonthlyCost: 299, Requests: 200000, Speed: "priority"},
		},
		Notes: "200K+超长上下文, 适合大型代码库; 免费用户每天50次",
	},

	// ============================================================
	// 讯飞星火
	// ============================================================
	{
		Provider: "spark", Model: "spark-4.0",
		Currency: CNY, Input1M: 0.50, Output1M: 2.00,
		HasFreeTier: true, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "基础版", MonthlyCost: 29, Speed: "standard"},
			{Name: "标准版", MonthlyCost: 89, Speed: "standard"},
			{Name: "企业版", MonthlyCost: 299, Speed: "priority"},
		},
		Notes: "讯飞开放平台, spark-3.5免费额度大",
	},
	{
		Provider: "spark", Model: "spark-3.5",
		Currency: CNY, Input1M: 0, Output1M: 0,
		HasFreeTier: true, HasCodingPlan: false,
		Notes: "免费, 每日有额度限制",
	},

	// ============================================================
	// 百川
	// ============================================================
	{
		Provider: "baichuan", Model: "baichuan-4",
		Currency: CNY, Input1M: 0.80, Output1M: 2.00,
		HasFreeTier: true, HasCodingPlan: false,
		Notes: "按量计费, 有免费额度",
	},

	// ============================================================
	// 零一万物 Yi
	// ============================================================
	{
		Provider: "yi", Model: "yi-lightning",
		Currency: CNY, Input1M: 0.50, Output1M: 1.50,
		HasFreeTier: true, HasCodingPlan: false,
		Notes: "yi-lightning性价比高",
	},
	{
		Provider: "yi", Model: "yi-large",
		Currency: CNY, Input1M: 1.00, Output1M: 4.00,
		HasFreeTier: false, HasCodingPlan: false,
	},

	// ============================================================
	// 阶跃星辰 Step
	// ============================================================
	{
		Provider: "step", Model: "step-2-16k",
		Currency: CNY, Input1M: 0.60, Output1M: 2.00,
		HasFreeTier: true, HasCodingPlan: false,
		Notes: "有免费额度",
	},

	// ============================================================
	// MiniMax
	// ============================================================
	{
		Provider: "minimax", Model: "minimax-m2.5",
		Currency: CNY, Input1M: 0.80, Output1M: 2.40,
		HasFreeTier: true, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "基础版", MonthlyCost: 29, Speed: "standard"},
			{Name: "专业版", MonthlyCost: 99, Speed: "priority"},
		},
		Notes: "MiniMax有token套餐",
	},

	// ============================================================
	// 华为盘古
	// ============================================================
	{
		Provider: "pangu", Model: "pangu-nlp-5.0",
		Currency: CNY, Input1M: 2.00, Output1M: 8.00,
		HasFreeTier: false, HasCodingPlan: true,
		PlanTiers: []PlanTier{
			{Name: "基础版", MonthlyCost: 99, Speed: "standard"},
			{Name: "企业版", MonthlyCost: 399, Speed: "priority"},
			{Name: "旗舰版", MonthlyCost: 999, Speed: "dedicated"},
		},
		Notes: "ModelArts平台, 华为云生态, 数据不出云",
	},

	// ============================================================
	// 硅基流动 SiliconFlow — 免费开源模型
	// ============================================================
	{
		Provider: "siliconflow", Model: "Qwen2.5-72B",
		Currency: CNY, Input1M: 0.35, Output1M: 0.70,
		HasFreeTier: true, HasCodingPlan: false,
		Notes: "多模型免费; Qwen2.5-72B/DeepSeek-V3/GLM-4-9B等开源模型",
	},
	{
		Provider: "siliconflow", Model: "DeepSeek-V3",
		Currency: CNY, Input1M: 0.35, Output1M: 0.70,
		HasFreeTier: true,
		Notes: "硅基流动免费提供DeepSeek-V3",
	},

	// ============================================================
	// NVIDIA NIM — 免费模型
	// ============================================================
	{
		Provider: "nvidia", Model: "qwen3-coder-480b",
		Currency: USD, Input1M: 0, Output1M: 0,
		HasFreeTier: true,
		Notes: "完全免费, ~40 RPM限制; 开发/原型用",
	},
	{
		Provider: "nvidia", Model: "nemotron-3",
		Currency: USD, Input1M: 0, Output1M: 0,
		HasFreeTier: true,
		Notes: "免费, 函数调用最强",
	},

	// ============================================================
	// OpenRouter — 路由
	// ============================================================
	{
		Provider: "openrouter", Model: "auto",
		Currency: USD, Input1M: 0, Output1M: 0,
		HasFreeTier: true,
		Notes: "自动路由最优模型; free池有频率限制(RPM)",
	},
	{
		Provider: "openrouter", Model: "free",
		Currency: USD, Input1M: 0, Output1M: 0,
		HasFreeTier: true,
		Notes: "免费模型池, 有RPM限制, 适合开发调试",
	},
}

// ============================================================
// 查询接口
// ============================================================

func GetTokenPlan(provider, model string) *TokenPlan {
	for _, p := range tokenPlans {
		if p.Provider == provider && p.Model == model {
			return &p
		}
	}
	return nil
}

func ListProviderPlans(provider string) []TokenPlan {
	var plans []TokenPlan
	for _, p := range tokenPlans {
		if p.Provider == provider {
			plans = append(plans, p)
		}
	}
	return plans
}

func ListAllPlans() []TokenPlan {
	return tokenPlans
}

func (p TokenPlan) FormatPrice() string {
	if p.HasFreeTier && p.Input1M == 0 && p.Output1M == 0 {
		return "免费"
	}
	return fmt.Sprintf("输入 ¥%.2f/M  输出 ¥%.2f/M", p.Input1M, p.Output1M)
}

func (p TokenPlan) FormatTiers() string {
	if len(p.PlanTiers) == 0 {
		return "无套餐, 按量计费"
	}
	var parts []string
	for _, t := range p.PlanTiers {
		reqStr := ""
		if t.Requests > 0 {
			reqStr = fmt.Sprintf(" ~%d次/月", t.Requests)
		}
		parts = append(parts, fmt.Sprintf("%s ¥%.0f/月%s (%s)", t.Name, t.MonthlyCost, reqStr, t.Speed))
	}
	return strings.Join(parts, " | ")
}

func FormatPlanShort(provider, model string) string {
	p := GetTokenPlan(provider, model)
	if p == nil {
		return "—"
	}
	if p.HasFreeTier && p.Input1M == 0 && p.Output1M == 0 {
		return "免费"
	}
	s := fmt.Sprintf("%.2f/%.2f", p.Input1M, p.Output1M)
	if p.HasCodingPlan {
		s += " +套餐"
	} else {
		s += " 按量"
	}
	return s
}

// EstimateCost estimates the cost of a request given token counts.
func (p TokenPlan) EstimateCost(inputTokens, outputTokens int) float64 {
	inputCost := p.Input1M * float64(inputTokens) / 1_000_000
	outputCost := p.Output1M * float64(outputTokens) / 1_000_000
	return inputCost + outputCost
}

func HasFreeModel(provider string) bool {
	for _, p := range tokenPlans {
		if p.Provider == provider && p.HasFreeTier && p.Input1M == 0 {
			return true
		}
	}
	return false
}
