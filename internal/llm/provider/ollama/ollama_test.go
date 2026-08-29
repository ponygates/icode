package ollama

import (
	"context"
	"testing"
)

func TestNew(t *testing.T) {
	p := New("", "")
	if p.Name() != ProviderName {
		t.Errorf("expected provider name %q, got %q", ProviderName, p.Name())
	}
	models := p.ListModels()
	if len(models) == 0 {
		t.Error("expected at least one model")
	}
}

func TestDefaultModels(t *testing.T) {
	models := DefaultModels()
	if len(models) < 3 {
		t.Fatalf("expected at least 3 default models, got %d", len(models))
	}
	for _, m := range models {
		if m.ID == "" {
			t.Error("model ID must not be empty")
		}
		if m.Provider != ProviderName {
			t.Errorf("model %s has wrong provider %q", m.ID, m.Provider)
		}
		if m.ContextWindow <= 0 {
			t.Errorf("model %s has non-positive context window", m.ID)
		}
	}
}

func TestModelDisplayName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"llama3.1", "Llama 3.1"},
		{"qwen2.5-coder:7b", "Qwen2.5 Coder"},
		{"phi3-medium:latest", "Phi 3 Medium"},
		{"codellama", "Codellama"},
		{"deepseek-coder-v2:16b", "Deepseek Coder V2"},
		{"mistral", "Mistral"},
	}
	for _, c := range cases {
		got := modelDisplayName(c.input)
		if got != c.expected {
			t.Errorf("modelDisplayName(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestDynamicModels_NotReachable(t *testing.T) {
	ctx := context.Background()
	// Use an obviously wrong base URL to simulate server unreachable
	models, err := DynamicModels(ctx, "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if models != nil {
		t.Error("expected nil models for unreachable server")
	}
}
