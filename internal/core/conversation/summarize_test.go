package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/types"
)

// fakeChatProvider returns a canned reply through ChatStream and records the
// request so tests can assert what was sent (model, no tools, cap).
type fakeChatProvider struct {
	name  string
	model types.ModelInfo
	reply string
	seen  []types.ChatRequest
}

func (p *fakeChatProvider) Name() string { return p.name }
func (p *fakeChatProvider) ListModels() []types.ModelInfo {
	return []types.ModelInfo{p.model}
}
func (p *fakeChatProvider) ChatStream(ctx context.Context, req types.ChatRequest) (<-chan types.StreamEvent, error) {
	p.seen = append(p.seen, req)
	ch := make(chan types.StreamEvent, 3)
	if ctx.Err() != nil {
		close(ch)
		return ch, ctx.Err()
	}
	ch <- types.StreamEvent{Type: types.EventText, Content: p.reply}
	ch <- types.StreamEvent{Type: types.EventDone}
	close(ch)
	return ch, nil
}
func (p *fakeChatProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.Message, error) {
	return &types.Message{Role: types.RoleAssistant, Content: p.reply}, nil
}
func (p *fakeChatProvider) Health(ctx context.Context) error { return nil }
func (p *fakeChatProvider) SupportsCache() bool              { return true }

// fakeRegistry is a single-provider ProviderRegistry stub.
type fakeRegistry struct {
	prov types.Provider
}

func (r *fakeRegistry) Register(p types.Provider) error { r.prov = p; return nil }
func (r *fakeRegistry) Get(name string) (types.Provider, error) {
	if r.prov != nil && r.prov.Name() == name {
		return r.prov, nil
	}
	return nil, errProviderNotFound
}
func (r *fakeRegistry) List() []string {
	if r.prov == nil {
		return nil
	}
	return []string{r.prov.Name()}
}
func (r *fakeRegistry) ListAllModels() []types.ModelInfo {
	if r.prov == nil {
		return nil
	}
	return r.prov.ListModels()
}
func (r *fakeRegistry) RefreshAll(ctx context.Context) []error { return nil }
func (r *fakeRegistry) ResolveModel(modelID string) (types.Provider, types.ModelInfo, error) {
	if r.prov == nil {
		return nil, types.ModelInfo{}, errModelNotFound
	}
	m := r.prov.ListModels()[0]
	if modelID != "" && modelID != m.ID {
		return nil, types.ModelInfo{}, errModelNotFound
	}
	return r.prov, m, nil
}
func (r *fakeRegistry) SetCredentials(name, apiKey, apiBase string) bool    { return false }
func (r *fakeRegistry) SetTimeout(name string, sec int) bool                { return false }
func (r *fakeRegistry) RegisterCustomModel(m types.ModelInfo, alias string) {}
func (r *fakeRegistry) RemoveCustomModel(canonicalID string)                {}
func (r *fakeRegistry) Deregister(name string)                              {}

func newSummarizeEngine(reply string) (*Engine, *fakeChatProvider) {
	prov := &fakeChatProvider{
		name:  "fake",
		model: types.ModelInfo{ID: "fake-model", Provider: "fake", MaxOutputTokens: 8192},
		reply: reply,
	}
	e := NewEngine(&fakeRegistry{prov: prov}, session.NewStore(), nil)
	return e, prov
}

func TestSummarizeConversation_TooFewTurns(t *testing.T) {
	e, prov := newSummarizeEngine("## 目标\n啥都没有")
	sess := &types.Session{ID: "s1", ModelID: "fake-model"}
	if err := e.sessionSt.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: "a"},
		{Role: types.RoleAssistant, Content: "b"},
	}
	sum, err := e.SummarizeConversation(context.Background(), "s1", "")
	if err != nil {
		t.Fatalf("expected nil error for too-few turns, got %v", err)
	}
	if sum != "" {
		t.Fatalf("too-few turns should return empty summary, got %q", sum)
	}
	if len(prov.seen) != 0 {
		t.Fatalf("no model call expected, got %d", len(prov.seen))
	}
}

