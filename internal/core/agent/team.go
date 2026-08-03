// Package agent implements sub-agent orchestration.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// TeamRole defines the role an agent plays in a team.
type TeamRole string

const (
	RoleLeader    TeamRole = "leader"
	RoleSpecialist TeamRole = "specialist"
	RoleReviewer  TeamRole = "reviewer"
)

// TeamDef defines a multi-agent team.
type TeamDef struct {
	Name        string
	Description string
	Leader      AgentDef
	Members     []TeamMember
}

// TeamMember is a member of an agent team.
type TeamMember struct {
	Name     string
	Role     TeamRole
	AgentDef AgentDef
}

// TeamResult captures the output of a team run.
type TeamResult struct {
	Name         string
	LeaderOutput string
	MemberOutputs map[string]string
	TotalTokens  int
	Duration     time.Duration
	Errors       []string
}

// TeamRunner orchestrates multi-agent teams.
type TeamRunner struct {
	runner *Runner
	mu     sync.Mutex
}

// NewTeamRunner creates a team orchestrator wrapping a Runner.
func NewTeamRunner(r *Runner) *TeamRunner {
	return &TeamRunner{runner: r}
}

// Run executes a team: the leader decomposes the task, delegates to
// specialists, collects results, and produces a final response.
func (tr *TeamRunner) Run(ctx context.Context, def *TeamDef, input string) (*TeamResult, error) {
	start := time.Now()
	result := &TeamResult{
		Name:          def.Name,
		MemberOutputs: make(map[string]string),
	}

	// Step 1: Leader decomposes the task.
	decompPrompt := fmt.Sprintf(`You are the leader of a team of AI agents.
Team: %s
Description: %s

Your team members:
%s

User request: %s

Decompose this task into subtasks that can be worked on in parallel.
For each subtask, specify:
1. Which team member should handle it
2. What exactly they should do

Return a structured plan with one subtask per line:
MEMBER: <member_name> | TASK: <detailed instructions>`,
		def.Name, def.Description, formatMemberList(def.Members), input)

	leaderDef := &def.Leader
	plan, _, err := tr.runner.Run(ctx, leaderDef, decompPrompt)
	if err != nil {
		return nil, fmt.Errorf("leader decomposition failed: %w", err)
	}
	result.LeaderOutput = plan

	// Step 2: Parse plan into member tasks.
	memberTasks := parsePlan(plan, def.Members)
	if len(memberTasks) == 0 {
		result.Duration = time.Since(start)
		result.Errors = append(result.Errors, "team plan parsing yielded no tasks; returning leader output only")
		return result, nil
	}

	// Step 3: Run specialists in parallel.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string

	for memberName, task := range memberTasks {
		memberDef := findMember(def.Members, memberName)
		if memberDef == nil {
			continue
		}
		wg.Add(1)
		go func(name, tsk string, ad *AgentDef) {
			defer wg.Done()
			// A panic in a member agent must not hang wg.Wait.
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					errs = append(errs, fmt.Sprintf("%s: agent panic（已恢复）: %v", name, r))
					mu.Unlock()
				}
			}()
			output, _, err := tr.runner.Run(ctx, ad, tsk)
			mu.Lock()
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			} else {
				result.MemberOutputs[name] = output
			}
			mu.Unlock()
		}(memberName, task, memberDef)
	}
	wg.Wait()

	result.Errors = errs
	result.Duration = time.Since(start)

	// Step 4: If there's a leader, synthesize final response.
	if len(result.MemberOutputs) > 0 && leaderDef != nil {
		synthPrompt := fmt.Sprintf(`You led a team working on: %s

Here was your plan:
%s

Here are the results from your team members:
%s

Synthesize a final, coherent response for the user.`,
			input, plan, formatMemberOutputs(result.MemberOutputs))

		synth, _, err := tr.runner.Run(ctx, leaderDef, synthPrompt)
		if err == nil {
			result.LeaderOutput = synth
		}
	}

	return result, nil
}

