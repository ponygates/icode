package slashui

import (
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/types"
)

func testBackend() (*Backend, types.SessionStore) {
	store := session.NewStore()
	return &Backend{SessStore: store}, store
}

func seedSession(t *testing.T, store types.SessionStore, id string) *types.Session {
	t.Helper()
	sess := &types.Session{ID: id, Title: "src", ModelID: "m1", ProviderName: "p1"}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: "q1"},
		{Role: types.RoleAssistant, Content: "a1"},
	}
	if err := store.Update(sess); err != nil {
		t.Fatalf("update: %v", err)
	}
	return sess
}

func TestCmdFork_BranchesNewSession(t *testing.T) {
	b, store := testBackend()
	src := seedSession(t, store, "src")
	st := &State{SessionID: "src"}

	res := cmdFork(b, st, []string{"src"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if res.SessionID == "" || res.SessionID == "src" {
		t.Fatalf("fork should activate a new session, got %q", res.SessionID)
	}
	forked, err := store.Get(res.SessionID)
	if err != nil {
		t.Fatalf("forked session missing: %v", err)
	}
	if len(forked.Messages) != 2 {
		t.Fatalf("fork should copy all messages, got %d", len(forked.Messages))
	}
	if st.SessionID != forked.ID {
		t.Fatalf("state session id not updated")
	}
	if src.Messages[0].Content != forked.Messages[0].Content {
		t.Fatalf("fork prefix content mismatch")
	}
}

func TestCmdFork_PrefixAtIndex(t *testing.T) {
	b, store := testBackend()
	seedSession(t, store, "src")
	st := &State{SessionID: "src"}

	res := cmdFork(b, st, []string{"src@1"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	forked, _ := store.Get(res.SessionID)
	if len(forked.Messages) != 1 {
		t.Fatalf("fork@1 should keep 1 message, got %d", len(forked.Messages))
	}
}

func TestCmdFork_Errors(t *testing.T) {
	b, store := testBackend()
	seedSession(t, store, "src")
	st := &State{SessionID: "src"}

	if res := cmdFork(b, st, nil); res.IsError {
		t.Fatalf("missing args should be usage, not error")
	}
	if res := cmdFork(b, st, []string{"nope"}); !res.IsError {
		t.Fatalf("missing source should error")
	}
	empty := &types.Session{ID: "empty", Title: "e"}
	_ = store.Create(empty)
	if res := cmdFork(b, st, []string{"empty"}); !res.IsError {
		t.Fatalf("empty source should error")
	}
	if res := cmdFork(b, st, []string{"src@abc"}); !res.IsError {
		t.Fatalf("non-numeric index should error")
	}
}

func TestCmdGoal_SetShowClear(t *testing.T) {
	b, store := testBackend()
	seedSession(t, store, "g")
	st := &State{SessionID: "g"}

	res := cmdGoal(b, st, []string{"set", "完成重构"})
	if res.IsError || !strings.Contains(res.Output, "完成重构") {
		t.Fatalf("set failed: %s", res.Output)
	}
	res = cmdGoal(b, st, []string{"show"})
	if !strings.Contains(res.Output, "完成重构") {
		t.Fatalf("show should contain goal: %s", res.Output)
	}
	sess, _ := store.Get("g")
	if sessionum.GetGoal(sess) != "完成重构" {
		t.Fatalf("goal not persisted")
	}
	res = cmdGoal(b, st, []string{"clear"})
	if res.IsError {
		t.Fatalf("clear failed: %s", res.Output)
	}
	sess, _ = store.Get("g")
	if sessionum.GetGoal(sess) != "" {
		t.Fatalf("goal should be cleared")
	}
	// No args → friendly usage when unset.
	res = cmdGoal(b, st, nil)
	if res.IsError || !strings.Contains(res.Output, "当前没有目标") {
		t.Fatalf("unset show should report no goal: %s", res.Output)
	}
}

func TestCmdResume_Lite(t *testing.T) {
	b, store := testBackend()
	seedSession(t, store, "src")
	st := &State{SessionID: ""}

	// No summary yet → lite must refuse.
	res := cmdResume(b, st, []string{"src", "--lite"})
	if !res.IsError || !strings.Contains(res.Output, "还没有存档摘要") {
		t.Fatalf("lite without summary should refuse: %s", res.Output)
	}

	// Archive a summary, then lite works.
	sess, _ := store.Get("src")
	_ = sessionum.Save(store, sess, sessionum.Generate(sess, "m", "p", "agent"))
	res = cmdResume(b, st, []string{"src", "--lite=2"})
	if res.IsError {
		t.Fatalf("lite resume failed: %s", res.Output)
	}
	sess, _ = store.Get("src")
	if sessionum.LiteN(sess) != 2 {
		t.Fatalf("lite_n should be 2, got %d", sessionum.LiteN(sess))
	}
	if !strings.Contains(res.Output, "lite 模式") {
		t.Fatalf("output should mention lite: %s", res.Output)
	}
	if res.SessionID != "src" {
		t.Fatalf("lite resume should stay on src")
	}

	// Plain resume clears lite? No — lite is a session property that persists.
	// Clearing is via --lite=0 semantics; verify a bad flag errors.
	if r := cmdResume(b, st, []string{"src", "--bogus"}); !r.IsError {
		t.Fatalf("unknown flag should error")
	}
}

func TestCmdGoal_ArchivesOnExitPaths(t *testing.T) {
	b, store := testBackend()
	seedSession(t, store, "src")
	other := seedSession(t, store, "other")
	st := &State{SessionID: "src", Model: "m1", Provider: "p1"}

	// /resume away should archive src's summary.
	cmdResume(b, st, []string{"other"})
	src, _ := store.Get("src")
	if sessionum.Get(src) == "" {
		t.Fatalf("leaving a session via /resume should archive its summary")
	}
	_ = other
}
