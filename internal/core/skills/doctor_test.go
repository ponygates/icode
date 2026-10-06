package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustWriteSkill writes a SKILL.md with the given frontmatter into
// <dir>/<name>/SKILL.md, creating directories as needed.
func mustWriteSkill(t *testing.T, dir, name, frontmatter string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\n" + frontmatter + "\n---\n# " + name + "\nbody\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDoctor_EmptyRegistry(t *testing.T) {
	rep := Doctor(NewRegistry())
	if len(rep.Findings) != 0 {
		t.Errorf("expected no findings for empty registry, got %+v", rep.Findings)
	}
	if !strings.Contains(rep.EvalSummary, "无已加载技能") {
		t.Errorf("EvalSummary = %q, want the empty-registry hint", rep.EvalSummary)
	}
}

func TestDoctor_Findings(t *testing.T) {
	isolateHome(t) // keep DefaultDirs()/catalog checks away from the real home
	tmp := t.TempDir()

	mustWriteSkill(t, tmp, "healthy", "name: healthy\ndescription: has it all\ntriggers:\n  - alpha")
	mustWriteSkill(t, tmp, "no-triggers", "name: no-triggers\ndescription: missing triggers")
	mustWriteSkill(t, tmp, "no-desc", "name: no-desc")

	reg := Load(tmp)
	if len(reg.List()) != 3 {
		t.Fatalf("expected 3 skills loaded, got %d", len(reg.List()))
	}

	rep := Doctor(reg)
	if rep.Healthy != 1 || rep.Warn != 1 || rep.Error != 1 {
		t.Errorf("tallies = healthy:%d warn:%d error:%d, want 1/1/1 (findings: %+v)",
			rep.Healthy, rep.Warn, rep.Error, rep.Findings)
	}
	// The exact issues must be reported with their skill names.
	bySkill := map[string]DoctorFinding{}
	for _, f := range rep.Findings {
		bySkill[f.Skill] = f
	}
	if f := bySkill["no-desc"]; f.Sev != SevError || !strings.Contains(f.Issue, "description") {
		t.Errorf("no-desc finding = %+v, want SevError about missing description", f)
	}
	if f := bySkill["no-triggers"]; f.Sev != SevWarn || !strings.Contains(f.Issue, "触发词") {
		t.Errorf("no-triggers finding = %+v, want SevWarn about missing triggers", f)
	}
	if f := bySkill["healthy"]; f.Sev != SevOK {
		t.Errorf("healthy finding = %+v, want SevOK", f)
	}
	if !strings.Contains(rep.EvalSummary, "evals.yaml") {
		t.Errorf("EvalSummary = %q, want it to mention missing evals.yaml", rep.EvalSummary)
	}
}

func TestDoctor_Shadowing(t *testing.T) {
	home := isolateHome(t)

	// The same skill name in two interop directories: the iCode dir (later
	// in DefaultDirs) must be reported as shadowing the WorkBuddy one.
	wb := filepath.Join(home, ".workbuddy", "skills")
	ic := filepath.Join(home, ".icode", "skills")
	mustWriteSkill(t, wb, "dup", "name: dup\ndescription: workbuddy copy")
	mustWriteSkill(t, ic, "dup", "name: dup\ndescription: icode copy")

	reg := Load(DefaultDirs()...)
	rep := Doctor(reg)
	found := false
	for _, s := range rep.Shadowed {
		if strings.Contains(s, "dup") && strings.Contains(s, "覆盖") {
			found = true
		}
	}
	if !found {
		t.Errorf("shadowing not reported: %+v", rep.Shadowed)
	}
}

func TestDoctor_Orphans(t *testing.T) {
	home := isolateHome(t)

	// A user-installed skill that is not in the embedded catalog.
	mustWriteSkill(t, filepath.Join(home, ".icode", "skills"), "ghost-skill",
		"name: ghost-skill\ndescription: not in catalog")

	reg := Load(DefaultDirs()...)
	rep := Doctor(reg)
	found := false
	for _, o := range rep.Orphans {
		if o == "ghost-skill" {
			found = true
		}
	}
	if !found {
		t.Errorf("ghost-skill not reported as orphan: %+v", rep.Orphans)
	}
	// Catalog skills installed into the user dir must NOT be flagged.
	if len(rep.Orphans) > 5 {
		t.Errorf("too many orphans reported — did isolation leak? %+v", rep.Orphans)
	}
}
