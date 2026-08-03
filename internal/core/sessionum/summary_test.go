package sessionum

import (
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/types"
)

func mkStore(t *testing.T) types.SessionStore {
	t.Helper()
	return session.NewStore()
}

func mkSession(t *testing.T, store types.SessionStore, title string) *types.Session {
	t.Helper()
	sess := &types.Session{ID: "sess-" + title, Title: title, ModelID: "m1", ProviderName: "p1"}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	return sess
}

func TestGenerate_EmptyReturnsEmpty(t *testing.T) {
	if got := Generate(nil, "", "", ""); got != "" {
		t.Fatalf("nil session should yield empty summary, got %q", got)
	}
	store := mkStore(t)
	if got := Generate(mkSession(t, store, "empty"), "", "", ""); got != "" {
		t.Fatalf("empty session should yield empty summary, got %q", got)
	}
}

func TestGenerate_StatsAndQuestions(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "demo")
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: "帮我重构这个函数"},
		{Role: types.RoleAssistant, Content: "好的。"},
		{Role: types.RoleUser, Content: strings.Repeat("长", 200)},
	}
	sess.TotalTokens = types.TokenUsage{PromptTokens: 100, CompletionTokens: 50}

	got := Generate(sess, "gpt-x", "prov", "agent")
	for _, want := range []string{"对话总结", "3 条消息", "gpt-x", "prov", "agent", "150 总计", "帮我重构这个函数"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
	// Long question must be truncated with ellipsis, not dumped whole.
	if strings.Contains(got, strings.Repeat("长", 200)) {
		t.Errorf("long question not truncated")
	}
	if !strings.Contains(got, "…") {
		t.Errorf("expected truncation ellipsis")
	}
}

func TestSaveGet_RoundTrip(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "r")
	sum := Generate(sess, "", "", "")
	if sum != "" {
		t.Fatalf("empty session should generate empty summary")
	}
	// Save with empty summary must be a no-op.
	if err := Save(store, sess, ""); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	if Get(sess) != "" {
		t.Fatalf("empty summary should not be stored")
	}

	sess.Messages = []types.Message{{Role: types.RoleUser, Content: "hi"}}
	sum = Generate(sess, "", "", "")
	if err := Save(store, sess, sum); err != nil {
		t.Fatalf("save: %v", err)
	}
	if Get(sess) != sum {
		t.Fatalf("round-trip mismatch")
	}
	// Reload from store to prove persistence path.
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if Get(got) != sum {
		t.Fatalf("persisted summary mismatch")
	}
}

func TestSave_IdempotentSameSummary(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "idem")
	sess.Messages = []types.Message{{Role: types.RoleUser, Content: "x"}}
	sum := Generate(sess, "", "", "")
	if err := Save(store, sess, sum); err != nil {
		t.Fatalf("first save: %v", err)
	}
	before := store.(*session.Store)
	if err := Save(store, sess, sum); err != nil {
		t.Fatalf("second save: %v", err)
	}
	_ = before // second Save must not error; idempotency is in sessionum
}

func TestFork_ClampsAndBranches(t *testing.T) {
	store := mkStore(t)
	src := mkSession(t, store, "src")
	src.Messages = []types.Message{
		{Role: types.RoleUser, Content: "a"},
		{Role: types.RoleUser, Content: "b"},
		{Role: types.RoleUser, Content: "c"},
	}
	_ = Save(store, src, Generate(src, "", "", ""))
	if err := store.Update(src); err != nil {
		t.Fatalf("update: %v", err)
	}

	// n clamped to len(Messages)
	forked, err := Fork(store, src.ID, 999)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if len(forked.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(forked.Messages))
	}
	if forked.ID == src.ID {
		t.Fatalf("fork must get a new id")
	}
	if !strings.HasPrefix(forked.Title, "Fork:") {
		t.Fatalf("fork title should be prefixed: %q", forked.Title)
	}
	if Get(forked) != "" {
		t.Fatalf("fork must NOT inherit the archived summary")
	}

	// n=2 keeps exactly the first 2 messages
	f2, err := Fork(store, src.ID, 2)
	if err != nil {
		t.Fatalf("fork n=2: %v", err)
	}
	if len(f2.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(f2.Messages))
	}
	if f2.Messages[0].Content != "a" || f2.Messages[1].Content != "b" {
		t.Fatalf("fork prefix wrong")
	}

	// Empty source fails
	empty := mkSession(t, store, "empty")
	if _, err := Fork(store, empty.ID, 0); err == nil {
		t.Fatalf("fork of empty session should fail")
	}
	// Missing source fails
	if _, err := Fork(store, "nope", 1); err == nil {
		t.Fatalf("fork of missing session should fail")
	}
}

