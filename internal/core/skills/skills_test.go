package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(r.List()) != 0 {
		t.Errorf("expected empty registry, got %d skills", len(r.List()))
	}
}

func TestLoad_EmptyDirs(t *testing.T) {
	r := Load()
	if r == nil {
		t.Fatal("Load returned nil")
	}
}

func TestLoad_SingleSkill(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	skillContent := `---
name: test-skill
description: A test skill
triggers:
  - test
  - example
---
# Test Skill
This is a test skill.
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644)

	r := Load(dir)
	skills := r.List()
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got %q", skills[0].Name)
	}
	if skills[0].Description != "A test skill" {
		t.Errorf("expected description 'A test skill', got %q", skills[0].Description)
	}
}

func TestLoad_SkillWithoutFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("Just some content"), 0644)

	r := Load(dir)
	skills := r.List()
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "my-skill" {
		t.Errorf("expected name from directory, got %q", skills[0].Name)
	}
}

func TestGet(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "get-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("content"), 0644)

	r := Load(dir)
	skill, ok := r.Get("get-skill")
	if !ok {
		t.Fatal("expected to find skill")
	}
	if skill == nil {
		t.Fatal("skill is nil")
	}
	if skill.Name != "get-skill" {
		t.Errorf("expected 'get-skill', got %q", skill.Name)
	}

	// Non-existent skill
	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("expected false for nonexistent skill")
	}
}

func TestFind_ByTrigger(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "find-skill")
	os.MkdirAll(skillDir, 0755)
	skillContent := `---
name: find-skill
description: Findable skill
triggers:
  - database
  - sql
---
Do database work
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644)

	r := Load(dir)
	results := r.Find("I need to query the database")
	if len(results) == 0 {
		t.Fatal("expected to find skill by trigger")
	}
	if results[0].Name != "find-skill" {
		t.Errorf("expected 'find-skill', got %q", results[0].Name)
	}
}

func TestFind_NoMatch(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "no-match")
	os.MkdirAll(skillDir, 0755)
	skillContent := `---
name: no-match
triggers:
  - kubernetes
---
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644)

	r := Load(dir)
	results := r.Find("javascript")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestFormatSystemPrompt_Empty(t *testing.T) {
	result := FormatSystemPrompt(nil)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}

	result = FormatSystemPrompt([]*Skill{})
	if result != "" {
		t.Errorf("expected empty string for empty slice, got %q", result)
	}
}

func TestFormatSystemPrompt_WithSkills(t *testing.T) {
	skills := []*Skill{
		{
			Name: "test-skill",
			Body: "Do something useful",
		},
	}
	result := FormatSystemPrompt(skills)
	if !contains(result, "test-skill") {
		t.Error("expected skill name in formatted output")
	}
	if !contains(result, "Do something useful") {
		t.Error("expected skill body in formatted output")
	}
}

func TestFormatIndex_Compact(t *testing.T) {
	// The compact index must list skills WITHOUT embedding their full body.
	// Embedding bodies in the immutable prefix would bloat it and invalidate
	// the provider's KV cache as the skill set grows — defeating the
	// token-saving mechanism. This test guards that contract.
	skills := []*Skill{
		{Name: "test-skill", Description: "Does useful things", Triggers: []string{"useful", "demo"}, Body: strings.Repeat("very long body that must NOT appear in the prefix ", 50)},
	}
	result := FormatIndex(skills)
	if !contains(result, "test-skill") {
		t.Error("expected skill name in compact index")
	}
	if !contains(result, "Does useful things") {
		t.Error("expected skill description in compact index")
	}
	if !contains(result, "useful") {
		t.Error("expected triggers in compact index")
	}
	if contains(result, "very long body") {
		t.Error("FormatIndex must NOT embed the full skill body — that defeats cache stability")
	}
	if !contains(result, "use_skill") {
		t.Error("compact index should instruct the model to call use_skill for full instructions")
	}
}

func TestDefaultDirs(t *testing.T) {
	dirs := DefaultDirs()
	if len(dirs) == 0 {
		t.Error("expected at least one default dir")
	}
	for _, d := range dirs {
		if d == "" {
			t.Error("expected non-empty dir")
		}
	}
}

// TestDefaultDirsMimocodeInterop locks in mimocode skill interop: both
// user-level and project-level .mimocode/skills must be present, and each must
// sort before the corresponding .icode/skills dir (iCode wins name conflicts).
func TestDefaultDirsMimocodeInterop(t *testing.T) {
	dirs := DefaultDirs()
	var mimoIdx, icodeIdx []int
	for i, d := range dirs {
		slash := filepath.ToSlash(d)
		if strings.HasSuffix(slash, ".mimocode/skills") {
			mimoIdx = append(mimoIdx, i)
		}
		if strings.HasSuffix(slash, ".icode/skills") {
			icodeIdx = append(icodeIdx, i)
		}
	}
	if len(mimoIdx) != 2 {
		t.Fatalf("expected 2 .mimocode/skills dirs (user+project), got %d in %v", len(mimoIdx), dirs)
	}
	if len(icodeIdx) != 2 {
		t.Fatalf("expected 2 .icode/skills dirs (user+project), got %d in %v", len(icodeIdx), dirs)
	}
	for k := 0; k < 2; k++ {
		if mimoIdx[k] > icodeIdx[k] {
			t.Errorf(".mimocode/skills should sort before .icode/skills (iCode wins conflicts): %v", dirs)
		}
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

func TestListCatalog_Embedded(t *testing.T) {
	got := ListCatalog()
	if len(got) == 0 {
		t.Fatal("expected at least one built-in catalog skill, got none (embed may have failed)")
	}
	names := map[string]bool{}
	for _, s := range got {
		if s.Name == "" {
			t.Error("catalog skill with empty name")
		}
		if s.Description == "" {
			t.Errorf("catalog skill %q missing description", s.Name)
		}
		names[s.Name] = true
	}
	if !names["code-review"] {
		t.Error("expected built-in 'code-review' skill in catalog")
	}
}

func TestInstall_UnknownReturnsError(t *testing.T) {
	// Installing an unknown skill must fail without touching disk (this also
	// exercises the path.Join fix for embed.FS reads on Windows).
	if err := Install("no-such-skill-xyz"); err == nil {
		t.Error("expected error installing unknown skill")
	}
}

func TestIsInstalled_KnownName(t *testing.T) {
	// Must not panic and must return a boolean; we don't assert the value
	// since it depends on the user's home skills directory.
	_ = IsInstalled("code-review")
}