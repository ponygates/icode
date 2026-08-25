package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/types"
)

// stagedProvider replays a fixed script of event batches, one per ChatStream
// call, so tests can simulate "first reply truncated → repair reply valid".
type stagedProvider struct {
	replies [][]types.StreamEvent
	call    int
	seen    []types.ChatRequest
}

func (p *stagedProvider) Name() string { return "fake" }
func (p *stagedProvider) ListModels() []types.ModelInfo {
	return []types.ModelInfo{{ID: "fake-model", Provider: "fake", MaxOutputTokens: 8192}}
}
func (p *stagedProvider) ChatStream(ctx context.Context, req types.ChatRequest) (<-chan types.StreamEvent, error) {
	p.seen = append(p.seen, req)
	ch := make(chan types.StreamEvent, 16)
	if ctx.Err() != nil {
		close(ch)
		return ch, ctx.Err()
	}
	if p.call >= len(p.replies) {
		close(ch)
		return ch, nil
	}
	for _, ev := range p.replies[p.call] {
		ch <- ev
	}
	p.call++
	close(ch)
	return ch, nil
}
func (p *stagedProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.Message, error) {
	return &types.Message{Role: types.RoleAssistant, Content: "staged"}, nil
}
func (p *stagedProvider) Health(ctx context.Context) error { return nil }
func (p *stagedProvider) SupportsCache() bool              { return true }

// drain consumes every event from out until it is closed.
func drain(out chan types.StreamEvent) {
	for range out {
	}
}

// newRepairEngine wires an engine with the staged provider + fake_echo tool,
// a real in-memory session store and a session with 2 messages.
func newRepairEngine(p *stagedProvider, t *testing.T) (*Engine, *types.Session, types.ModelInfo) {
	t.Helper()
	st := session.NewStore()
	e := NewEngine(&fakeRegistry{prov: p}, st, nil)
	e.RegisterTool(&fakeTool{})
	sess := &types.Session{ID: "repair-sess", ModelID: "fake-model"}
	if err := st.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: "帮我调用工具"},
		{Role: types.RoleAssistant, Content: "好的，我来调用。"},
	}
	mi := p.ListModels()[0]
	return e, sess, mi
}

