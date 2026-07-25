package tool

import (
	"context"
	"strings"
	"testing"
)

func TestUseSkillTool_LoadsBodyOnDemand(t *testing.T) {
	loader := func(name string) (body, desc string, ok bool) {
		if name == "pdf" {
			return "# PDF Skill\nSteps to build a PDF.", "Build PDFs", true
		}
		return "", "", false
	}
	tl := NewUseSkillTool(loader)

	// Def advertises the tool.
	if tl.Def().Name != "use_skill" {
		t.Fatalf("unexpected tool name %q", tl.Def().Name)
	}

	// Known skill → full body returned (lands in volatile scratch, not prefix).
	res, err := tl.Execute(context.Background(), `{"name":"pdf"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	if !strings.Contains(res.Content, "# PDF Skill") {
		t.Errorf("expected skill body in result, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "Steps to build a PDF.") {
		t.Errorf("expected skill steps in result, got %q", res.Content)
	}

	// Unknown skill → friendly error, no body leak.
	res2, err := tl.Execute(context.Background(), `{"name":"nope"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res2.Success {
		t.Errorf("expected failure for unknown skill, got %q", res2.Content)
	}
	if strings.Contains(res2.Error, "body") {
		t.Errorf("error should not leak internal body info: %q", res2.Error)
	}
}

func TestUseSkillTool_NilLoader(t *testing.T) {
	tl := NewUseSkillTool(nil)
	res, err := tl.Execute(context.Background(), `{"name":"pdf"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Errorf("expected failure when loader is nil")
	}
}