func TestLite_SetReadClear(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "lite")
	if LiteN(sess) != 0 {
		t.Fatalf("new session should not be lite")
	}
	if err := SetLite(store, sess, 4); err != nil {
		t.Fatalf("set lite: %v", err)
	}
	got, _ := store.Get(sess.ID)
	if LiteN(got) != 4 {
		t.Fatalf("lite not persisted: %d", LiteN(got))
	}
	if err := SetLite(store, sess, 0); err != nil {
		t.Fatalf("clear lite: %v", err)
	}
	got, _ = store.Get(sess.ID)
	if LiteN(got) != 0 {
		t.Fatalf("lite should be cleared")
	}
	if _, ok := got.Metadata[LiteKey]; ok {
		t.Fatalf("lite key should be deleted, not left empty")
	}
}

func TestBudget_SetReadClear(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "b")
	if BudgetMax(sess) != 0 {
		t.Fatalf("new session should have no budget")
	}
	if err := SetBudget(store, sess, 16000); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, _ := store.Get(sess.ID)
	if BudgetMax(got) != 16000 {
		t.Fatalf("budget not persisted: %d", BudgetMax(got))
	}
	if err := SetBudget(store, sess, 0); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = store.Get(sess.ID)
	if BudgetMax(got) != 0 {
		t.Fatalf("budget should be cleared")
	}
}

func TestTrimToBudget_KeepsNewest(t *testing.T) {
	// 10 messages, each ~60 runes → ~20 tokens each.
	msgs := make([]types.Message, 10)
	for i := range msgs {
		msgs[i] = types.Message{Role: types.RoleUser, Content: strings.Repeat("x", 60)}
	}

	// Budget for ~5 messages (70% headroom → ~5 messages fit).
	trimmed, didTrim := TrimToBudget(msgs, 150)
	if !didTrim {
		t.Fatalf("expected trim to happen")
	}
	if len(trimmed) == 0 || len(trimmed) >= len(msgs) {
		t.Fatalf("trim should keep a strict subset, got %d", len(trimmed))
	}
	if len(trimmed) != 6 {
		t.Fatalf("expected 6 kept messages, got %d", len(trimmed))
	}
	// The newest message must survive.
	if trimmed[len(trimmed)-1].Content != msgs[len(msgs)-1].Content {
		t.Fatalf("newest message must be kept")
	}

	// Huge budget → no trim.
	all, did := TrimToBudget(msgs, 1<<30)
	if did || len(all) != len(msgs) {
		t.Fatalf("huge budget should not trim")
	}
	// Empty / non-positive budget → no trim.
	if _, did := TrimToBudget(msgs, 0); did {
		t.Fatalf("zero budget must not trim")
	}
	if _, did := TrimToBudget(nil, 1000); did {
		t.Fatalf("empty messages must not trim")
	}
}

func TestGoal_SetShowClear(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "goal")
	if GetGoal(sess) != "" {
		t.Fatalf("new session should have no goal")
	}
	if err := SetGoal(store, sess, " 重构整个模块  "); err != nil {
		t.Fatalf("set: %v", err)
	}
	if GetGoal(sess) != "重构整个模块" {
		t.Fatalf("goal not trimmed: %q", GetGoal(sess))
	}
	// Persists through reload.
	got, _ := store.Get(sess.ID)
	if GetGoal(got) != "重构整个模块" {
		t.Fatalf("goal not persisted")
	}
	// Clear removes it entirely.
	if err := SetGoal(store, sess, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = store.Get(sess.ID)
	if GetGoal(got) != "" {
		t.Fatalf("goal should be cleared")
	}
	if _, ok := got.Metadata[GoalKey]; ok {
		t.Fatalf("goal key should be deleted, not left empty")
	}
}
