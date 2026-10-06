package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// fakeTeamCall records one underlying runner invocation.
type fakeTeamCall struct {
	agent string
	input string
}

// fakeTeamRunner is a scripted memberRunner: the returned text is keyed by
// the AgentDef name, every call is recorded for assertions.
type fakeTeamRunner struct {
	mu    sync.Mutex
	calls []fakeTeamCall
}

func (f *fakeTeamRunner) record(name, input string) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeTeamCall{agent: name, input: input})
	f.mu.Unlock()
}

func (f *fakeTeamRunner) snapshot() []fakeTeamCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeTeamCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// call returns the first recorded call for the given agent name.
func (f *fakeTeamRunner) call(name string) (fakeTeamCall, bool) {
	for _, c := range f.snapshot() {
		if c.agent == name {
			return c, true
		}
	}
	return fakeTeamCall{}, false
}

func (f *fakeTeamRunner) lastCall(name string) (fakeTeamCall, bool) {
	var found fakeTeamCall
	ok := false
	for _, c := range f.snapshot() {
		if c.agent == name {
			found, ok = c, true
		}
	}
	return found, ok
}

func devTeamDef() *TeamDef {
	return &TeamDef{
		Name:        "dev",
		Description: "build and audit",
		Leader:      AgentDef{Name: "lead"},
		Members: []TeamMember{
			{Name: "coder", Role: RoleSpecialist, AgentDef: AgentDef{Name: "coder"}},
			{Name: "auditor", Role: RoleReviewer, AgentDef: AgentDef{Name: "auditor"}},
		},
	}
}

// TestTeamRun_ReviewerReceivesBlackboard verifies the two-phase flow:
// specialists run in parallel first, then the reviewer's prompt carries the
// peer outputs (blackboard), and the leader synthesis sees the review.
func TestTeamRun_ReviewerReceivesBlackboard(t *testing.T) {
	fake := &fakeTeamRunner{}
	plan := "MEMBER: coder | TASK: write the module\nMEMBER: auditor | TASK: audit the module"
	fake.record("lead-plan-stub", "") // placeholder removed below
	fake.calls = nil
	tr := &TeamRunner{runner: &scriptedRunner{fake: fake, plan: plan, replies: map[string]string{
		"coder": "CODE-RESULT", "auditor": "REVIEW-RESULT", "lead": "FINAL",
	}}}
	res, err := tr.Run(context.Background(), devTeamDef(), "ship feature")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The reviewer saw the coder's output.
	audit, ok := fake.call("auditor")
	if !ok {
		t.Fatal("auditor never ran")
	}
	if !strings.Contains(audit.input, "Peer outputs") || !strings.Contains(audit.input, "CODE-RESULT") {
		t.Errorf("auditor prompt missing blackboard:\n%s", audit.input)
	}
	// The auditor ran after the coder finished (its own task too).
	if !strings.Contains(audit.input, "audit the module") {
		t.Errorf("auditor prompt missing its assigned task:\n%s", audit.input)
	}

	// The leader synthesis includes the review result.
	synth, ok := fake.lastCall("lead")
	if !ok || !strings.Contains(synth.input, "REVIEW-RESULT") {
		t.Errorf("synthesis prompt missing review output:\n%s", synth.input)
	}

	if res.MemberOutputs["auditor"] != "REVIEW-RESULT" {
		t.Errorf("MemberOutputs[auditor] = %q", res.MemberOutputs["auditor"])
	}
}

// TestTeamRun_ReviewerAutoAssigned: a reviewer the leader never tasked still
// gets an auto-generated review pass over the blackboard.
func TestTeamRun_ReviewerAutoAssigned(t *testing.T) {
	fake := &fakeTeamRunner{}
	tr := &TeamRunner{runner: &scriptedRunner{fake: fake, plan: "MEMBER: coder | TASK: write it", replies: map[string]string{
		"coder": "CODE-RESULT", "auditor": "REVIEW-RESULT", "lead": "FINAL",
	}}}
	res, err := tr.Run(context.Background(), devTeamDef(), "ship feature")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	audit, ok := fake.call("auditor")
	if !ok {
		t.Fatal("reviewer without an assigned task never ran")
	}
	if !strings.Contains(audit.input, "CODE-RESULT") {
		t.Errorf("auto review missing blackboard:\n%s", audit.input)
	}
	if res.MemberOutputs["auditor"] == "" {
		t.Error("auto review output not captured")
	}
}

// TestTeamRun_NoReviewers: all-specialist teams keep the classic behaviour —
// everyone gets the raw task, no blackboard injection.
func TestTeamRun_NoReviewers(t *testing.T) {
	def := devTeamDef()
	def.Members = []TeamMember{
		{Name: "coder", Role: RoleSpecialist, AgentDef: AgentDef{Name: "coder"}},
	}
	fake := &fakeTeamRunner{}
	tr := &TeamRunner{runner: &scriptedRunner{fake: fake, plan: "MEMBER: coder | TASK: write it", replies: map[string]string{
		"coder": "CODE-RESULT", "lead": "FINAL",
	}}}
	res, err := tr.Run(context.Background(), def, "ship feature")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	call, _ := fake.call("coder")
	if !strings.Contains(call.input, "write it") || strings.Contains(call.input, "Peer outputs") {
		t.Errorf("specialist prompt = %q", call.input)
	}
	if res.MemberOutputs["coder"] != "CODE-RESULT" {
		t.Errorf("MemberOutputs = %v", res.MemberOutputs)
	}
}

// TestSplitTasksByRole covers the partition helper directly.
func TestSplitTasksByRole(t *testing.T) {
	members := []TeamMember{
		{Name: "a", Role: RoleSpecialist},
		{Name: "b", Role: RoleReviewer},
		{Name: "c", Role: RoleSpecialist},
	}
	spec, rev := splitTasksByRole(map[string]string{
		"a": "do", "b": "check", "c": "build",
	}, members)
	if len(spec) != 2 || len(rev) != 1 {
		t.Fatalf("spec=%v rev=%v", spec, rev)
	}
	if _, ok := rev["b"]; !ok {
		t.Errorf("rev = %v, want b", rev)
	}
	// Unknown members default to the specialist bucket.
	spec2, rev2 := splitTasksByRole(map[string]string{"ghost": "x"}, members)
	if len(spec2) != 1 || len(rev2) != 0 {
		t.Errorf("ghost: spec=%v rev=%v", spec2, rev2)
	}
}

// scriptedRunner routes by AgentDef name: "lead" returns the plan on its
// first call and "SYNTH" afterwards; every call is recorded in the shared
// fakeTeamRunner.
type scriptedRunner struct {
	fake    *fakeTeamRunner
	plan    string
	replies map[string]string
}

func (s *scriptedRunner) Run(ctx context.Context, def *AgentDef, input string) (string, int, error) {
	s.fake.record(def.Name, input)
	if def.Name == "lead" {
		if strings.Contains(input, "Synthesize a final") {
			return "FINAL", 3, nil
		}
		return s.plan, 2, nil
	}
	if r, ok := s.replies[def.Name]; ok {
		return r, 1, nil
	}
	return "ok", 1, nil
}
