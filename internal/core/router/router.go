// Package router provides intelligent model routing.
//
// Based on query complexity analysis, it selects the most cost-effective
// model for each user request. Simple queries use cheap models, complex
// tasks use powerful models.
package router

import (
	"strings"

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

// Router selects models based on query complexity.
type Router struct {
	defaultModel  string
	defaultProv   string
	cheapModel    string
	cheapProv     string
	powerfulModel string
	powerfulProv  string
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

	if len(q) < 100 {
		// Very short queries are likely simple questions.
		// Exceptions: if they contain code-like keywords.
		codeKeywords := []string{"implement", "refactor", "create", "write", "build",
			"fix", "debug", "function", "class", "struct", "interface",
			"file:", "path:", "import "}
		for _, kw := range codeKeywords {
			if strings.Contains(q, kw) {
				return ComplexityNormal
			}
		}
		return ComplexitySimple
	}

	// Longer queries are likely complex.
	if len(q) > 500 {
		return ComplexityComplex
	}

	// Check for complex keywords.
	complexKeywords := []string{"refactor", "redesign", "architecture", "migrate",
		"multi-step", "test suite", "benchmark", "concurrent",
		"optimize", "profiling", "deep analysis", "review all"}
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
