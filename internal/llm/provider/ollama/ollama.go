// Package ollama implements the Ollama Provider using the local OpenAI-compatible
// API (Chat Completions). Ollama runs entirely offline on localhost, no API key
// required — it is the "离线编程代理" that makes iCode work without any network.
//
// Local models (example):
//   - llama3.1        通用对话/推理
//   - qwen2.5-coder   代码生成专用
//   - codellama       代码补全
//   - phi3            轻量级代码模型
//
// Install via: ollama pull <model>  （如：ollama pull llama3.1）
package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/types"
)

const (
	// ProviderName is the canonical identifier for this provider.
	ProviderName = "ollama"
	// DefaultBase is the standard Ollama server URL.
	DefaultBase = "http://localhost:11434"
	// TimeoutSec is generous for large local models (e.g. llama3 70B can take 2+ minutes).
	TimeoutSec = 600
)

func init() {
	// Register dynamically so other packages can resolve "ollama/*" model IDs.
	// The actual registration happens in app.registerProviders, but this ensures
	// the provider is known to the openai_compat package at compile time.
}

// New creates an Ollama provider. No API key is needed — the apiKey parameter
// is ignored. apiBase defaults to http://localhost:11434 if empty.
func New(apiKey, apiBase string) types.Provider {
	if strings.TrimSpace(apiBase) == "" {
		apiBase = DefaultBase
	}
	return openai_compat.NewProvider(openai_compat.FactoryConfig{
		Name:         ProviderName,
		DefaultBase:  DefaultBase,
		TimeoutSec:   TimeoutSec,
		CacheSupport: false, // 本地模型暂不支持前缀缓存
	}, apiKey, apiBase, DefaultModels())
}

// DefaultModels returns a stable list of common Ollama models.
// The real list is dynamic at runtime via DynamicModels /api/tags.
func DefaultModels() []types.ModelInfo {
	now := time.Now()
	return []types.ModelInfo{
		{
			ID:              "ollama/llama3.1",
			Name:            "Llama 3.1 8B",
			Description:     "Meta 通用语言模型，适合日常对话和编码辅助",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{Name: "free", Description: "本地免费运行，零成本", InputPrice: 0, OutputPrice: 0},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: now,
		},
		{
			ID:              "ollama/qwen2.5-coder:7b",
			Name:            "Qwen2.5 Coder 7B",
			Description:     "阿里巴巴代码专用模型，擅长代码生成和补全",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 16384,
			Plans: []types.TokenPlan{
				{Name: "free", Description: "本地免费运行", InputPrice: 0, OutputPrice: 0},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: now,
		},
		{
			ID:              "ollama/phi3",
			Name:            "Phi-3 Medium",
			Description:     "微软轻量推理模型，性价比高",
			Provider:        ProviderName,
			ContextWindow:   128000,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{Name: "free", Description: "本地免费运行", InputPrice: 0, OutputPrice: 0},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: now,
		},
		{
			ID:              "ollama/codellama",
			Name:            "CodeLlama 7B",
			Description:     "Meta 代码专用大模型，支持 Python/JS/Go 等",
			Provider:        ProviderName,
			ContextWindow:   16384,
			MaxOutputTokens: 4096,
			Plans: []types.TokenPlan{
				{Name: "free", Description: "本地免费运行", InputPrice: 0, OutputPrice: 0},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: now,
		},
	}
}

// DynamicModels queries /api/tags at runtime and returns the latest model list
// from the running Ollama server. Returns nil (not an error) if the server is
// unreachable — callers should fall back to DefaultModels in that case.
func DynamicModels(ctx context.Context, base string) ([]types.ModelInfo, error) {
	base = strings.TrimRight(base, "/")
	url := fmt.Sprintf("%s/api/tags", base)

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接 Ollama 服务器 %s（请确认已运行 ollama serve）: %w", base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama /api/tags 返回 HTTP %d", resp.StatusCode)
	}

	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	if len(body.Models) == 0 {
		return nil, fmt.Errorf("Ollama 服务器上暂无模型，请先运行：ollama pull llama3.1")
	}

	models := make([]types.ModelInfo, 0, len(body.Models))
	for _, m := range body.Models {
		display := modelDisplayName(m.Name)
		models = append(models, types.ModelInfo{
			ID:              "ollama/" + m.Name,
			Name:            display,
			Description:     "Ollama 本地模型 — 离线运行，无需网络",
			Provider:        ProviderName,
			ContextWindow:   131072,
			MaxOutputTokens: 8192,
			Plans: []types.TokenPlan{
				{Name: "free", Description: "本地免费", InputPrice: 0, OutputPrice: 0},
			},
			Capabilities: types.ModelCap{
				Tools:     true,
				Streaming: true,
				JSONMode:  true,
			},
			UpdatedAt: time.Now(),
		})
	}
	return models, nil
}