func formatMemberList(members []TeamMember) string {
	var b strings.Builder
	for _, m := range members {
		b.WriteString(fmt.Sprintf("- %s (%s): %s\n", m.Name, m.Role, m.AgentDef.SystemPrompt[:min(80, len(m.AgentDef.SystemPrompt))]))
	}
	return b.String()
}

func formatMemberOutputs(outputs map[string]string) string {
	var b strings.Builder
	for name, out := range outputs {
		b.WriteString(fmt.Sprintf("\n=== %s ===\n%s\n", name, out))
	}
	return b.String()
}

func parsePlan(plan string, members []TeamMember) map[string]string {
	tasks := make(map[string]string)
	lines := strings.Split(plan, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		origLine := line

		if strings.HasPrefix(line, "MEMBER:") {
			rest := line[len("MEMBER:"):]
			parts := strings.SplitN(rest, "|", 2)
			if len(parts) < 2 {
				parts = strings.SplitN(rest, ":", 2)
			}
			if len(parts) < 2 {
				continue
			}
			name := strings.TrimSpace(parts[0])
			task := strings.TrimSpace(parts[1])
			if strings.HasPrefix(task, "TASK:") {
				task = strings.TrimSpace(task[5:])
			}
			if name != "" && task != "" {
				tasks[name] = task
			}
			continue
		}

		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			content := strings.TrimPrefix(origLine, "- ")
			content = strings.TrimPrefix(content, "* ")
			for _, m := range members {
				if strings.Contains(content, m.Name) {
					taskPart := content
					if idx := strings.Index(content, ":"); idx >= 0 {
						taskPart = strings.TrimSpace(content[idx+1:])
					}
					if taskPart != "" {
						tasks[m.Name] = taskPart
					}
					break
				}
			}
		}

		for _, m := range members {
			prefix := m.Name + ":"
			if strings.HasPrefix(line, prefix) {
				task := strings.TrimSpace(line[len(prefix):])
				if task != "" {
					tasks[m.Name] = task
				}
			}
		}
	}
	return tasks
}

func findMember(members []TeamMember, name string) *AgentDef {
	for _, m := range members {
		if m.Name == name {
			return &m.AgentDef
		}
	}
	return nil
}

// ── Team file loading ─────────────────────────────────────────────

// teamFile is the on-disk YAML representation of a TeamDef.
//
//	---
//	name: review
//	description: 多视角代码审查团队
//	leader:
//	  name: lead
//	  description: 审查协调者
//	  model: deepseek-chat
//	  system_prompt: |
//	    You are the review lead...
//	members:
//	  - name: security
//	    role: reviewer
//	    agent:
//	      name: security
//	      description: 安全审查专家
//	      system_prompt: |
//	        ...
type teamFile struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Leader      struct {
		Name         string   `yaml:"name"`
		Description  string   `yaml:"description"`
		Model        string   `yaml:"model"`
		SystemPrompt string   `yaml:"system_prompt"`
		Tools        []string `yaml:"tools"`
		MaxRounds    int      `yaml:"max_rounds"`
		MaxTokens    int      `yaml:"max_tokens"`
	} `yaml:"leader"`
	Members []struct {
		Name string `yaml:"name"`
		Role string `yaml:"role"`
		Agent struct {
			Name         string   `yaml:"name"`
			Description  string   `yaml:"description"`
			Model        string   `yaml:"model"`
			SystemPrompt string   `yaml:"system_prompt"`
			Tools        []string `yaml:"tools"`
			MaxRounds    int      `yaml:"max_rounds"`
			MaxTokens    int      `yaml:"max_tokens"`
		} `yaml:"agent"`
	} `yaml:"members"`
}

