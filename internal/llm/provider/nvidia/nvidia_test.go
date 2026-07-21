package nvidia

import (
	"testing"

	"github.com/ponygates/icode/internal/llm/provider/openai_compat"
)

func TestNew(t *testing.T) {
	p := New("nvapi-test", "")
	if p.Name() != ProviderName {
		t.Errorf("expected name %q, got %q", ProviderName, p.Name())
	}
	bp, ok := p.(*openai_compat.BaseProvider)
	if !ok {
		t.Fatal("NVIDIA provider should wrap openai_compat.BaseProvider")
	}
	if bp.Name() != ProviderName {
		t.Errorf("expected base name %q, got %q", ProviderName, bp.Name())
	}
}

func TestNew_CustomBase(t *testing.T) {
	customBase := "https://custom.nvidia.com"
	p := New("nvapi-test", customBase)
	if p.Name() != ProviderName {
		t.Errorf("expected name %q", ProviderName)
	}
}

func TestDefaultModels(t *testing.T) {
	models := DefaultModels()
	if len(models) < 10 {
		t.Fatalf("expected at least 10 models (NVIDIA has ~80), got %d", len(models))
	}
	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
	}
	// Check a few key models
	keyModels := []string{
		"meta/llama-3.1-405b-instruct",
		"nvidia/llama-3.1-nemotron-ultra-253b-v1",
		"qwen/qwen3-235b-a22b",
		"deepseek-ai/deepseek-r1",
	}
	for _, id := range keyModels {
		if !ids[id] {
			t.Errorf("expected model %q in default models", id)
		}
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

func TestModelsHaveFreePricing(t *testing.T) {
	for _, m := range DefaultModels() {
		for _, plan := range m.Plans {
			if plan.InputPrice != 0 {
				t.Errorf("model %q plan %q: NVIDIA free endpoint should have 0 input price", m.ID, plan.Name)
			}
			if plan.OutputPrice != 0 {
				t.Errorf("model %q plan %q: NVIDIA free endpoint should have 0 output price", m.ID, plan.Name)
			}
			if plan.FreeTier == nil {
				t.Errorf("model %q plan %q: NVIDIA free endpoint should have FreeTier", m.ID, plan.Name)
			} else {
				if plan.FreeTier.DailyTokens <= 0 {
					t.Errorf("model %q: expected positive daily token limit", m.ID)
				}
				if plan.FreeTier.DailyRequests <= 0 {
					t.Errorf("model %q: expected positive daily request limit", m.ID)
				}
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
	p := New("nvapi-test", "")
	if p.Name() != "nvidia" {
		t.Errorf("expected name 'nvidia', got %q", p.Name())
	}
	if p.SupportsCache() {
		t.Error("NVIDIA NIM should NOT support prefix caching")
	}
	models := p.ListModels()
	if len(models) == 0 {
		t.Fatal("expected at least 1 model")
	}
}

func TestProviderNameConstant(t *testing.T) {
	if ProviderName != "nvidia" {
		t.Errorf("expected ProviderName 'nvidia', got %q", ProviderName)
	}
	if DefaultBase != "https://integrate.api.nvidia.com/v1" {
		t.Errorf("expected DefaultBase 'https://integrate.api.nvidia.com/v1', got %q", DefaultBase)
	}
}

func TestFreePlanHelper(t *testing.T) {
	plan := freePlan("test-plan", "test description")
	if plan.InputPrice != 0 {
		t.Errorf("expected 0 input price, got %f", plan.InputPrice)
	}
	if plan.OutputPrice != 0 {
		t.Errorf("expected 0 output price, got %f", plan.OutputPrice)
	}
	if plan.FreeTier == nil {
		t.Fatal("expected FreeTier to be set")
	}
	if plan.FreeTier.DailyTokens <= 0 {
		t.Errorf("expected positive DailyTokens, got %d", plan.FreeTier.DailyTokens)
	}
	if plan.FreeTier.DailyRequests <= 0 {
		t.Errorf("expected positive DailyRequests, got %d", plan.FreeTier.DailyRequests)
	}
}