// brandDisplayNames spells known model families the way their vendors do.
// The spellings are genuinely inconsistent ("Llama 3.1" vs "Qwen2.5" vs
// "Phi 3"), so a lookup table is the only reliable source — a single
// tokenising rule cannot derive all of them.
var brandDisplayNames = map[string]string{
	"llama2": "Llama 2", "llama3": "Llama 3", "llama3.1": "Llama 3.1",
	"llama3.2": "Llama 3.2", "llama3.3": "Llama 3.3", "llama4": "Llama 4",
	"codellama":         "Codellama",
	"qwen":              "Qwen",
	"qwen2":             "Qwen2",
	"qwen2.5":           "Qwen2.5",
	"qwen3":             "Qwen3",
	"phi3":              "Phi 3",
	"phi4":              "Phi 4",
	"deepseek-coder-v2": "Deepseek Coder V2",
	"deepseek-r1":       "Deepseek R1",
	"gemma":             "Gemma",
	"gemma2":            "Gemma 2",
	"gemma3":            "Gemma 3",
	"mistral":           "Mistral",
	"mixtral":           "Mixtral",
}

// modelDisplayName converts an Ollama model name to a readable label.
// e.g. "qwen2.5-coder:7b" → "Qwen2.5 Coder", "llama3.1" → "Llama 3.1"
func modelDisplayName(raw string) string {
	// Strip any ":tag" suffix (":latest", ":7b", ":16b", ":instruct", ...).
	base := raw
	if i := strings.Index(base, ":"); i >= 0 {
		base = base[:i]
	}
	low := strings.ToLower(base)
	// Exact family match first (handles "deepseek-coder-v2" as a whole).
	if name, ok := brandDisplayNames[low]; ok {
		return name
	}
	// Then the leading brand segment, keeping the rest title-cased:
	// "qwen2.5-coder" → "Qwen2.5 Coder".
	if i := strings.Index(base, "-"); i > 0 {
		if name, ok := brandDisplayNames[strings.ToLower(base[:i])]; ok {
			return name + " " + titleCasedSegments(base[i+1:])
		}
	}
	return titleCasedSegments(base)
}

// titleCasedSegments hyphen-splits a model tail and title-cases each segment,
// inserting a space at letter→digit boundaries when the letter run is at least
// two characters ("llama4-tiny" → "Llama 4 Tiny") while keeping short version
// markers glued ("v2" → "V2").
func titleCasedSegments(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		p = splitLetterDigit(p)
		if len(p) >= 2 {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		} else {
			parts[i] = strings.ToUpper(p)
		}
	}
	return strings.Join(parts, " ")
}

// splitLetterDigit inserts a space where a run of ≥2 letters is followed by a
// digit ("phi3" → "phi 3"); single-letter runs stay glued ("v2" → "v2").
func splitLetterDigit(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && isASCIILetter(runes[i-1]) && r >= '0' && r <= '9' {
			j := i
			for j > 0 && isASCIILetter(runes[j-1]) {
				j--
			}
			if i-j >= 2 {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
