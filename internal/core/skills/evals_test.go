package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir, name, frontmatter string) *Skill {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\n" + frontmatter + "\n---\nbody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := Load(dir)
	s, ok := reg.Get(name)
	if !ok {
		t.Fatalf("skill %q not loaded", name)
	}
	return s
}

func TestLoadEvalMissing(t *testing.T) {
	dir := t.TempDir()
	s := writeSkill(t, dir, "plain", "description: x")
	if _, ok := LoadEval(s); ok {
		t.Error("missing evals.yaml should report ok=false")
	}
}

func TestRunEvalPassAndFail(t *testing.T) {
	dir := t.TempDir()
	s := writeSkill(t, dir, "commiter", "description: commit helper\ntriggers:\n  - commit message\n")
	evalYAML := "cases:\n" +
		"  - prompt: \"帮我写个 commit message\"\n    should_trigger: true\n" +
		"  - prompt: \"修复编译错误\"\n    should_trigger: false\n" +
		"  - prompt: \"commit message 规范是什么\"\n    should_trigger: true\n" +
		"  - prompt: \"别管 commit message 了，直接改代码\"\n    should_trigger: false\n" // contains trigger → overtrigger
	if err := os.WriteFile(s.EvalPath(), []byte(evalYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	suite, ok := LoadEval(s)
	if !ok {
		t.Fatal("evals.yaml not loaded")
	}
	rep := RunEval(s, suite)
	if rep.Total != 4 || rep.Passed != 3 {
		t.Fatalf("passed = %d/%d, want 3/4", rep.Passed, rep.Total)
	}
	if got := rep.PassRate(); got < 0.74 || got > 0.76 {
		t.Errorf("passRate = %.2f, want 0.75", got)
	}
	// The failing one is the overtrigger (case 4): prompt contains the
	// trigger so it fires even though it shouldn't.
	last := rep.Cases[3]
	if last.Pass || last.WantFire || !last.GotFire {
		t.Errorf("case4 = %+v, want overtrigger failure", last)
	}
}

func c_last(c EvalCaseResult) bool { return c.WantFire == false && c.GotFire == true }

func TestScaffoldEvalFromTriggers(t *testing.T) {
	dir := t.TempDir()
	s := writeSkill(t, dir, "docs", "description: doc gen\ntriggers:\n  - 写文档\n  - generate docs\n")
	if err := ScaffoldEval(s); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	suite, ok := LoadEval(s)
	if !ok {
		t.Fatal("scaffolded evals not loadable")
	}
	// One positive per trigger + one negative.
	if len(suite.Cases) != 3 {
		t.Fatalf("cases = %d, want 3", len(suite.Cases))
	}
	if suite.Cases[0].Prompt != "请帮我 写文档" || !suite.Cases[0].ShouldTrigger {
		t.Errorf("case0 = %+v", suite.Cases[0])
	}
	// All scaffolded cases must actually pass.
	rep := RunEval(s, suite)
	if rep.Passed != rep.Total {
		t.Errorf("scaffolded evals self-fail: %d/%d", rep.Passed, rep.Total)
	}
	// Second scaffold refuses to overwrite.
	if err := ScaffoldEval(s); err == nil {
		t.Error("re-scaffold should error")
	}
}

func TestRunAllEvalsSkipsUnevaluated(t *testing.T) {
	dir := t.TempDir()
	withEval := writeSkill(t, dir, "with-evals", "description: a\ntriggers: [alpha]\n")
	writeSkill(t, dir, "no-evals", "description: b\ntriggers: [beta]\n")
	if err := os.WriteFile(withEval.EvalPath(), []byte("cases:\n  - prompt: \"use alpha\"\n    should_trigger: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := Load(dir)
	reports := RunAllEvals(reg)
	if len(reports) != 1 || reports[0].SkillName != "with-evals" {
		t.Fatalf("reports = %+v, want only with-evals", reports)
	}
}

func TestSkillFiresCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	s := writeSkill(t, dir, "review", "description: r\ntriggers: [CodeReview]\n")
	// Matching is a case-insensitive substring of the raw trigger.
	if !s.skillFires("please run CODEREVIEW now") {
		t.Error("matching should be case-insensitive substring")
	}
	if s.skillFires("unrelated task") {
		t.Error("should not fire without trigger substring")
	}
}