func TestSummarizeConversation_CallsModelAndCaps(t *testing.T) {
	reply := strings.Repeat("## 已完成\n- 修好了", 200) // > 6000 runes → must be capped
	e, prov := newSummarizeEngine(reply)
	sess := &types.Session{ID: "s2", ModelID: "fake-model"}
	if err := e.sessionSt.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	msgs := make([]types.Message, 0, 8)
	for i := 0; i < 4; i++ {
		msgs = append(msgs,
			types.Message{Role: types.RoleUser, Content: "问题 " + string(rune('a'+i))},
			types.Message{Role: types.RoleAssistant, Content: "修复 " + string(rune('a'+i))},
		)
	}
	sess.Messages = msgs

	sum, err := e.SummarizeConversation(context.Background(), "s2", "只保留安全相关")
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if sum == "" {
		t.Fatalf("expected non-empty summary")
	}
	if len([]rune(sum)) > 6000 {
		t.Fatalf("summary not capped: %d runes", len([]rune(sum)))
	}
	if len(prov.seen) != 1 {
		t.Fatalf("expected exactly 1 model call, got %d", len(prov.seen))
	}
	req := prov.seen[0]
	if len(req.Tools) != 0 {
		t.Fatalf("summarization must not send tools, got %d", len(req.Tools))
	}
	if !strings.Contains(req.Messages[0].Content, "只保留安全相关") {
		t.Fatalf("instruction not folded into prompt")
	}
	if !strings.Contains(req.Messages[0].Content, "[user]") || !strings.Contains(req.Messages[0].Content, "[assistant]") {
		t.Fatalf("transcript not rendered with role labels")
	}
	// Older turns only: the last user/assistant pair must NOT be in the input.
	if strings.Contains(req.Messages[0].Content, "修复 d") {
		t.Fatalf("the most recent turn should be excluded from the summary input")
	}
}

func TestSummarizeConversation_MissingSession(t *testing.T) {
	e, _ := newSummarizeEngine("x")
	if _, err := e.SummarizeConversation(context.Background(), "nope", ""); err == nil {
		t.Fatalf("missing session must error")
	}
}

func TestSummarizeConversation_NoModelConfigured(t *testing.T) {
	e, _ := newSummarizeEngine("x")
	sess := &types.Session{ID: "s3", ModelID: ""} // no model
	if err := e.sessionSt.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	sess.Messages = []types.Message{{Role: types.RoleUser, Content: "a"}}
	if _, err := e.SummarizeConversation(context.Background(), "s3", ""); err == nil {
		t.Fatalf("session without model must error")
	}
}

func TestRenderTranscript_HeadTailRetention(t *testing.T) {
	big := strings.Repeat("中", 50_000)
	msgs := []types.Message{
		{Role: types.RoleUser, Content: "开始"},
		{Role: types.RoleAssistant, Content: big},
		{Role: types.RoleUser, Content: "结尾"},
	}
	out := renderTranscript(msgs, 1000)
	if len([]rune(out)) > 1000 {
		t.Fatalf("transcript exceeds cap: %d", len([]rune(out)))
	}
	if !strings.Contains(out, "开始") {
		t.Fatalf("head must be retained")
	}
	if !strings.Contains(out, "结尾") {
		t.Fatalf("tail must be retained")
	}
	if !strings.Contains(out, "omitted") {
		t.Fatalf("omission marker missing")
	}
	// Tool role labeled.
	msgs2 := []types.Message{
		{Role: types.RoleUser, Content: "run"},
		{Role: types.RoleTool, Content: "exit 0", ToolCalls: []types.ToolCall{{Name: "bash"}}},
	}
	out2 := renderTranscript(msgs2, 100_000)
	if !strings.Contains(out2, "[tool]") {
		t.Fatalf("tool role not labeled")
	}
}

var errProviderNotFound = errors.New("provider not found")
var errModelNotFound = errors.New("model not found")
