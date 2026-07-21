package volcengine

import (
	"testing"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
)

func TestNew(t *testing.T) {
	p := New("sk-volc", "")
	if p.Name() != ProviderName {
		t.Errorf("expected name %q, got %q", ProviderName, p.Name())
	}
	bp, ok := p.(*openai_compat.BaseProvider)
	if !ok {
		t.Fatal("Volcengine provider should wrap openai_compat.BaseProvider")
	}
	if bp.Name() != ProviderName {
		t.Errorf("expected base name %q, got %q", ProviderName, bp.Name())
	}
}

func TestNew_CustomBase(t *testing.T) {
	customBase := "https://custom.volcengine.com"
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
	if !ids["doubao-seed-2.1-pro"] {
		t.Error("expected doubao-seed-2.1-pro in default models")
	}
	if !ids["doubao-seed-2.1-turbo"] {
		t.Error("expected doubao-seed-2.1-turbo in default models")
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
	if p.Name() != "volcengine" {
		t.Errorf("expected name 'volcengine', got %q", p.Name())
	}
	models := p.ListModels()
	if len(models) == 0 {
		t.Fatal("expected at least 1 model")
	}
}

func TestProviderNameConstant(t *testing.T) {
	if ProviderName != "volcengine" {
		t.Errorf("expected ProviderName 'volcengine', got %q", ProviderName)
	}
	if DefaultBase != "https://ark.cn-beijing.volces.com/api/v3" {
		t.Errorf("expected DefaultBase 'https://ark.cn-beijing.volces.com/api/v3', got %q", DefaultBase)
	}
}
