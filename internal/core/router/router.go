// Package router provides intelligent model routing.
//
// Based on query complexity analysis, it selects the most cost-effective
// model for each user request. Simple queries use cheap models, complex
// tasks use powerful models.
package router

import (
	"context"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// Complexity classifies the user query.
type Complexity int

const (
	ComplexitySimple  Complexity = iota // quick Q&A, no code
	ComplexityNormal                    // standard coding task
	ComplexityComplex                   // multi-step, large refactor, deep analysis
)

// Route represents the routing decision.
type Route struct {
	ModelID    string
	Provider   string
	Complexity Complexity
	Reason     string
}

// LLMClassifyFunc asks an LLM to grade a query's complexity. Implementations
// should use a cheap/fast model. Returning an error falls back to the
// keyword heuristic, so failures are never fatal.
type LLMClassifyFunc func(ctx context.Context, query string) (Complexity, error)

// Router selects models based on query complexity.
type Router struct {
	defaultModel  string
	defaultProv   string
	cheapModel    string
	cheapProv     string
	powerfulModel string
	powerfulProv  string

	// llmClassify, when set, upgrades classification from keyword heuristics
	// to LLM-based grading (config routing.mode: "llm").
	llmClassify LLMClassifyFunc

	// embedClassify, when set, refines the keyword baseline with a local,
	// zero-cost semantic classifier (config routing.mode: "embedding"). It
	// returns (complexity, confident); when not confident the keyword
	// verdict is kept. Being fully offline it costs no tokens, so it stays
	// on the hot path without undermining the token-saving mission.
	embedClassify func(query string) (Complexity, bool)
}

// SetLLMClassifier enables LLM-based complexity grading. Pass nil to revert
// to the zero-cost keyword heuristic.
func (r *Router) SetLLMClassifier(f LLMClassifyFunc) { r.llmClassify = f }

// SetEmbeddingClassifier enables the local semantic classifier. Pass nil to
// disable. It refines (never blocks) the keyword baseline at zero token cost.
func (r *Router) SetEmbeddingClassifier(f func(query string) (Complexity, bool)) {
	r.embedClassify = f
}

// Config for the router.
type Config struct {
	DefaultModel  string
	DefaultProv   string
	CheapModel    string
	CheapProv     string
	PowerfulModel string
	PowerfulProv  string
}

// New creates a router with the given config.
func New(cfg Config) *Router {
	r := &Router{
		defaultModel:  cfg.DefaultModel,
		defaultProv:   cfg.DefaultProv,
		powerfulModel: cfg.PowerfulModel,
		powerfulProv:  cfg.PowerfulProv,
	}
	// Fall back to cheap = default if not specified.
	if cfg.CheapModel != "" {
		r.cheapModel = cfg.CheapModel
		r.cheapProv = cfg.CheapProv
	} else {
		r.cheapModel = cfg.DefaultModel
		r.cheapProv = cfg.DefaultProv
	}
	if r.powerfulModel == "" {
		r.powerfulModel = r.defaultModel
		r.powerfulProv = r.defaultProv
	}
	return r
}

// Classify determines query complexity heuristically.
func Classify(query string) Complexity {
	q := strings.ToLower(strings.TrimSpace(query))

	// Note: len() counts bytes; Chinese text is ~3 bytes per rune, so use
	// rune count for length thresholds to treat zh/en queries equally.
	runes := len([]rune(q))

	if runes < 60 {
		// Very short queries are likely simple questions.
		// Exceptions: if they contain code-like keywords (en + zh).
		codeKeywords := []string{"implement", "refactor", "create", "write", "build",
			"fix", "debug", "function", "class", "struct", "interface",
			"file:", "path:", "import ",
			"实现", "重构", "创建", "编写", "构建", "修复", "调试", "修改",
			"函数", "接口", "写一个", "帮我写", "报错", "bug"}
		for _, kw := range codeKeywords {
			if strings.Contains(q, kw) {
				return ComplexityNormal
			}
		}
		return ComplexitySimple
	}

	// Longer queries are likely complex.
	if runes > 300 {
		return ComplexityComplex
	}

	// Check for complex keywords (en + zh).
	complexKeywords := []string{"refactor", "redesign", "architecture", "migrate",
		"multi-step", "test suite", "benchmark", "concurrent",
		"optimize", "profiling", "deep analysis", "review all",
		"重构", "架构", "迁移", "多步骤", "测试套件", "基准测试", "并发",
		"优化", "性能分析", "深度分析", "全面审查", "全部检查", "整个项目"}
	for _, kw := range complexKeywords {
		if strings.Contains(q, kw) {
			return ComplexityComplex
		}
	}

	return ComplexityNormal
}

// RouteQuery picks the best model for the given query.
func (r *Router) RouteQuery(query string, historyLen int) Route {
	c := Classify(query)

	// Local semantic refinement (when enabled): zero-cost, offline. Only
	// overrides the keyword baseline when it is confident, so it can only
	// improve accuracy, never regress it.
	if r.embedClassify != nil {
		if sc, ok := r.embedClassify(query); ok {
			c = sc
		}
	}

	// LLM grading (when enabled) refines the heuristic. Bounded at 3s so a
	// slow classifier never blocks the conversation; errors fall back to
	// the keyword result.
	if r.llmClassify != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if lc, err := r.llmClassify(ctx, query); err == nil {
			c = lc
		}
		cancel()
	}

	// Use simple classification if the conversation is very long.
	if historyLen > 50 {
		c = ComplexityComplex
	}

	route := Route{Complexity: c}

	switch c {
	case ComplexitySimple:
		route.ModelID = r.cheapModel
		route.Provider = r.cheapProv
		route.Reason = "simple query → cheap model"
	case ComplexityNormal:
		route.ModelID = r.defaultModel
		route.Provider = r.defaultProv
		route.Reason = "normal query → default model"
	case ComplexityComplex:
		route.ModelID = r.powerfulModel
		route.Provider = r.powerfulProv
		route.Reason = "complex query → powerful model"
	}

	return route
}

// RouteFromSession picks a model based on the session's current context.
func RouteFromSession(session *types.Session, r *Router) string {
	q := ""
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if session.Messages[i].Role == types.RoleUser {
			q = session.Messages[i].Content
			break
		}
	}
	route := r.RouteQuery(q, len(session.Messages))
	return route.ModelID
}
