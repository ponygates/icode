// Package agent implements sub-agent orchestration.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
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
		// If parsing fails, just return the leader's output.
		result.Duration = time.Since(start)
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
		if !strings.HasPrefix(line, "MEMBER:") {
			continue
		}
		rest := line[len("MEMBER:"):]
		parts := strings.SplitN(rest, "|", 2)
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
