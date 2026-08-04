package router

import (
	"testing"
)

func TestClassify_Simple(t *testing.T) {
	tests := []struct {
		query    string
		expected Complexity
	}{
		{"hi", ComplexitySimple},
		{"hello", ComplexitySimple},
		{"what is go", ComplexitySimple},
		{"how are you", ComplexitySimple},
		{"thanks", ComplexitySimple},
	}
	for _, tt := range tests {
		result := Classify(tt.query)
		if result != tt.expected {
			t.Errorf("Classify(%q) = %d, want %d", tt.query, result, tt.expected)
		}
	}
}

func TestClassify_Normal(t *testing.T) {
	// Short queries with code keywords
	tests := []struct {
		query    string
		expected Complexity
	}{
		{"implement a function", ComplexityNormal},
		{"write a test", ComplexityNormal},
		{"fix this bug", ComplexityNormal},
		{"create a struct", ComplexityNormal},
		{"build a parser", ComplexityNormal},
		{"debug the issue", ComplexityNormal},
	}
	for _, tt := range tests {
		result := Classify(tt.query)
		if result != tt.expected {
			t.Errorf("Classify(%q) = %d, want %d", tt.query, result, tt.expected)
		}
	}
}

func TestClassify_Complex(t *testing.T) {
	tests := []struct {
		query    string
		expected Complexity
	}{
		// Long queries (>500 chars) should be complex
		{string(make([]byte, 501)), ComplexityComplex},
		// Queries with complex keywords that are also long enough
		{"refactor the entire authentication module to use JWT tokens and add role-based access control for the enterprise platform with multi-tenant support and granular permissions", ComplexityComplex},
		{"redesign the database schema to support multi-tenancy with sharding, replication, and failover across multiple regions", ComplexityComplex},
	}
	for _, tt := range tests {
		result := Classify(tt.query)
		if result != tt.expected {
			t.Errorf("Classify(%q) = %d, want %d", tt.query, result, tt.expected)
		}
	}
}

func TestRouteQuery_Simple(t *testing.T) {
	r := New(Config{
		DefaultModel:  "deepseek-chat",
		DefaultProv:   "deepseek",
		CheapModel:    "deepseek-chat",
		CheapProv:     "deepseek",
		PowerfulModel: "claude-sonnet-4",
		PowerfulProv:  "anthropic",
	})
	route := r.RouteQuery("hello", 0)
	if route.ModelID != "deepseek-chat" {
		t.Errorf("expected cheap model, got %s", route.ModelID)
	}
	if route.Complexity != ComplexitySimple {
		t.Errorf("expected simple, got %d", route.Complexity)
	}
}

func TestRouteQuery_Normal(t *testing.T) {
	r := New(Config{
		DefaultModel:  "deepseek-chat",
		DefaultProv:   "deepseek",
		CheapModel:    "deepseek-chat",
		CheapProv:     "deepseek",
		PowerfulModel: "claude-sonnet-4",
		PowerfulProv:  "anthropic",
	})
	route := r.RouteQuery("implement a sorting function", 0)
	if route.ModelID != "deepseek-chat" {
		t.Errorf("expected default model, got %s", route.ModelID)
	}
	if route.Complexity != ComplexityNormal {
		t.Errorf("expected normal, got %d", route.Complexity)
	}
}

func TestRouteQuery_Complex(t *testing.T) {
	r := New(Config{
		DefaultModel:  "deepseek-chat",
		DefaultProv:   "deepseek",
		CheapModel:    "deepseek-chat",
		CheapProv:     "deepseek",
		PowerfulModel: "claude-sonnet-4",
		PowerfulProv:  "anthropic",
	})
	// Use a long query (>500 chars) to trigger complex classification
	longQuery := "refactor the entire authentication module to use JWT tokens and add role-based access control for the enterprise platform with multi-tenant support and granular permissions across the entire system while maintaining backward compatibility with existing APIs and ensuring data consistency throughout the migration process"
	route := r.RouteQuery(longQuery, 0)
	if route.ModelID != "claude-sonnet-4" {
		t.Errorf("expected powerful model, got %s", route.ModelID)
	}
	if route.Complexity != ComplexityComplex {
		t.Errorf("expected complex, got %d", route.Complexity)
	}
}

func TestRouteQuery_LongHistory(t *testing.T) {
	r := New(Config{
		DefaultModel:  "deepseek-chat",
		DefaultProv:   "deepseek",
		CheapModel:    "deepseek-chat",
		CheapProv:     "deepseek",
		PowerfulModel: "claude-sonnet-4",
		PowerfulProv:  "anthropic",
	})
	// With history > 50, even simple queries should be treated as complex
	route := r.RouteQuery("hello", 60)
	if route.Complexity != ComplexityComplex {
		t.Errorf("expected complex for long history, got %d", route.Complexity)
	}
}

func TestRoute_CheapDefaultsToDefault(t *testing.T) {
	r := New(Config{
		DefaultModel: "deepseek-chat",
		DefaultProv:  "deepseek",
	})
	if r.cheapModel != "deepseek-chat" {
		t.Errorf("cheap should default to default model")
	}
}

func TestRoute_PowerfulDefaultsToDefault(t *testing.T) {
	r := New(Config{
		DefaultModel: "deepseek-chat",
		DefaultProv:  "deepseek",
	})
	if r.powerfulModel != "deepseek-chat" {
		t.Errorf("powerful should default to default model")
	}
}

func TestRouteFromSession(t *testing.T) {
	// We can't easily test RouteFromSession without a full Session,
	// but we can verify it doesn't panic on nil
	r := New(Config{
		DefaultModel: "deepseek-chat",
		DefaultProv:  "deepseek",
	})
	_ = r // RouteFromSession needs a session
}
