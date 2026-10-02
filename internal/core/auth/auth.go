// Package auth owns the credential lifecycle: saving a provider API key
// (encrypted at rest by config.Save), clearing it, and proving that a
// freshly-entered key actually works.
//
// Before this package existed, every surface that wanted to store a key
// re-implemented "load config, poke the map, save" and none of them verified
// the result — so a typo'd key stayed invisible until the first chat failed.
// /login and /logout now route through here so the TUI, the desktop/VS Code
// slash surface, and the CLI all store, clear and verify identically.
package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/llm/provider/agnes"
	"github.com/ponygates/icode/internal/llm/provider/anthropic"
	"github.com/ponygates/icode/internal/llm/provider/deepseek"
	"github.com/ponygates/icode/internal/llm/provider/huawei"
	"github.com/ponygates/icode/internal/llm/provider/kimi"
	"github.com/ponygates/icode/internal/llm/provider/nvidia"
	"github.com/ponygates/icode/internal/llm/provider/ollama"
	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
	"github.com/ponygates/icode/internal/llm/provider/openrouter"
	"github.com/ponygates/icode/internal/llm/provider/scnet"
	"github.com/ponygates/icode/internal/llm/provider/sensenova"
	"github.com/ponygates/icode/internal/llm/provider/tencent"
	"github.com/ponygates/icode/internal/llm/provider/volcengine"
	"github.com/ponygates/icode/internal/llm/provider/zhipu"
	"github.com/ponygates/icode/internal/types"
)

// builtins maps a vendor name to its constructor. Keeping the list here (open)
// means the bootstrap in internal/app and the connectivity check below can
// never disagree about which name means which endpoint.
var builtins = map[string]func(key, base string) types.Provider{
	"deepseek":   func(k, b string) types.Provider { return deepseek.New(k, b) },
	"zhipu":      func(k, b string) types.Provider { return zhipu.New(k, b) },
	"kimi":       func(k, b string) types.Provider { return kimi.New(k, b) },
	"openrouter": func(k, b string) types.Provider { return openrouter.New(k, b) },
	"volcengine": func(k, b string) types.Provider { return volcengine.New(k, b) },
	"tencent":    func(k, b string) types.Provider { return tencent.New(k, b) },
	"huawei":     func(k, b string) types.Provider { return huawei.New(k, b) },
	"scnet":      func(k, b string) types.Provider { return scnet.New(k, b) },
	"nvidia":     func(k, b string) types.Provider { return nvidia.New(k, b) },
	"sensenova":  func(k, b string) types.Provider { return sensenova.New(k, b) },
	"agnes":      func(k, b string) types.Provider { return agnes.New(k, b) },
	"anthropic":  func(k, b string) types.Provider { return anthropic.New(k, b) },
	"ollama":     func(k, b string) types.Provider { return ollama.New(k, b) },
}

// IsBuiltin reports whether name is one of the vendors iCode knows about.
func IsBuiltin(name string) bool {
	_, ok := builtins[strings.ToLower(name)]
	return ok
}

// BuiltinNames returns the vendor names iCode ships a constructor for,
// unsorted — what /login offers as candidates.
func BuiltinNames() []string {
	out := make([]string, 0, len(builtins))
	for name := range builtins {
		out = append(out, name)
	}
	return out
}

// Provider builds a provider for name from a config entry. Unknown names are
// treated as generic OpenAI-compatible gateways, exactly as the bootstrap does
// for user-added vendors, so verification covers custom providers too.
//
// Every live provider built here is also wired with its credential kind (an
// OAuth subscription token does not travel in a vendor's API-key header) and
// the 401 renewal path, so all surfaces sharing the registry pick up rotated
// tokens without a re-login.
func Provider(name string, pc config.ProviderCfg) types.Provider {
	key := strings.TrimSpace(pc.APIKey)
	var p types.Provider
	if fn, ok := builtins[strings.ToLower(name)]; ok {
		p = fn(key, pc.APIBase)
	} else {
		p = openai_compat.New(openai_compat.Config{
			Name:         name,
			APIKey:       key,
			APIBase:      pc.APIBase,
			TimeoutSec:   pc.Timeout,
			CacheSupport: true,
		})
	}
	if oc, ok := p.(types.OAuthCredentialProvider); ok {
		_, _, sub := pc.Subscription()
		oc.SetSubscription(sub)
		oc.SetTokenRefresher(refreshHook(name))
	}
	return p
}

// refreshHook is the types.TokenRefresher installed on live providers: it
// renews the access token when it is expired or about to be and reports the
// credential kind the config now holds. For API-key-only vendors Refresh
// hands back the unchanged key with bearer=false, so providers never retry a
// plain key the user just entered.
func refreshHook(name string) types.TokenRefresher {
	return func(ctx context.Context, _ string) (string, bool, error) {
		return Refresh(ctx, name)
	}
}

