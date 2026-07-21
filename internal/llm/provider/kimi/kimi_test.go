package kimi

import (
	"testing"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
)

func TestNew(t *testing.T) {
	p := New("sk-kimi", "")
	if p.Name() != ProviderName {
		t.Errorf("expected name %q, got %q", ProviderName, p.Name())
	}
	bp, ok := p.(*openai_compat.BaseProvider)
	if !ok {
		t.Fatal("Kimi provider should wrap openai_compat.BaseProvider")
	}
	if bp.Name() != ProviderName {
		t.Errorf("expected base name %q, got %q", ProviderName, bp.Name())
	}
}

func TestNew_CustomBase(t *testing.T) {
	customBase := "https://custom.moonshot.cn"
	p := New("sk-test", customBase)
	if p.Name() != ProviderName {
		t.Errorf("expected name %q", ProviderName)
	}
}

func TestDefaultModels(t *testing.T) {
	models := DefaultModels()
	if len(models) < 2 {
		t.Fatalf("expected at least 2 models, got %d", len(models))
	}
	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
	}
	if !ids["kimi-k2.7-code"] {
		t.Error("expected kimi-k2.7-code in default models")
	}
	if !ids["kimi-k2.6"] {
		t.Error("expected kimi-k2.6 in default models")
	}
}

func TestModelsHaveValidConfig(t *testing.T) {
	for _, m := range DefaultModels() {
		if m.ContextWindow <= 0 {
			t.Errorf("model %q: context window should be > 0", m.ID)
		}
		if m.MaxOutputTokens <= 0 {
			t.Errorf("model %q: max output tokens should be > 0", m.ID)
		}
		if m.Provider != ProviderName {
			t.Errorf("model %q: provider should be %q, got %q", m.ID, ProviderName, m.Provider)
		}
		if len(m.Plans) == 0 {
			t.Errorf("model %q: expected at least 1 pricing plan", m.ID)
		}
	}
}

func TestModelsHavePricing(t *testing.T) {
	for _, m := range DefaultModels() {
		for _, plan := range m.Plans {
			if plan.InputPrice < 0 {
				t.Errorf("model %q plan %q: input price should be >= 0", m.ID, plan.Name)
			}
			if plan.OutputPrice < 0 {
				t.Errorf("model %q plan %q: output price should be >= 0", m.ID, plan.Name)
			}
		}
	}
}

func TestAllModelsHaveCapabilities(t *testing.T) {
	for _, m := range DefaultModels() {
		if !m.Capabilities.Streaming {
			t.Errorf("model %q: should support streaming", m.ID)
		}
		if !m.Capabilities.Tools {
			t.Errorf("model %q: should support tools", m.ID)
		}
	}
}

func TestProviderViaNew(t *testing.T) {
	p := New("sk-test", "")
	if p.Name() != "kimi" {
		t.Errorf("expected name 'kimi', got %q", p.Name())
	}
	models := p.ListModels()
	if len(models) == 0 {
		t.Fatal("expected at least 1 model")
	}
}

func TestProviderNameConstant(t *testing.T) {
	if ProviderName != "kimi" {
		t.Errorf("expected ProviderName 'kimi', got %q", ProviderName)
	}
	if DefaultBase != "https://api.moonshot.cn/v1" {
		t.Errorf("expected DefaultBase 'https://api.moonshot.cn/v1', got %q", DefaultBase)
	}
}
