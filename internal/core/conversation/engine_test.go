package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/prefmem"
	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/types"
)

// fakeTool is a deterministic no-side-effect tool for engine-level tests.
type fakeTool struct {
	called int
	last   string
}

func (t *fakeTool) Def() types.ToolDef {
	return types.ToolDef{Name: "fake_echo", Description: "echo", Parameters: map[string]any{}}
}
func (t *fakeTool) Execute(_ context.Context, args string) (*types.ToolResult, error) {
	t.called++
	t.last = args
	return &types.ToolResult{Success: true, Content: "echo:" + args}, nil
}

// newTestEngine returns an engine wired to the fake tool, with no session
// store, gate, or skill registry attached.
func newTestEngine() *Engine {
	return NewEngine(nil, nil, nil)
}

func TestNewEngineRegistersTaskTool(t *testing.T) {
	e := newTestEngine()
	if _, ok := e.toolReg.Get("task"); !ok {
		t.Fatal("engine should register the task tool")
	}
}

func TestRegisterAndExecuteTool(t *testing.T) {
	e := newTestEngine()
	tool := &fakeTool{}
	e.RegisterTool(tool)

	res := e.ExecuteTool("fake_echo", `{"hello":"world"}`)
	if res == nil || !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if res.Content != `echo:{"hello":"world"}` {
		t.Fatalf("content = %q", res.Content)
	}
	if tool.called != 1 {
		t.Fatalf("tool called %d times, want 1", tool.called)
	}

	// Unregister → execution must return an error result, not panic.
	e.UnregisterTool("fake_echo")
	res = e.ExecuteTool("fake_echo", `{}`)
	if res == nil || res.Success {
		t.Fatalf("expected failure after unregister, got %+v", res)
	}
}

func TestExecuteToolUnknownToolReturnsError(t *testing.T) {
	e := newTestEngine()
	res := e.ExecuteTool("no_such_tool", `{}`)
	if res == nil || res.Success {
		t.Fatalf("expected error result for unknown tool, got %+v", res)
	}
}

func TestExecuteToolDoesNotPanicOnNilRegistry(t *testing.T) {
	// NewEngine(nil, nil, nil) has a nil providerReg but a valid toolReg —
	// ExecuteTool must still work for registered tools.
	e := newTestEngine()
	e.RegisterTool(&fakeTool{})
	res := e.ExecuteTool("fake_echo", `{}`)
	if res == nil || !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
}

func TestSessionStatsUnknownSessionIsNil(t *testing.T) {
	e := newTestEngine()
	if s := e.SessionStats("no-session"); s != nil {
		t.Fatalf("expected nil stats for unknown session, got %+v", s)
	}
}

func TestStopNoop(t *testing.T) {
	e := newTestEngine()
	// No running session → Stop must not panic and must not leave stale state.
	e.Stop("no-session")
	if len(e.stopFns) != 0 {
		t.Fatalf("stopFns should stay empty, got %d", len(e.stopFns))
	}
}

func TestSetPermissionResponseNoop(t *testing.T) {
	e := newTestEngine()
	// Unknown request id → must not panic.
	e.SetPermissionResponse("unknown-request", permission.DecisionAllow)
}

func TestEnableSkillWithoutRegistryErrors(t *testing.T) {
	e := newTestEngine()
	if err := e.EnableSkill("any"); err == nil {
		t.Fatal("expected error when no skill registry is attached")
	}
	if err := e.DisableSkill("any"); err == nil {
		t.Fatal("expected error when no skill registry is attached")
	}
	if s := e.ListSkills(); s != nil {
		t.Fatalf("expected nil skills without registry, got %v", s)
	}
}

func TestRegisterTeamAndListTeams(t *testing.T) {
	e := newTestEngine()
	if got := e.ListTeams(); len(got) != 0 {
		t.Fatalf("expected no teams initially, got %v", got)
	}
	e.RegisterTeam(&agent.TeamDef{Name: "team-a", Description: "A"})
	e.RegisterTeam(&agent.TeamDef{Name: "team-b"})
	teams := e.ListTeams()
	if len(teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(teams))
	}
	names := map[string]bool{}
	for _, tm := range teams {
		names[tm.Name] = true
	}
	if !names["team-a"] || !names["team-b"] {
		t.Fatalf("missing teams: %v", names)
	}
}