// Result is the outcome of saving or clearing a credential.
type Result struct {
	Provider string `json:"provider"`
	// Path is the config file the credential was written to.
	Path string `json:"path,omitempty"`
	// Masked is the key rendered for display; the raw key is never returned.
	Masked string `json:"masked,omitempty"`
	// Cleared reports a key that was actually present and removed.
	Cleared bool `json:"cleared,omitempty"`
}

// Save stores apiKey for provider in the user config file. An empty key is
// rejected — clearing a credential is Clear's job, and silently accepting ""
// here would turn a mistyped `/login` into a logout.
func Save(provider, apiKey string) (Result, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return Result{}, fmt.Errorf("未指定提供商")
	}
	if strings.TrimSpace(apiKey) == "" {
		return Result{}, fmt.Errorf("API Key 为空（清除凭据请用 logout）")
	}
	cfg, err := config.Load()
	if err != nil {
		return Result{}, fmt.Errorf("读取配置失败: %w", err)
	}
	pc := cfg.Providers[provider]
	pc.APIKey = strings.TrimSpace(apiKey)
	// An explicitly typed key replaces any subscription login; keeping the old
	// grant alive would make providers send this plain key as a Bearer token.
	pc.Grant = nil
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.ProviderCfg{}
	}
	cfg.Providers[provider] = pc
	path := config.DefaultPath()
	if err := cfg.Save(path); err != nil {
		return Result{}, fmt.Errorf("写入配置失败: %w", err)
	}
	return Result{Provider: provider, Path: path, Masked: Masked(apiKey)}, nil
}

// Clear removes the stored credential for provider. It reports Cleared=false
// (not an error) when there was nothing to remove, so callers can tell the
// user "wasn't configured" without treating it as a failure.
func Clear(provider string) (Result, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return Result{}, fmt.Errorf("未指定提供商")
	}
	cfg, err := config.Load()
	if err != nil {
		return Result{}, fmt.Errorf("读取配置失败: %w", err)
	}
	pc, ok := cfg.Providers[provider]
	had := ok && strings.TrimSpace(pc.APIKey) != ""
	if had {
		pc.APIKey = ""
		pc.APIKeyEnc = ""
		// A subscription login's refresh token must go with it, otherwise a
		// "logout" would leave the vendor renewing the removed access token.
		pc.Grant = nil
		cfg.Providers[provider] = pc
	}
	path := config.DefaultPath()
	if err := cfg.Save(path); err != nil {
		return Result{}, fmt.Errorf("写入配置失败: %w", err)
	}
	return Result{Provider: provider, Path: path, Cleared: had}, nil
}

// Masked renders a credential for display: leading 3 and trailing 4 characters
// only, so a key on screen or in a log can't be read off the terminal.
func Masked(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	r := []rune(key)
	if len(r) <= 8 {
		return strings.Repeat("*", len(r))
	}
	return string(r[:3]) + strings.Repeat("*", 6) + string(r[len(r)-4:])
}

// VerifyResult reports a live round-trip against the vendor.
type VerifyResult struct {
	OK        bool   `json:"ok"`
	Provider  string `json:"provider"`
	ModelN    int    `json:"model_count,omitempty"`
	Sample    string `json:"sample,omitempty"`
	Err       string `json:"error,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	// Skipped is non-empty when no network probe was possible (no key, or the
	// vendor exposes no model listing). It is not a failure.
	Skipped string `json:"skipped,omitempty"`
}

// Verify proves that the saved credential for provider works by asking the
// vendor what it serves. It is the check `/login` and `icode login` run right
// after saving, so a bad key is reported at the moment it is entered rather
// than at the first failed chat.
func Verify(ctx context.Context, provider string) VerifyResult {
	provider = strings.TrimSpace(provider)
	res := VerifyResult{Provider: provider}
	cfg, err := config.Load()
	if err != nil {
		res.Err = "读取配置失败: " + err.Error()
		return res
	}
	pc := cfg.Providers[provider]
	if strings.TrimSpace(pc.APIKey) == "" {
		res.Skipped = "该提供商没有已保存的 Key，跳过连通性测试"
		return res
	}
	fetcher, ok := Provider(provider, pc).(types.ModelFetcher)
	if !ok {
		res.Skipped = "该厂商不支持模型列表接口，跳过连通性测试"
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := time.Now()
	models, err := fetcher.FetchModels(ctx)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.OK = true
	res.ModelN = len(models)
	if len(models) > 0 {
		res.Sample = models[0].ID
	}
	return res
}