func TestBrokenToolCallIndex(t *testing.T) {
	cases := []struct {
		name  string
		calls []types.ToolCall
		want  int
	}{
		{"all-valid", []types.ToolCall{
			{Name: "a", Arguments: `{"x":1}`},
			{Name: "b", Arguments: `{}`},
		}, -1},
		{"truncated-json", []types.ToolCall{
			{Name: "a", Arguments: `{"x":1}`},
			{Name: "b", Arguments: `{"y":`}, // unbalanced
		}, 1},
		{"empty-args", []types.ToolCall{
			{Name: "a", Arguments: ""},
		}, 0},
		{"nil", nil, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := brokenToolCallIndex(tc.calls); got != tc.want {
				t.Fatalf("brokenToolCallIndex() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestRepairBrokenToolCalls_RepairsAndDispatches is the full tool-JSON
// auto-resend integration: a broken call is repaired via a second model call,
// the repaired call is executed, and the tool result lands in the session.
func TestRepairBrokenToolCalls_RepairsAndDispatches(t *testing.T) {
	// Isolate checkpoint storage (it writes to ~/.icode/checkpoints).
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	p := &stagedProvider{replies: [][]types.StreamEvent{
		// Call #1 — the repair round: model returns the completed tool call.
		// The argument value is unique to avoid the global tool-output dedup
		// cache (tokenopt.DefaultOutputCache) swapping the result for a
		// placeholder when other tests ran the same call.
		{
			{Type: types.EventToolUse, ToolCall: &types.LiveToolCall{ID: "t2", Name: "fake_echo", Arguments: `{"hello":"repair-unique-9f3c"}`}},
			{Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}},
		},
		// Call #2 — continueAgentLoop after tool results: plain text reply.
		{
			{Type: types.EventText, Content: "已完成"},
			{Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}},
		},
	}}
	e, sess, mi := newRepairEngine(p, t)
	opt := e.getOrCreateOptimizer(sess.ID, mi, sess.Messages, "")

	// The model's turn was cut off at max_tokens: the last tool call's
	// arguments JSON is incomplete.
	broken := []types.ToolCall{{ID: "t1", Name: "fake_echo", Arguments: `{"hello":`}}

	out := make(chan types.StreamEvent, 128)
	go drain(out)
	ok := e.repairBrokenToolCalls(context.Background(), sess.ID, p, opt, mi, broken, true, out, 0)
	close(out)

	if !ok {
		t.Fatalf("repairBrokenToolCalls should return true after successful repair")
	}
	if len(p.seen) != 2 {
		t.Fatalf("expected 2 model calls (repair + continuation), got %d", len(p.seen))
	}
	// The repair prompt must reference the broken tool and replay its partial
	// arguments so the model can complete them faithfully. It is appended as
	// the LAST message of the repair request.
	repairReq := p.seen[0]
	if len(repairReq.Messages) == 0 {
		t.Fatalf("repair request has no messages")
	}
	last := repairReq.Messages[len(repairReq.Messages)-1]
	if !strings.Contains(last.Content, "fake_echo") ||
		!strings.Contains(last.Content, `{"hello":`) {
		t.Fatalf("repair prompt missing broken call context: %q", last.Content)
	}
	// The repaired tool call must have executed: the continuation request
	// (call #2) carries the tool result (RoleTool) fed back to the model.
	cont := p.seen[1]
	foundTool := false
	for _, m := range cont.Messages {
		if m.Role == types.RoleTool && strings.Contains(m.Content, "repair-unique-9f3c") {
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Fatalf("repaired tool call did not execute (no tool result in continuation request)")
	}
}

// TestRepairBrokenToolCalls_AllValidCalls_ReturnsFalse: when every tool call
// is well-formed there is nothing to repair — the caller executes as-is.
func TestRepairBrokenToolCalls_AllValidCalls_ReturnsFalse(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	p := &stagedProvider{}
	e, sess, mi := newRepairEngine(p, t)
	opt := e.getOrCreateOptimizer(sess.ID, mi, sess.Messages, "")
	out := make(chan types.StreamEvent, 16)
	ok := e.repairBrokenToolCalls(context.Background(), sess.ID, p, opt, mi,
		[]types.ToolCall{{ID: "t1", Name: "fake_echo", Arguments: `{"hello":"world"}`}}, true, out, 0)
	if ok {
		t.Fatalf("valid tool calls must not trigger repair")
	}
	if len(p.seen) != 0 {
		t.Fatalf("no model call expected, got %d", len(p.seen))
	}
}

// TestRepairBrokenToolCalls_NonTruncated_RepairsToo: malformed JSON with
// finish_reason="stop" (a model bug, not a budget cut) also gets one repair
// chance — with the CURRENT output budget, no escalation.
func TestRepairBrokenToolCalls_NonTruncated_RepairsToo(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	p := &stagedProvider{replies: [][]types.StreamEvent{
		// Repair round (non-truncated): model re-emits the call with valid JSON.
		{
			{Type: types.EventToolUse, ToolCall: &types.LiveToolCall{ID: "t2", Name: "fake_echo", Arguments: `{"hello":"non-truncated-77aa"}`}},
			{Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}},
		},
		// Continuation after tool results.
		{
			{Type: types.EventText, Content: "修复完成"},
			{Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}},
		},
	}}
	e, sess, mi := newRepairEngine(p, t)
	opt := e.getOrCreateOptimizer(sess.ID, mi, sess.Messages, "")
	out := make(chan types.StreamEvent, 128)
	go drain(out)

	// truncated=false → still repaired.
	ok := e.repairBrokenToolCalls(context.Background(), sess.ID, p, opt, mi,
		[]types.ToolCall{{ID: "t1", Name: "fake_echo", Arguments: `{"hello":`}}, false, out, 0)
	close(out)

	if !ok {
		t.Fatalf("non-truncated malformed JSON should also be repaired")
	}
	if len(p.seen) < 2 {
		t.Fatalf("expected repair + continuation calls, got %d", len(p.seen))
	}
	repairReq := p.seen[0]
	last := repairReq.Messages[len(repairReq.Messages)-1]
	if !strings.Contains(last.Content, "格式不合法") {
		t.Fatalf("non-truncated repair prompt should say 格式不合法: %q", last.Content)
	}
	// No budget escalation for non-truncated repairs: max_tokens stays at the
	// model default (orMaxTokens(0, 8192) = 8192).
	if repairReq.MaxTokens != 8192 {
		t.Fatalf("non-truncated repair must not escalate max_tokens, got %d", repairReq.MaxTokens)
	}
}

// TestRepairBrokenToolCalls_BudgetExhausted: the per-turn repair budget caps
// auto-repairs so a model emitting broken JSON over and over cannot loop.
// After the budget is spent, further repair attempts return false immediately
// without calling the provider.
func TestRepairBrokenToolCalls_BudgetExhausted(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	// Both repair rounds fail to produce a valid tool call (model insists on
	// emitting text only) → each consumes one budget unit.
	p := &stagedProvider{replies: [][]types.StreamEvent{
		{{Type: types.EventText, Content: "no tool call"}, {Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}}},
		{{Type: types.EventText, Content: "still no tool call"}, {Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}}},
	}}
	e, sess, mi := newRepairEngine(p, t)
	opt := e.getOrCreateOptimizer(sess.ID, mi, sess.Messages, "")
	out := make(chan types.StreamEvent, 128)
	go drain(out)

	broken := []types.ToolCall{{ID: "t1", Name: "fake_echo", Arguments: `{"x":`}}
	// Attempt 1 — consumes budget, repair fails (no valid call) → false.
	if ok := e.repairBrokenToolCalls(context.Background(), sess.ID, p, opt, mi, broken, false, out, 0); ok {
		t.Fatalf("attempt 1 should fail (no valid tool call returned)")
	}
	// Attempt 2 — consumes the last budget unit, fails again → false.
	if ok := e.repairBrokenToolCalls(context.Background(), sess.ID, p, opt, mi, broken, false, out, 0); ok {
		t.Fatalf("attempt 2 should fail (no valid tool call returned)")
	}
	// Attempt 3 — budget spent: must NOT call the provider.
	callsBefore := len(p.seen)
	if ok := e.repairBrokenToolCalls(context.Background(), sess.ID, p, opt, mi, broken, false, out, 0); ok {
		t.Fatalf("attempt 3 must not repair (budget spent)")
	}
	if len(p.seen) != callsBefore {
		t.Fatalf("budget-spent attempt must not call the provider (calls %d → %d)", callsBefore, len(p.seen))
	}
	close(out)
}