func TestEngineSetters(t *testing.T) {
	e := newTestEngine()
	e.SetGenerationParams(0.3, 512)
	e.SetSystemPrompt("be concise")
	e.SetFallbackModels([]string{"model-b"})
	e.SetPermissionHandler(nil)
	e.SetRouter(nil)
	e.SetLSPManager(nil)
	e.SetHooksRunner(nil)
	e.WireTaskRunner()
	if e.temperature != 0.3 || e.maxTokens != 512 {
		t.Fatalf("generation params not set: %v %v", e.temperature, e.maxTokens)
	}
	if e.systemPrompt != "be concise" {
		t.Fatalf("system prompt not set: %q", e.systemPrompt)
	}
	if len(e.fallbackModels) != 1 || e.fallbackModels[0] != "model-b" {
		t.Fatalf("fallback models not set: %v", e.fallbackModels)
	}
}

// TestLearnPreferences_HonorsExplicit verifies the engine records explicit
// user preferences while ignoring plain task text and code.
func TestLearnPreferences_HonorsExplicit(t *testing.T) {
	e := newTestEngine()
	e.learnPreferences("帮我重构一下 engine.go，给它加快点")
	if n := e.PreferenceMemory().Snapshot(); len(n) != 0 {
		t.Fatalf("plain task must not be remembered, got %v", n)
	}
	e.learnPreferences("以后都用简体中文回答我")
	found := false
	for _, ent := range e.PreferenceMemory().Snapshot() {
		if strings.Contains(ent.Text, "简体中文回答") {
			found = true
		}
	}
	if !found {
		t.Fatalf("explicit preference should be remembered: %v", e.PreferenceMemory().Snapshot())
	}
}

// TestLearnPreferences_InjectIntoBuild verifies buildSystemPrompt appends
// remembered preferences.
func TestLearnPreferences_InjectIntoBuild(t *testing.T) {
	e := newTestEngine()
	e.SetPreferenceMemory(prefmem.New(prefmem.Options{}))
	e.PreferenceMemory().Remember("优先用 Go 写后台服务")
	prompt := e.buildSystemPrompt("sess-x")
	if !strings.Contains(prompt, "优先用 Go 写后台服务") {
		t.Fatalf("buildSystemPrompt should inject remembered preference, got:\n%s", prompt)
	}
}

// TestStashIfPivot_SnapshotsOnPivot verifies that when in-progress work exists
// and the user opens a NEW task, the engine snapshots a stash checkpoint and
// appends a recovery note to the archived summary.
func TestStashIfPivot_SnapshotsOnPivot(t *testing.T) {
	store := session.NewStore()
	sess := &types.Session{ID: "sess-stash", ModelID: "m", ProviderName: "deepseek"}
	if err := store.Create(sess); err != nil {
		t.Fatal(err)
	}
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: "重构 engine.go 让缓存更好"},
		{Role: types.RoleTool, Content: "Edited engine.go"},
	}
	if err := store.Update(sess); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(nil, store, nil)
	e.stashIfPivot(context.Background(), sess, "另外帮我写个新工具 readfile2")

	got, _ := store.Get("sess-stash")
	if summary := sessionum.Get(got); !strings.Contains(summary, "(stash)") {
		t.Fatalf("expected stash note in summary, got:\n%s", summary)
	}
}

// TestStashIfPivot_IgnoresContinuation verifies a plain follow-up ("继续")
// does NOT create a stash point — only genuine pivots do.
func TestStashIfPivot_IgnoresContinuation(t *testing.T) {
	store := session.NewStore()
	sess := &types.Session{ID: "sess-cont", ModelID: "m", ProviderName: "deepseek"}
	if err := store.Create(sess); err != nil {
		t.Fatal(err)
	}
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: "重构 engine.go"},
		{Role: types.RoleTool, Content: "Edited engine.go"},
	}
	if err := store.Update(sess); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(nil, store, nil)
	e.stashIfPivot(context.Background(), sess, "继续")

	got, _ := store.Get("sess-cont")
	if s := sessionum.Get(got); strings.Contains(s, "(stash)") {
		t.Fatalf("continuation must not stash, got:\n%s", s)
	}
}
