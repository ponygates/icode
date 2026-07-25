package router

import "testing"

// TestSemanticClassifier_Accuracy checks that paraphrased queries (not verbatim
// exemplars) land in the right complexity bucket via cosine similarity.
func TestSemanticClassifier_Accuracy(t *testing.T) {
	sc := NewSemanticClassifier()
	tests := []struct {
		query string
		want  Complexity
	}{
		// simple — conceptual questions, no code change
		{"can you explain what a channel is", ComplexitySimple},
		{"这个错误是什么意思呢", ComplexitySimple},
		// normal — a single concrete coding task
		{"write a helper to parse a config file", ComplexityNormal},
		{"帮我实现一个简单的快速排序函数", ComplexityNormal},
		// complex — sweeping, multi-step work
		{"refactor the entire module and split into separate packages", ComplexityComplex},
		{"深度分析并发模型并优化整个项目的性能热点", ComplexityComplex},
	}
	correct := 0
	for _, tt := range tests {
		got, ok := sc.Classify(tt.query)
		if ok && got == tt.want {
			correct++
		} else {
			t.Logf("query %q -> got=%v ok=%v want=%v", tt.query, got, ok, tt.want)
		}
	}
	// Allow a small tolerance but demand strong majority accuracy.
	if correct < len(tests)-1 {
		t.Fatalf("semantic accuracy too low: %d/%d correct", correct, len(tests))
	}
}

// TestSemanticClassifier_LowConfidence ensures ambiguous / empty input is not
// trusted, so the router safely falls back to the keyword heuristic.
func TestSemanticClassifier_LowConfidence(t *testing.T) {
	sc := NewSemanticClassifier()
	for _, q := range []string{"", "   ", "!!!"} {
		if _, ok := sc.Classify(q); ok {
			t.Errorf("expected low confidence (ok=false) for %q", q)
		}
	}
}

// TestRouteQuery_EmbeddingRefines verifies the embedding classifier is applied
// on the routing hot path and can override the keyword baseline.
func TestRouteQuery_EmbeddingRefines(t *testing.T) {
	r := New(Config{
		DefaultModel: "default-m", DefaultProv: "p",
		CheapModel: "cheap-m", CheapProv: "p",
		PowerfulModel: "power-m", PowerfulProv: "p",
	})
	r.SetEmbeddingClassifier(NewSemanticClassifier().Classify)

	// A sweeping refactor request should route to the powerful model.
	route := r.RouteQuery("refactor the entire authentication architecture across all packages", 0)
	if route.ModelID != "power-m" {
		t.Errorf("expected powerful model for complex query, got %q (%s)", route.ModelID, route.Reason)
	}
}

// TestFeaturize_Normalized confirms feature vectors are unit length after
// normalize (cosine == dot precondition).
func TestFeaturize_Normalized(t *testing.T) {
	v := featurize("refactor the module")
	if len(v) == 0 {
		t.Fatal("expected non-empty feature vector")
	}
	normalize(v)
	var sum float64
	for _, val := range v {
		sum += val * val
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("expected unit L2 norm, got %f", sum)
	}
}