// TestRepairBrokenToolCalls_ProviderError_FallsThrough: a failing repair call
// must not panic or block — it returns false so the original (broken) call is
// executed normally and surfaces its own error.
func TestRepairBrokenToolCalls_ProviderError_FallsThrough(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	p := &stagedProvider{replies: [][]types.StreamEvent{
		{{Type: types.EventError, Content: "boom"}},
		{{Type: types.EventText, Content: "retry done"}, {Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}}},
	}}
	e, sess, mi := newRepairEngine(p, t)
	opt := e.getOrCreateOptimizer(sess.ID, mi, sess.Messages, "")
	out := make(chan types.StreamEvent, 128)
	go drain(out)
	ok := e.repairBrokenToolCalls(context.Background(), sess.ID, p, opt, mi,
		[]types.ToolCall{{ID: "t1", Name: "fake_echo", Arguments: `{"x":`}}, true, out, 0)
	close(out)
	if ok {
		t.Fatalf("repair must fail through when the provider errors")
	}
}

// TestEngine_SetThinking covers the extended-thinking toggle on the engine:
// off by default, enabled with a budget, cleared again on budget <= 0.
func TestEngine_SetThinking(t *testing.T) {
	e := newTestEngine()
	if got := e.ThinkingBudget(); got != 0 {
		t.Fatalf("extended thinking should default to off, got %d", got)
	}
	e.SetThinking(4096)
	if got := e.ThinkingBudget(); got != 4096 {
		t.Fatalf("budget = %d, want 4096", got)
	}
	e.SetThinking(0)
	if got := e.ThinkingBudget(); got != 0 {
		t.Fatalf("budget should clear on <= 0, got %d", got)
	}
}
