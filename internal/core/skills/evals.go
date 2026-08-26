package skills

// Skill Evals — regression tests for trigger accuracy (Claude Code
// skill-creator parity, offline and zero-token). A skill ships an optional
// evals.yaml next to its SKILL.md:
//
//	cases:
//	  - prompt: "帮我写个 commit message"
//	    should_trigger: true
//	  - prompt: "修复编译错误"
//	    should_trigger: false
//
// /skill-eval replays each prompt through the same matcher the engine uses
// at runtime and reports pass rate, so editing a description/triggers can be
// verified instead of guessed.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// EvalCase is one trigger-accuracy test: a user prompt plus whether the
// skill SHOULD fire for it.
type EvalCase struct {
	Prompt        string `yaml:"prompt"`
	ShouldTrigger bool   `yaml:"should_trigger"`
}

// EvalSuite is the on-disk evals.yaml.
type EvalSuite struct {
	Cases []EvalCase `yaml:"cases"`
}

// EvalCaseResult captures one case's outcome.
type EvalCaseResult struct {
	Prompt   string
	WantFire bool
	GotFire  bool
	Pass     bool
}

// EvalReport summarises one skill's run.
type EvalReport struct {
	SkillName string
	Total     int
	Passed    int
	Cases     []EvalCaseResult
}

// PassRate returns 0..1 (1 when no cases — nothing to fail).
func (r EvalReport) PassRate() float64 {
	if r.Total == 0 {
		return 1
	}
	return float64(r.Passed) / float64(r.Total)
}

// EvalPath returns the evals.yaml path for a skill's Source location.
func (s *Skill) EvalPath() string {
	return filepath.Join(filepath.Dir(s.Source), "evals.yaml")
}

// LoadEval reads a skill's eval suite; ok=false when absent.
func LoadEval(s *Skill) (*EvalSuite, bool) {
	data, err := os.ReadFile(s.EvalPath())
	if err != nil {
		return nil, false
	}
	var suite EvalSuite
	if err := yaml.Unmarshal(data, &suite); err != nil {
		return nil, false
	}
	return &suite, true
}

// skillFires reports whether this single skill's triggers match the query,
// mirroring Registry.Find's runtime matching (any trigger substring hit).
func (s *Skill) skillFires(query string) bool {
	q := strings.ToLower(query)
	for _, t := range s.Triggers {
		if t != "" && strings.Contains(q, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

// RunEval replays every case against this skill's runtime matcher.
func RunEval(s *Skill, suite *EvalSuite) EvalReport {
	rep := EvalReport{SkillName: s.Name, Total: len(suite.Cases)}
	for _, c := range suite.Cases {
		got := s.skillFires(c.Prompt)
		pass := got == c.ShouldTrigger
		rep.Cases = append(rep.Cases, EvalCaseResult{
			Prompt: c.Prompt, WantFire: c.ShouldTrigger, GotFire: got, Pass: pass,
		})
		if pass {
			rep.Passed++
		}
	}
	return rep
}

// ScaffoldEval writes a starter evals.yaml derived from the skill's own
// triggers (positive case per trigger) plus one generic negative case. It
// never overwrites an existing file.
func ScaffoldEval(s *Skill) error {
	path := s.EvalPath()
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("evals already exist: %s", path)
	}
	var b strings.Builder
	b.WriteString("# Trigger-accuracy evals for this skill.\n")
	b.WriteString("# prompt: sample user input; should_trigger: expected match.\n")
	b.WriteString("cases:\n")
	trigs := s.Triggers
	if len(trigs) == 0 {
		trigs = []string{s.Name}
	}
	for _, t := range trigs {
		fmt.Fprintf(&b, "  - prompt: %q\n    should_trigger: true\n", "请帮我 "+t)
	}
	b.WriteString("  - prompt: \"一个完全无关的任务\"\n    should_trigger: false\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// RunAllEvals evaluates every registered skill that ships an evals.yaml,
// sorted by name for stable output.
func RunAllEvals(r *Registry) []EvalReport {
	var out []EvalReport
	list := r.List()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	for i := range list {
		s := &list[i]
		suite, ok := LoadEval(s)
		if !ok || len(suite.Cases) == 0 {
			continue
		}
		out = append(out, RunEval(s, suite))
	}
	return out
}
