package searchreplace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageAdd_Valid(t *testing.T) {
	// Create a temp file with known content
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	content := `package main

func main() {
	println("hello")
}
`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	s := &StagingArea{}
	idx, valid, reason := s.Add(filePath, `println("hello")`, `println("world")`)
	if !valid {
		t.Errorf("expected valid edit, got invalid: %s", reason)
	}
	if idx != 0 {
		t.Errorf("expected index 0, got %d", idx)
	}
	if s.Count() != 1 {
		t.Errorf("expected 1 staged edit, got %d", s.Count())
	}

	// Verify Diff is populated
	edits := s.List()
	if edits[0].Diff == "" {
		t.Error("expected Diff to be populated")
	}
}

// TestStageForSession_Isolation verifies that two sessions stage into
// separate areas — the multi-tab desktop regression this fix targets.
func TestStageForSession_Isolation(t *testing.T) {
	a := StageForSession("sess-A")
	b := StageForSession("sess-B")
	if a == b {
		t.Fatal("different sessions must get different staging areas")
	}
	// Empty session ID falls back to the shared default area, so the
	// UI slash commands (/apply /reject) keep working without a session.
	if StageForSession("") != defaultStage {
		t.Fatal("empty session ID must resolve to the default stage")
	}
	if StageForSession("sess-A") != a {
		t.Fatal("same session must resolve to the same area")
	}

	dir := t.TempDir()
	fp := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(fp, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, valid, reason := a.Add(fp, "hello", "hola"); !valid {
		t.Fatalf("expected valid edit, got: %s", reason)
	}
	if b.Count() != 0 {
		t.Fatalf("session B must not see session A's staged edits, got %d", b.Count())
	}
	if a.Count() != 1 {
		t.Fatalf("session A should have 1 staged edit, got %d", a.Count())
	}
}

func TestStageAdd_Invalid_FileNotFound(t *testing.T) {
	s := &StagingArea{}
	_, valid, reason := s.Add("/nonexistent/file.go", "search", "replace")
	if valid {
		t.Error("expected invalid edit for nonexistent file")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestStageAdd_Invalid_SearchNotFound(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	if err := os.WriteFile(filePath, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	s := &StagingArea{}
	_, valid, reason := s.Add(filePath, "nonexistent text", "replace")
	if valid {
		t.Error("expected invalid edit when search text not found")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestStageApplyValid(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	content := `package main

func main() {
	println("hello")
}
`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	s := &StagingArea{}
	s.Add(filePath, `println("hello")`, `println("world")`)

	results := s.ApplyValid()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Verify the file was modified
	data, _ := os.ReadFile(filePath)
	if string(data) != `package main

func main() {
	println("world")
}
` {
		t.Errorf("file content not updated correctly: %s", string(data))
	}

	if s.Count() != 0 {
		t.Errorf("expected 0 remaining edits, got %d", s.Count())
	}
}

func TestStageClear(t *testing.T) {
	s := &StagingArea{}
	s.Add("/tmp/fake.go", "a", "b")
	s.Add("/tmp/fake2.go", "c", "d")
	if s.Count() != 2 {
		t.Fatalf("expected 2 edits, got %d", s.Count())
	}
	s.Clear()
	if s.Count() != 0 {
		t.Errorf("expected 0 edits after clear, got %d", s.Count())
	}
}

func TestUnifiedDiff_NoChanges(t *testing.T) {
	diff := unifiedDiff("test.go", "hello\nworld", "hello\nworld", "hello", "hello")
	if diff == "(no changes)" {
		// This is expected when search == replace
	}
}

func TestUnifiedDiff_WithChanges(t *testing.T) {
	oldText := `line1
line2
line3
line4
line5`
	newText := `line1
line2
modified
line4
line5`
	diff := unifiedDiff("test.go", oldText, newText, "line3", "modified")
	if diff == "" || diff == "(no changes)" {
		t.Fatal("expected a non-empty diff")
	}
	if !contains(diff, "---") || !contains(diff, "+++") {
		t.Error("expected diff headers (---/+++)")
	}
	if !contains(diff, "-line3") {
		t.Error("expected removed line marker")
	}
	if !contains(diff, "+modified") {
		t.Error("expected added line marker")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
