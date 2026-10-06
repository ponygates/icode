// Package skills — /skill-doctor (marketplace v1, A4).
//
// Doctor inspects the loaded skill registry plus the on-disk reality around
// it and reports health findings, mirroring the Claude Code /skill-doctor
// convention: frontmatter completeness, trigger coverage, name shadowing
// across interop directories, orphaned user skills that no longer exist in
// the built-in catalog, and eval-suite coverage (reusing evals.go).
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Severity classifies a finding.
type Severity int

const (
	SevOK    Severity = iota // explicit pass, shown as ✓
	SevWarn                  // suboptimal, shown as !
	SevError                 // broken, shown as ✗
)

// DoctorFinding is one health observation about one skill.
type DoctorFinding struct {
	Skill string
	Sev   Severity
	Issue string
	Hint  string // suggested fix, may be empty
}

// DoctorReport aggregates the check-up.
type DoctorReport struct {
	Findings []DoctorFinding

	// Shadowed lists same-name skills where a later directory (iCode user
	// dir) overrides an earlier one (WorkBuddy/mimocode) — usually intended,
	// but the user should know it happens.
	Shadowed []string

	// Orphans lists user-installed skills that are not part of the embedded
	// catalog (imported, remote-installed, or leftovers from an old market
	// build). Informational only.
	Orphans []string

	// CatalogNotInstalled counts market skills available but not installed.
	CatalogNotInstalled int

	// EvalSummary is a human-readable one-liner about eval coverage.
	EvalSummary string

	// Healthy / Warn / Error tally the findings by severity.
	Healthy, Warn, Error int
}

// Doctor runs all skill health checks against the given registry.
func Doctor(reg *Registry) DoctorReport {
	var rep DoctorReport

	if reg == nil || len(reg.List()) == 0 {
		rep.EvalSummary = "无已加载技能，无需体检。"
		return rep
	}

	// ── Per-skill findings ─────────────────────────────────────
	evaluated, evalPass, evalTotal := 0, 0, 0
	for _, s := range reg.List() {
		switch {
		case s.Description == "":
			rep.Findings = append(rep.Findings, DoctorFinding{
				Skill: s.Name, Sev: SevError,
				Issue: "缺少 description",
				Hint:  "模型靠 description 决定是否调用该技能，缺失将几乎不被触发；请在 frontmatter 补上。",
			})
			rep.Error++
			continue
		case len(s.Triggers) == 0:
			rep.Findings = append(rep.Findings, DoctorFinding{
				Skill: s.Name, Sev: SevWarn,
				Issue: "无触发词",
				Hint:  "仅靠 description 模糊匹配；建议在 frontmatter 增加 triggers 列表（/skill-doctor 复查）。",
			})
			rep.Warn++
		default:
			rep.Findings = append(rep.Findings, DoctorFinding{
				Skill: s.Name, Sev: SevOK, Issue: "健康",
			})
			rep.Healthy++
		}

		// Eval coverage rides along for every loaded skill.
		if suite, ok := LoadEval(&s); ok && len(suite.Cases) > 0 {
			r := RunEval(&s, suite)
			evaluated++
			evalPass += r.Passed
			evalTotal += r.Total
			if r.PassRate() < 1 {
				rep.Findings = append(rep.Findings, DoctorFinding{
					Skill: s.Name, Sev: SevWarn,
					Issue: fmt.Sprintf("触发自测 %d/%d 通过", r.Passed, r.Total),
					Hint:  "运行 /skill-eval " + s.Name + " 查看失败用例，调整 description/triggers。",
				})
				rep.Warn++
			}
		}
	}

	// ── Shadowing across interop dirs ──────────────────────────
	if seen := scanShadowing(DefaultDirs()); len(seen) > 0 {
		rep.Shadowed = seen
	}

	// ── Orphans vs the embedded catalog ────────────────────────
	catalogNames := map[string]bool{}
	for _, c := range ListCatalog() {
		catalogNames[c.Name] = true
		if !c.Installed {
			rep.CatalogNotInstalled++
		}
	}
	if dir := userSkillsDir(); dir != "" {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, ent := range entries {
				if !ent.IsDir() {
					continue
				}
				if _, err := os.Stat(filepath.Join(dir, ent.Name(), "SKILL.md")); err != nil {
					continue
				}
				if !catalogNames[ent.Name()] {
					rep.Orphans = append(rep.Orphans, ent.Name())
				}
			}
			sort.Strings(rep.Orphans)
		}
	}

	// ── Eval summary line ──────────────────────────────────────
	switch {
	case evaluated == 0:
		rep.EvalSummary = fmt.Sprintf("无技能携带 evals.yaml（%d 个可用 /skill-eval <名> --scaffold 生成）", len(reg.List()))
	case evalTotal == 0:
		rep.EvalSummary = "evals.yaml 均为空用例。"
	default:
		rep.EvalSummary = fmt.Sprintf("触发自测: %d 个技能有测试，共 %d/%d 用例通过 (%.0f%%)。",
			evaluated, evalPass, evalTotal, float64(evalPass)*100/float64(evalTotal))
	}
	return rep
}

// scanShadowing walks the load directories in order and reports skills whose
// name appears in more than one directory (later dirs win on conflicts).
func scanShadowing(dirs []string) []string {
	owner := map[string]string{} // skill name → first dir that provided it
	var out []string
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, ent.Name(), "SKILL.md")); err != nil {
				continue
			}
			if first, dup := owner[ent.Name()]; dup {
				if first != dir { // same skill, different directory → shadow
					out = append(out, fmt.Sprintf("%s 的 %q 被 %s 覆盖", first, ent.Name(), dir))
				}
			} else {
				owner[ent.Name()] = dir
			}
		}
	}
	sort.Strings(out)
	return out
}
