package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ── loadFile / Registry ────────────────────────────────────────────

func writeAgent(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFileFullFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "explore.md", `---
description: Read-only searcher
model: deepseek-v3
tools: [read_file, grep]
max_rounds: 12
max_tokens: 2048
---
You are the explore agent.
`)
	def, err := loadFile(filepath.Join(dir, "explore.md"))
	if err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	if def.Name != "explore" {
		t.Errorf("Name = %q", def.Name)
	}
	if def.Description != "Read-only searcher" {
		t.Errorf("Description = %q", def.Description)
	}
	if def.Model != "deepseek-v3" {
		t.Errorf("Model = %q", def.Model)
	}
	if len(def.Tools) != 2 || def.Tools[0] != "read_file" {
		t.Errorf("Tools = %v", def.Tools)
	}
	if def.MaxRounds != 12 || def.MaxTokens != 2048 {
		t.Errorf("MaxRounds=%d MaxTokens=%d", def.MaxRounds, def.MaxTokens)
	}
	if !strings.HasPrefix(def.SystemPrompt, "You are the explore agent.") {
		t.Errorf("SystemPrompt = %q", def.SystemPrompt)
	}
}

func TestLoadFileDefaults(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "bare.md", "Just a prompt, no frontmatter.")
	def, err := loadFile(filepath.Join(dir, "bare.md"))
	if err != nil {
		t.Fatalf("loadFile: %v", err)
	}
	if def.MaxRounds != 8 {
		t.Errorf("MaxRounds default = %d, want 8", def.MaxRounds)
	}
	if def.MaxTokens != 4096 {
		t.Errorf("MaxTokens default = %d, want 4096", def.MaxTokens)
	}
	if def.Description != "Sub-agent: bare" {
		t.Errorf("Description fallback = %q", def.Description)
	}
	if def.SystemPrompt != "Just a prompt, no frontmatter." {
		t.Errorf("SystemPrompt = %q", def.SystemPrompt)
	}
}

func TestLoadLaterDirOverrides(t *testing.T) {
	user := filepath.Join(t.TempDir(), "user")
	proj := filepath.Join(t.TempDir(), "proj")
	writeAgent(t, user, "x.md", "---\ndescription: from user\n---\nuser prompt")
	writeAgent(t, proj, "x.md", "---\ndescription: from project\n---\nproject prompt")

	r := Load(user, proj)
	def, ok := r.Get("X") // case-insensitive
	if !ok {
		t.Fatal("agent x not found")
	}
	if def.Description != "from project" {
		t.Errorf("project should override user; got %q", def.Description)
	}
}

func TestRegistryGetCaseInsensitiveAndMissing(t *testing.T) {
	r := NewRegistry()
	r.byName["explore"] = &AgentDef{Name: "explore"}
	if _, ok := r.Get("EXPLORE"); !ok {
		t.Error("Get should be case-insensitive")
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("Get for missing agent should return false")
	}
}

func TestRegisterDefaultsDoesNotOverrideFiles(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "explore.md", "---\ndescription: custom explore\n---\ncustom")
	r := Load(dir)
	r.RegisterDefaults()

	e, _ := r.Get("explore")
	if e.Description != "custom explore" {
		t.Errorf("file-based def overridden by default: %q", e.Description)
	}
	// Built-ins absent from files must be added.
	if _, ok := r.Get("plan"); !ok {
		t.Error("default 'plan' not registered")
	}
	if _, ok := r.Get("general"); !ok {
		t.Error("default 'general' not registered")
	}
}