func (tf *teamFile) toTeamDef() *TeamDef {
	def := &TeamDef{
		Name:        tf.Name,
		Description: tf.Description,
	}
	def.Leader = AgentDef{
		Name:         tf.Leader.Name,
		Description:  tf.Leader.Description,
		Model:        tf.Leader.Model,
		SystemPrompt: strings.TrimSpace(tf.Leader.SystemPrompt),
		Tools:        tf.Leader.Tools,
		MaxRounds:    tf.Leader.MaxRounds,
		MaxTokens:    tf.Leader.MaxTokens,
	}
	if def.Leader.MaxRounds <= 0 {
		def.Leader.MaxRounds = 8
	}
	if def.Leader.MaxTokens <= 0 {
		def.Leader.MaxTokens = 4096
	}
	if def.Leader.Description == "" {
		def.Leader.Description = "Team leader: " + def.Name
	}

	for _, m := range tf.Members {
		role := TeamRole(m.Role)
		if role == "" {
			role = RoleSpecialist
		}
		ad := AgentDef{
			Name:         m.Agent.Name,
			Description:  m.Agent.Description,
			Model:        m.Agent.Model,
			SystemPrompt: strings.TrimSpace(m.Agent.SystemPrompt),
			Tools:        m.Agent.Tools,
			MaxRounds:    m.Agent.MaxRounds,
			MaxTokens:    m.Agent.MaxTokens,
		}
		if ad.MaxRounds <= 0 {
			ad.MaxRounds = 8
		}
		if ad.MaxTokens <= 0 {
			ad.MaxTokens = 4096
		}
		if ad.Description == "" {
			ad.Description = "Team member: " + m.Name
		}
		def.Members = append(def.Members, TeamMember{
			Name:     m.Name,
			Role:     role,
			AgentDef: ad,
		})
	}
	if def.Description == "" {
		def.Description = "Multi-agent team: " + def.Name
	}
	return def
}

// LoadTeams walks directories and loads every *.yaml / *.yml as a TeamDef.
// User dir is searched first, then project dir; duplicates by name keep the
// last definition (project overrides user).
func LoadTeams(dirs ...string) []*TeamDef {
	var out []*TeamDef
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			name := ent.Name()
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			var tf teamFile
			if err := yaml.Unmarshal(data, &tf); err != nil {
				continue
			}
			if tf.Name == "" {
				tf.Name = strings.TrimSuffix(name, filepath.Ext(name))
			}
			out = append(out, tf.toTeamDef())
		}
	}
	return out
}

// TeamDefaultDirs returns standard paths for team definition files, in the
// order to pass to LoadTeams (user first, then project).
func TeamDefaultDirs() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".icode", "teams"))
	}
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(cwd, ".icode", "teams"))
	}
	return out
}

// DefaultTeamDefs returns a built-in team so teams are demonstrable with zero
// config. A lightweight code-review team (security + performance) is dispatched
// via the task tool as `team:review`.
func DefaultTeamDefs() []*TeamDef {
	return []*TeamDef{
		{
			Name:        "review",
			Description: "多视角代码审查团队：安全 + 性能并行审查，由 lead 汇总。用 task 工具以 team:review 调度。",
			Leader: AgentDef{
				Name:         "lead",
				Description:  "审查协调者，拆分任务并汇总成员结论",
				SystemPrompt: "你是代码审查团队的 leader。把用户的审查请求拆成子任务分配给成员，收集结果后给出统一的审查结论（含风险等级与修复建议）。",
				MaxRounds:    8,
				MaxTokens:    4096,
			},
			Members: []TeamMember{
				{
					Name:     "security",
					Role:     RoleReviewer,
					AgentDef: AgentDef{
						Name:         "security",
						Description:  "安全审查专家",
						SystemPrompt: "你是安全审查专家。重点检查：注入（SQL/命令/路径遍历）、鉴权与越权、敏感信息泄漏、不安全依赖。只报告问题并给出修复建议，不要修改文件。",
						Tools:        []string{"read_file", "grep", "glob", "git_diff"},
						MaxRounds:    8,
						MaxTokens:    4096,
					},
				},
				{
					Name:     "performance",
					Role:     RoleSpecialist,
					AgentDef: AgentDef{
						Name:         "performance",
						Description:  "性能审查专家",
						SystemPrompt: "你是性能审查专家。重点检查：不必要的全量循环、N+1 查询、内存/goroutine 泄漏、锁竞争、大对象拷贝。只报告问题并给出修复建议，不要修改文件。",
						Tools:        []string{"read_file", "grep", "glob", "git_diff"},
						MaxRounds:    8,
						MaxTokens:    4096,
					},
				},
			},
		},
	}
}
