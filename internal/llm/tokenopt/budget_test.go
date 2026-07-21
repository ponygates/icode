package tokenopt

import (
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func TestBudgetEnforcer_BashLimit(t *testing.T) {
	enforcer := NewBudgetEnforcer(DefaultBudgetConfig())

	// Generate content larger than BashMax (30K)
	largeContent := strings.Repeat("a", 35000)
	trimmed, truncated := enforcer.Enforce("bash", largeContent)

	if !truncated {
		t.Error("expected bash output to be truncated")
	}
	if len(trimmed) > 30000 {
		t.Errorf("bash output should be <= 30000 chars, got %d", len(trimmed))
	}
}

func TestBudgetEnforcer_ReadFileLimit(t *testing.T) {
	enforcer := NewBudgetEnforcer(DefaultBudgetConfig())

	largeContent := strings.Repeat("a", 60000)
	trimmed, truncated := enforcer.Enforce("read_file", largeContent)

	if !truncated {
		t.Error("expected read_file output to be truncated")
	}
	if len(trimmed) > 50000 {
		t.Errorf("read_file output should be <= 50000 chars, got %d", len(trimmed))
	}
}

func TestBudgetEnforcer_GlobalLimit(t *testing.T) {
	enforcer := NewBudgetEnforcer(BudgetConfig{
		GlobalMax:  100,
		DefaultMax: 1000,
	})

	first, _ := enforcer.Enforce("echo", strings.Repeat("a", 80))
	second, truncated := enforcer.Enforce("echo", strings.Repeat("b", 80))

	if !truncated {
		t.Error("expected second call to be truncated by global limit")
	}
	if !strings.Contains(second, "Global budget") {
		t.Errorf("expected global budget message, got %q", second)
	}
	_ = first
}

func TestBudgetEnforcer_SmallOutput(t *testing.T) {
	enforcer := NewBudgetEnforcer(DefaultBudgetConfig())

	content := "hello world"
	trimmed, truncated := enforcer.Enforce("bash", content)

	if truncated {
		t.Error("small output should not be truncated")
	}
	if trimmed != content {
		t.Errorf("expected %q, got %q", content, trimmed)
	}
}

func TestBudgetEnforcer_EmptyContent(t *testing.T) {
	enforcer := NewBudgetEnforcer(DefaultBudgetConfig())

	trimmed, truncated := enforcer.Enforce("bash", "")
	if truncated || trimmed != "" {
		t.Error("empty content should not be truncated")
	}
}

func TestBudgetEnforcer_Reset(t *testing.T) {
	enforcer := NewBudgetEnforcer(BudgetConfig{
		GlobalMax:  100,
		DefaultMax: 1000,
	})

	enforcer.Enforce("echo", strings.Repeat("a", 80))
	enforcer.Reset()

	// After reset, should have full budget again
	trimmed, truncated := enforcer.Enforce("echo", strings.Repeat("b", 80))
	if truncated {
		t.Error("after reset, budget should be restored")
	}
	if len(trimmed) != 80 {
		t.Errorf("expected 80 chars, got %d", len(trimmed))
	}
}

func TestCacheAwareCompactor_ShouldCompact(t *testing.T) {
	compactor := DefaultCacheAwareCompactor()

	msgs := []types.Message{
		{Role: types.RoleSystem, Content: "system prompt"},
		{Role: types.RoleUser, Content: "hello"},
	}

	// First message should be in cache region (not compacted)
	if compactor.ShouldCompact(msgs, 0) {
		t.Error("first message should not be compacted (cache region)")
	}
}

func TestDefaultBudgetConfig(t *testing.T) {
	cfg := DefaultBudgetConfig()
	if cfg.GlobalMax != 200_000 {
		t.Errorf("expected GlobalMax 200000, got %d", cfg.GlobalMax)
	}
	if cfg.BashMax != 30_000 {
		t.Errorf("expected BashMax 30000, got %d", cfg.BashMax)
	}
	if cfg.ReadMax != 50_000 {
		t.Errorf("expected ReadMax 50000, got %d", cfg.ReadMax)
	}
}
