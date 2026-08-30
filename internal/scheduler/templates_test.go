package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuiltinTemplatesValid(t *testing.T) {
	seen := map[string]bool{}
	for _, tpl := range BuiltinTemplates() {
		if tpl.ID == "" || tpl.Name == "" || tpl.Prompt == "" || tpl.Icon == "" {
			t.Fatalf("template %q has empty required fields", tpl.ID)
		}
		if seen[tpl.ID] {
			t.Fatalf("duplicate template id %q", tpl.ID)
		}
		seen[tpl.ID] = true
		if _, err := nextRunAfter(tpl.Schedule, time.Now(), ""); err != nil {
			t.Errorf("template %q schedule %q invalid: %v", tpl.ID, tpl.Schedule, err)
		}
		if len([]rune(tpl.Prompt)) < 20 {
			t.Errorf("template %q prompt suspiciously short: %q", tpl.ID, tpl.Prompt)
		}
	}
	if len(BuiltinTemplates()) < 8 {
		t.Fatalf("expected at least 8 builtin templates, got %d", len(BuiltinTemplates()))
	}
}

func TestCustomTemplateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	prev := templatesPathFn
	templatesPathFn = func() string { return filepath.Join(dir, "automation_templates.json") }
	t.Cleanup(func() { templatesPathFn = prev })

	saved, err := SaveCustomTemplate(Template{Name: "测试模板", Prompt: "做一个测试任务的提示词，长度足够。", Schedule: "every:2h", Desc: "d"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.ID == "" || !saved.Custom {
		t.Fatalf("saved template should get an ID and Custom=true, got %+v", saved)
	}

	list, err := LoadCustomTemplates()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(list) != 1 || list[0].Name != "测试模板" {
		t.Fatalf("expected 1 saved template, got %+v", list)
	}

	if err := DeleteCustomTemplate(saved.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err = LoadCustomTemplates()
	if err != nil || len(list) != 0 {
		t.Fatalf("expected empty list after delete, got %+v (err=%v)", list, err)
	}
	if err := DeleteCustomTemplate(saved.ID); err == nil {
		t.Fatal("deleting a missing template should error")
	}

	// invalid schedules are rejected at save time
	if _, err := SaveCustomTemplate(Template{Name: "x", Prompt: "提示词内容长度足够。", Schedule: "nope"}); err == nil {
		t.Fatal("invalid schedule should be rejected")
	}
	// missing file counts as no templates
	if _, err := os.Stat(templatesPath()); err == nil {
		// file may exist but be an empty list — LoadCustomTemplates must still succeed
	}
	if _, err := LoadCustomTemplates(); err != nil {
		t.Fatalf("load after delete-all: %v", err)
	}
}