func TestDefaultAgentDefsComplete(t *testing.T) {
	defs := DefaultAgentDefs()
	if len(defs) < 3 {
		t.Fatalf("want at least 3 built-in agents, got %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
		if d.SystemPrompt == "" {
			t.Errorf("agent %q has empty SystemPrompt", d.Name)
		}
		if d.Description == "" {
			t.Errorf("agent %q has empty Description", d.Name)
		}
	}
	for _, want := range []string{"explore", "plan", "general"} {
		if !names[want] {
			t.Errorf("missing built-in agent %q", want)
		}
	}
	// explore is read-only: its tool list must contain no write tools.
	for _, d := range defs {
		if d.Name != "explore" {
			continue
		}
		for _, tool := range d.Tools {
			switch tool {
			case "edit_file", "write_file", "bash":
				t.Errorf("explore agent has write-capable tool %q", tool)
			}
		}
	}
}

func TestLoadSkipsNonMarkdownAndDirs(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "a.md", "a")
	writeAgent(t, dir, "b.txt", "not an agent")
	if err := os.MkdirAll(filepath.Join(dir, "sub.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Load(dir, "")
	if got := len(r.List()); got != 1 {
		t.Errorf("List() = %d agents, want 1", got)
	}
}

// ── parsePlan ──────────────────────────────────────────────────────

var planMembers = []TeamMember{
	{Name: "alpha"}, {Name: "beta"},
}

func TestParsePlanMemberTaskFormat(t *testing.T) {
	plan := "MEMBER: alpha | TASK: scan auth code\nMEMBER: beta | TASK: scan db layer\ngarbage line"
	tasks := parsePlan(plan, planMembers)
	if tasks["alpha"] != "scan auth code" || tasks["beta"] != "scan db layer" {
		t.Errorf("tasks = %v", tasks)
	}
}

func TestParsePlanColonFallback(t *testing.T) {
	tasks := parsePlan("MEMBER: alpha : review the router", planMembers)
	if tasks["alpha"] != "review the router" {
		t.Errorf("colon-separated fallback failed: %v", tasks)
	}
}

func TestParsePlanBulletWithMemberMention(t *testing.T) {
	plan := "- alpha: check error handling\n* beta: profile hot loops"
	tasks := parsePlan(plan, planMembers)
	if tasks["alpha"] != "check error handling" || tasks["beta"] != "profile hot loops" {
		t.Errorf("bullet parsing failed: %v", tasks)
	}
}

func TestParsePlanMemberPrefixLine(t *testing.T) {
	tasks := parsePlan("beta: do the thing", planMembers)
	if tasks["beta"] != "do the thing" {
		t.Errorf("prefix parsing failed: %v", tasks)
	}
}

func TestParsePlanIgnoresEmptyTask(t *testing.T) {
	tasks := parsePlan("MEMBER: ghost | TASK: nothing\nMEMBER: alpha | TASK:", planMembers)
	if _, ok := tasks["alpha"]; ok {
		t.Error("empty task should be ignored")
	}
	// Unknown members stay in the parsed plan on purpose; TeamRunner.Run
	// filters them out later via findMember.
	if tasks["ghost"] != "nothing" {
		t.Errorf("unknown member should be preserved: %v", tasks)
	}
}

func TestFindMember(t *testing.T) {
	members := []TeamMember{{Name: "sec", AgentDef: AgentDef{Name: "security"}}}
	if findMember(members, "sec") == nil {
		t.Error("existing member not found")
	}
	if findMember(members, "other") != nil {
		t.Error("missing member should return nil")
	}
}

// ── Team file loading ──────────────────────────────────────────────

const sampleTeamYAML = `name: review
description: multi-perspective review
leader:
  name: lead
  system_prompt: |
    You lead.
members:
  - name: security
    role: reviewer
    agent:
      system_prompt: |
        Security expert.
  - name: perf
    agent:
      system_prompt: Perf expert.
      max_rounds: 3
`

func TestToTeamDefDefaults(t *testing.T) {
	var tf teamFile
	if err := yaml.Unmarshal([]byte(sampleTeamYAML), &tf); err != nil {
		t.Fatal(err)
	}
	def := tf.toTeamDef()
	if def.Leader.MaxRounds != 8 || def.Leader.MaxTokens != 4096 {
		t.Errorf("leader defaults: rounds=%d tokens=%d", def.Leader.MaxRounds, def.Leader.MaxTokens)
	}
	if def.Leader.Description != "Team leader: review" {
		t.Errorf("leader description fallback = %q", def.Leader.Description)
	}
	if len(def.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(def.Members))
	}
	// Missing role falls back to specialist.
	if def.Members[1].Role != RoleSpecialist {
		t.Errorf("role fallback = %q, want specialist", def.Members[1].Role)
	}
	if def.Members[0].Role != RoleReviewer {
		t.Errorf("explicit role lost: %q", def.Members[0].Role)
	}
	if def.Members[1].AgentDef.MaxRounds != 3 {
		t.Errorf("member max_rounds = %d, want 3", def.Members[1].AgentDef.MaxRounds)
	}
	if def.Members[0].AgentDef.Description != "Team member: security" {
		t.Errorf("member description fallback = %q", def.Members[0].AgentDef.Description)
	}
}

func TestLoadTeamsYAMLAndOverride(t *testing.T) {
	user := filepath.Join(t.TempDir(), "teams-user")
	proj := filepath.Join(t.TempDir(), "teams-proj")
	writeAgent(t, user, "review.yaml", sampleTeamYAML)
	writeAgent(t, proj, "review.yml", strings.Replace(sampleTeamYAML,
		"description: multi-perspective review", "description: project variant", 1))
	writeAgent(t, proj, "broken.yaml", "{{{ not yaml")
	writeAgent(t, proj, "ignored.txt", "nope")

	teams := LoadTeams(user, proj)
	byName := map[string]*TeamDef{}
	for _, tm := range teams {
		byName[tm.Name] = tm
	}
	rv, ok := byName["review"]
	if !ok {
		t.Fatal("team 'review' not loaded")
	}
	if rv.Description != "project variant" {
		t.Errorf("project should override user team; got %q", rv.Description)
	}
	if _, ok := byName["broken"]; ok {
		t.Error("malformed yaml should be skipped")
	}
}

func TestLoadTeamsFilenameFallback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "teams")
	writeAgent(t, dir, "noname.yaml", "leader:\n  system_prompt: x\n")
	teams := LoadTeams(dir)
	if len(teams) != 1 || teams[0].Name != "noname" {
		t.Errorf("filename fallback failed: %+v", teams)
	}
}

func TestDefaultTeamDefsShape(t *testing.T) {
	teams := DefaultTeamDefs()
	if len(teams) == 0 {
		t.Fatal("no default teams")
	}
	for _, tm := range teams {
		if tm.Leader.SystemPrompt == "" {
			t.Errorf("team %q leader has empty prompt", tm.Name)
		}
		for _, m := range tm.Members {
			if m.AgentDef.SystemPrompt == "" {
				t.Errorf("team %q member %q has empty prompt", tm.Name, m.Name)
			}
		}
	}
}
