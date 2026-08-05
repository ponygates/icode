package conversation

import "testing"

// TestRememberDiagnostics_Dedup verifies G1: identical LSP diagnostics text for
// a file is injected only once, and a change (or clearing) triggers re-injection.
func TestRememberDiagnostics_Dedup(t *testing.T) {
	e := &Engine{diagCache: make(map[string]string)}

	if !e.rememberDiagnostics("a.go", "err one") {
		t.Fatalf("first occurrence should be new")
	}
	if e.rememberDiagnostics("a.go", "err one") {
		t.Fatalf("identical re-injection should be suppressed")
	}
	if !e.rememberDiagnostics("a.go", "err two") {
		t.Fatalf("changed diagnostics should be new")
	}
	if !e.rememberDiagnostics("b.go", "err one") {
		t.Fatalf("same text on a different file should be new")
	}
}
