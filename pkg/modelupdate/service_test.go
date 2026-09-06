package modelupdate

import (
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// TestCompareModels pins the add/remove diff between the built-in catalog
// and the enriched API-fetched list — the signal that drives "新模型加入 /
// 模型下架" toasts. Order must not affect the result.
func TestCompareModels(t *testing.T) {
	builtin := []types.ModelInfo{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	enriched := []types.ModelInfo{{ID: "b"}, {ID: "c"}, {ID: "d"}}

	added, removed := compareModels(builtin, enriched)

	if len(added) != 1 || added[0].ID != "d" {
		t.Fatalf("added = %v, want [d]", added)
	}
	if len(removed) != 1 || removed[0] != "a" {
		t.Fatalf("removed = %v, want [a]", removed)
	}
}

func TestCompareModelsIdentical(t *testing.T) {
	list := []types.ModelInfo{{ID: "a"}, {ID: "b"}}
	added, removed := compareModels(list, list)
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("identical lists: added=%v removed=%v, want empty", added, removed)
	}
}

func TestCompareModelsEmptyBuiltin(t *testing.T) {
	added, removed := compareModels(nil, []types.ModelInfo{{ID: "x"}})
	if len(added) != 1 || added[0].ID != "x" {
		t.Fatalf("added = %v, want [x]", added)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want empty", removed)
	}
}

// TestApplyDeprecatedLogicAntiFlap verifies the consecutive-2-absence rule:
// a model flickering off for one refresh must NOT be marked deprecated
// (avoids flapping on transient API gaps), but two consecutive absences do.
func TestApplyDeprecatedLogicAntiFlap(t *testing.T) {
	current := []types.ModelInfo{{ID: "gone"}}

	// First absence: count 1, not yet deprecated.
	res := applyDeprecatedLogic(current, nil, map[string]bool{})
	if res[0].DeprecatedCount != 1 || res[0].Deprecated {
		t.Fatalf("1st absence: count=%d deprecated=%v, want 1/false",
			res[0].DeprecatedCount, res[0].Deprecated)
	}

	// Second absence (prev count=1): count 2, now deprecated.
	prev := []types.ModelInfo{{ID: "gone", DeprecatedCount: 1}}
	res = applyDeprecatedLogic(current, prev, map[string]bool{})
	if res[0].DeprecatedCount != 2 || !res[0].Deprecated {
		t.Fatalf("2nd absence: count=%d deprecated=%v, want 2/true",
			res[0].DeprecatedCount, res[0].Deprecated)
	}
}

// TestApplyDeprecatedLogicResetsOnReturn ensures a model that reappears in
// the API after being deprecated is un-deprecated (count reset to 0).
func TestApplyDeprecatedLogicResetsOnReturn(t *testing.T) {
	current := []types.ModelInfo{{ID: "back", Deprecated: true, DeprecatedCount: 3}}
	res := applyDeprecatedLogic(current, current, map[string]bool{"back": true})
	if res[0].Deprecated || res[0].DeprecatedCount != 0 {
		t.Fatalf("reappeared model: deprecated=%v count=%d, want false/0",
			res[0].Deprecated, res[0].DeprecatedCount)
	}
}

// TestMergeModelInfoBuiltinPriority verifies built-in metadata wins over
// fetched/doc for known models (so a stale /v1/models skeleton never erases
// curated context-window / pricing data), while new models pick up doc info.
func TestMergeModelInfoBuiltinPriority(t *testing.T) {
	builtin := []types.ModelInfo{{
		ID:            "known",
		ContextWindow: 200000, // curated — must survive
	}}
	fetched := []types.ModelInfo{{
		ID:            "known",
		ContextWindow: 0, // API returned no context window
	}, {
		ID: "new",
	}}
	doc := map[string]DocModelInfo{
		"new": {ContextWindow: 128000, SupportsVision: true, Reasoning: true},
	}

	got := mergeModelInfo(fetched, builtin, doc)

	byID := map[string]types.ModelInfo{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if byID["known"].ContextWindow != 200000 {
		t.Fatalf("known model context = %d, want 200000 (builtin preserved)",
			byID["known"].ContextWindow)
	}
	if byID["new"].ContextWindow != 128000 {
		t.Fatalf("new model context = %d, want 128000 (from doc)", byID["new"].ContextWindow)
	}
	if !byID["new"].SupportsVision || !byID["new"].Capabilities.Reasoning {
		t.Fatal("new model doc capabilities (vision/reasoning) not applied")
	}
}
