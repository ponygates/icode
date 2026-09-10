package agent

import (
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// Sub-agents historically pinned temperature to 0.1 so tool-call arguments stay
// predictable. The per-model ⚙️ panel must be able to override that — including
// pinning it to an explicit 0 — without any other behaviour changing.

func TestResolveGenerationDefaultsToSubAgentTemp(t *testing.T) {
	r := &Runner{}
	temp, topP := r.resolveGeneration("deepseek", "deepseek-v4-flash")

	if temp == nil {
		t.Fatal("temperature = nil, want the sub-agent default")
	}
	if *temp != subAgentDefaultTemp {
		t.Fatalf("temperature = %v, want %v", *temp, subAgentDefaultTemp)
	}
	if topP != 0 {
		t.Fatalf("topP = %v, want 0 (unset)", topP)
	}
}

func TestResolveGenerationPerModelOverrideWins(t *testing.T) {
	r := &Runner{}
	r.SetModelParamsResolver(func(provider, modelID string) (*float64, float64, int, bool) {
		if provider != "deepseek" || modelID != "deepseek-v4-flash" {
			return nil, 0, 0, false
		}
		return types.Temp(0.9), 0.95, 4096, true
	})

	temp, topP := r.resolveGeneration("deepseek", "deepseek-v4-flash")
	if temp == nil || *temp != 0.9 {
		t.Fatalf("temperature = %v, want 0.9", temp)
	}
	if topP != 0.95 {
		t.Fatalf("topP = %v, want 0.95", topP)
	}
}

// Under the old float64 + `> 0` guard an override of 0 was indistinguishable
// from "unset" and got dropped, so choosing 精确 changed nothing.
func TestResolveGenerationExplicitZeroIsHonoured(t *testing.T) {
	r := &Runner{}
	r.SetModelParamsResolver(func(provider, modelID string) (*float64, float64, int, bool) {
		return types.Temp(0), 0, 0, true
	})

	temp, _ := r.resolveGeneration("openai", "gpt-4o")
	if temp == nil {
		t.Fatal("temperature = nil, want an explicit 0")
	}
	if *temp != 0 {
		t.Fatalf("temperature = %v, want 0", *temp)
	}
}

// A resolver that does not know this model must leave the sub-agent default
// alone rather than borrowing another model's settings.
func TestResolveGenerationUnknownModelKeepsDefault(t *testing.T) {
	r := &Runner{}
	r.SetModelParamsResolver(func(provider, modelID string) (*float64, float64, int, bool) {
		return types.Temp(0.9), 0.9, 1000, false
	})

	temp, topP := r.resolveGeneration("openai", "gpt-4o")
	if temp == nil || *temp != subAgentDefaultTemp {
		t.Fatalf("temperature = %v, want %v", temp, subAgentDefaultTemp)
	}
	if topP != 0 {
		t.Fatalf("topP = %v, want 0", topP)
	}
}

// top_p alone may be pinned while temperature keeps its sub-agent default.
func TestResolveGenerationTopPOnly(t *testing.T) {
	r := &Runner{}
	r.SetModelParamsResolver(func(provider, modelID string) (*float64, float64, int, bool) {
		return nil, 0.4, 0, true
	})

	temp, topP := r.resolveGeneration("openai", "gpt-4o")
	if temp == nil || *temp != subAgentDefaultTemp {
		t.Fatalf("temperature = %v, want the untouched default", temp)
	}
	if topP != 0.4 {
		t.Fatalf("topP = %v, want 0.4", topP)
	}
}

func TestHasModelParamsResolver(t *testing.T) {
	r := &Runner{}
	if r.HasModelParamsResolver() {
		t.Fatal("a fresh runner must not report a resolver")
	}
	r.SetModelParamsResolver(func(provider, modelID string) (*float64, float64, int, bool) {
		return nil, 0, 0, false
	})
	if !r.HasModelParamsResolver() {
		t.Fatal("resolver was installed but not reported")
	}
}
