package conversation

import (
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// These cover applyModelParams directly: it is the single place the three
// request sites (main turn, tool-JSON repair, fallback model) resolve
// generation parameters through, so getting it right here is what keeps them
// from drifting apart.

func newParamEngine(t *testing.T, globalTemp float64, resolver ModelParamsResolver) *Engine {
	t.Helper()
	e := NewEngine(nil, nil, nil)
	e.SetGenerationParams(globalTemp, 0)
	if resolver != nil {
		e.SetModelParamsResolver(resolver)
	}
	return e
}

func ptr(v float64) *float64 { return &v }

func TestApplyModelParamsFallsBackToGlobals(t *testing.T) {
	e := newParamEngine(t, 0.7, nil)
	mi := types.ModelInfo{ID: "demo-a", Provider: "demo", MaxOutputTokens: 8192}

	temp, topP, maxOut := e.applyModelParams(mi)

	if temp == nil || *temp != 0.7 {
		t.Fatalf("temperature = %v, want 0.7 (the engine-wide default)", temp)
	}
	if topP != 0 {
		t.Fatalf("topP = %v, want 0 (unset)", topP)
	}
	if maxOut != 8192 {
		t.Fatalf("maxOut = %d, want the model's own 8192", maxOut)
	}
}

// A global default of 0 has always meant "not configured — let the provider
// decide", and it must stay nil on the wire rather than becoming an explicit 0.
func TestApplyModelParamsUnsetGlobalStaysNil(t *testing.T) {
	e := newParamEngine(t, 0, nil)
	temp, _, _ := e.applyModelParams(types.ModelInfo{ID: "demo-a", Provider: "demo"})
	if temp != nil {
		t.Fatalf("temperature = %v, want nil (nothing configured)", *temp)
	}
}

// An override of 0 means "deterministic" and must survive: under the old
// float64 + `> 0` guard it was indistinguishable from "unset" and silently
// dropped, so choosing 精确 in the dialog changed nothing.
func TestApplyModelParamsExplicitZeroOverrideIsHonoured(t *testing.T) {
	e := newParamEngine(t, 0.7, func(provider, modelID string) (*float64, float64, int, bool) {
		return ptr(0), 0, 0, true
	})
	temp, _, _ := e.applyModelParams(types.ModelInfo{ID: "demo-a", Provider: "demo"})
	if temp == nil {
		t.Fatalf("temperature = nil, want an explicit 0")
	}
	if *temp != 0 {
		t.Fatalf("temperature = %v, want 0", *temp)
	}
}

// Each field is independent: setting only top_p must not disturb the global
// temperature, and vice versa.
func TestApplyModelParamsPartialOverride(t *testing.T) {
	e := newParamEngine(t, 0.3, func(provider, modelID string) (*float64, float64, int, bool) {
		return nil, 0.4, 32000, true
	})
	mi := types.ModelInfo{ID: "demo-a", Provider: "demo", MaxOutputTokens: 8192}

	temp, topP, maxOut := e.applyModelParams(mi)

	if temp == nil || *temp != 0.3 {
		t.Fatalf("temperature = %v, want the untouched global 0.3", temp)
	}
	if topP != 0.4 {
		t.Fatalf("topP = %v, want 0.4", topP)
	}
	if maxOut != 32000 {
		t.Fatalf("maxOut = %d, want the override 32000", maxOut)
	}
}

// A resolver that knows nothing about this model must leave the globals alone.
func TestApplyModelParamsUnknownModelKeepsGlobals(t *testing.T) {
	e := newParamEngine(t, 0.9, func(provider, modelID string) (*float64, float64, int, bool) {
		return ptr(0.1), 0.1, 1000, false
	})
	temp, topP, maxOut := e.applyModelParams(types.ModelInfo{ID: "demo-a", Provider: "demo", MaxOutputTokens: 4096})
	if temp == nil || *temp != 0.9 {
		t.Fatalf("temperature = %v, want 0.9", temp)
	}
	if topP != 0 {
		t.Fatalf("topP = %v, want 0", topP)
	}
	if maxOut != 4096 {
		t.Fatalf("maxOut = %d, want 4096", maxOut)
	}
}

// A model with no provider can never be matched in the config, so the lookup is
// skipped rather than issued with a half-empty key.
func TestApplyModelParamsSkipsLookupWithoutProvider(t *testing.T) {
	asked := false
	e := newParamEngine(t, 0.5, func(provider, modelID string) (*float64, float64, int, bool) {
		asked = true
		return nil, 0, 0, true
	})
	e.applyModelParams(types.ModelInfo{ID: "demo-a"})
	if asked {
		t.Fatalf("resolver must not be consulted for a provider-less model")
	}
}
