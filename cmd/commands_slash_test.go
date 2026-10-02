package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/app"
	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/tui"
	"github.com/ponygates/icode/internal/types"
)

// slashHarness drives chatCallback.OnSlashCommand and reports only the
// messages a single dispatch appended — the seam that proves the grouped
// dispatch behaves exactly like the original one big switch.
type slashHarness struct {
	c  *chatCallback
	tu *tui.TUI
}

func newSlashHarness() *slashHarness {
	tu := tui.New(tui.Config{Mode: tui.ModeAuto, Model: "m1", Provider: "p1", Version: "test"})
	return &slashHarness{c: &chatCallback{tui: tu}, tu: tu}
}

// run dispatches one command and returns the whole visible transcript
// afterwards. The transcript is reset first because several commands call
// LoadSession (resume / import / fork), which replaces the message list —
// a "messages added since the call" diff would hide their output.
func (h *slashHarness) run(cmd string, args ...string) string {
	h.tu.LoadSession(nil)
	h.c.OnSlashCommand(cmd, args)
	var b strings.Builder
	for _, m := range h.tu.Messages() {
		b.WriteString(fmt.Sprintf("[%s] %s\n", m.Role, m.Content))
	}
	return b.String()
}

func (h *slashHarness) withStore() types.SessionStore {
	store := session.NewStore()
	h.c.app = &app.App{SessStore: store}
	return store
}

func seedSession(t *testing.T, store types.SessionStore, id string, n int) *types.Session {
	t.Helper()
	sess := &types.Session{ID: id, Title: "seed-" + id, ModelID: "m1", ProviderName: "p1"}
	for i := 0; i < n; i++ {
		sess.Messages = append(sess.Messages,
			types.Message{Role: types.RoleUser, Content: fmt.Sprintf("q%d", i)},
			types.Message{Role: types.RoleAssistant, Content: fmt.Sprintf("a%d", i)},
		)
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess
}

// ── engine/config group ──────────────────────────────────────────

func TestSlash_Model(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/model"); got != "" {
		t.Errorf("/model without args should print nothing, got %q", got)
	}
	// Matching is case-insensitive (switch on strings.ToLower(cmd)).
	if got := h.run("/MODEL", "gpt-4o"); !strings.Contains(got, "Switched model to: gpt-4o") {
		t.Errorf("/MODEL gpt-4o -> %q", got)
	}
}

func TestSlash_Mode(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/mode"); got != "" {
		t.Errorf("/mode without args should print nothing, got %q", got)
	}
	if got := h.run("/mode", "Plan"); !strings.Contains(got, "Mode set to: Plan") {
		t.Errorf("/mode Plan -> %q", got)
	}
}

func TestSlash_ThinkingWithoutEngine(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/thinking", "on"); !strings.Contains(got, "引擎未初始化") {
		t.Errorf("/thinking with nil engine -> %q", got)
	}
}

func TestSlash_VoiceWithoutApp(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/voice"); !strings.Contains(got, "语音输入暂不可用") {
		t.Errorf("/voice with nil app -> %q", got)
	}
}

func TestSlash_Config(t *testing.T) {
	h := newSlashHarness()
	got := h.run("/config")
	if got == "" {
		t.Fatal("/config printed no message")
	}
}

func TestSlash_UnknownKeepsOriginalSpelling(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/ZzzNotACommand"); !strings.Contains(got, "Unknown command: /ZzzNotACommand") {
		t.Errorf("unknown command should echo the raw spelling, got %q", got)
	}
}

// ── session group ────────────────────────────────────────────────

func TestSlash_SessionID(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/session"); !strings.Contains(got, "No active session") {
		t.Errorf("/session without id -> %q", got)
	}
	h.c.sessionID = "s1"
	if got := h.run("/session"); !strings.Contains(got, "Active session: s1") {
		t.Errorf("/session with id -> %q", got)
	}
}

func TestSlash_ResumeUsage(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/resume"); !strings.Contains(got, "Usage: /resume") {
		t.Errorf("/resume without args -> %q", got)
	}
	store := h.withStore()
	seedSession(t, store, "src", 2)
	if got := h.run("/resume", "src", "--bogus"); !strings.Contains(got, "用法: /resume") {
		t.Errorf("bad flag should print usage, got %q", got)
	}
	if got := h.run("/resume", "src", "--lite=0"); !strings.Contains(got, "应为正整数") {
		t.Errorf("--lite=0 should reject, got %q", got)
	}
	if got := h.run("/resume", "ghost", "--lite"); !strings.Contains(got, "会话不存在: ghost") {
		t.Errorf("unknown id should report missing session, got %q", got)
	}
}

func TestSlash_ResumeLiteAndCompact(t *testing.T) {
	h := newSlashHarness()
	store := h.withStore()
	sess := seedSession(t, store, "src", 2)
	// No archived summary yet → compact resume refuses before resuming.
	if got := h.run("/resume", "src", "--compact"); !strings.Contains(got, "还没有存档摘要") {
		t.Errorf("compact without summary should refuse, got %q", got)
	}
	if h.c.sessionID != "" {
		t.Errorf("refused resume must not switch session, got %q", h.c.sessionID)
	}
	if err := sessionum.Save(store, sess, "存档摘要"); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	got := h.run("/resume", "src", "--lite=3")
	if !strings.Contains(got, "Resumed session src") {
		t.Errorf("resume should load the session, got %q", got)
	}
	if h.c.sessionID != "src" {
		t.Errorf("sessionID = %q, want src", h.c.sessionID)
	}
	if n := sessionum.LiteN(sess); n != 3 {
		t.Errorf("lite n = %d, want 3", n)
	}
	// NOTE (pre-existing, unchanged): the "✓ 已启用紧凑恢复" line is added
	// immediately before OnResume → LoadSession replaces the transcript, so it
	// is gone again in raw mode; assert on the persisted lite count instead.
	got = h.run("/resume", "src", "--compact=5")
	if !strings.Contains(got, "Resumed session src") {
		t.Errorf("compact resume -> %q", got)
	}
	if n := sessionum.LiteN(sess); n != 5 {
		t.Errorf("compact resume lite n = %d, want 5", n)
	}
}

func TestSlash_Restore(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/restore"); !strings.Contains(got, "Usage: /restore") {
		t.Errorf("/restore without args -> %q", got)
	}
	store := h.withStore()
	sess := seedSession(t, store, "s1", 1)
	if got := h.run("/restore", "s1"); !strings.Contains(got, "该会话未被删除") {
		t.Errorf("restoring a live session -> %q", got)
	}
	if err := sessionum.MarkDeleted(store, sess); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	if got := h.run("/restore", "s1"); !strings.Contains(got, "已恢复会话 s1") {
		t.Errorf("restore deleted session -> %q", got)
	}
	if got := h.run("/restore", "ghost"); !strings.Contains(got, "会话不存在: ghost") {
		t.Errorf("restore unknown id -> %q", got)
	}
}

func TestSlash_ExportImport(t *testing.T) {
	dir := t.TempDir()
	h := newSlashHarness()
	if got := h.run("/export"); !strings.Contains(got, "Usage: /export") {
		t.Errorf("/export without args -> %q", got)
	}
	store := h.withStore()
	seedSession(t, store, "s1", 2)
	if got := h.run("/export", "ghost"); !strings.Contains(got, "会话不存在: ghost") {
		t.Errorf("/export unknown id -> %q", got)
	}

	out := filepath.Join(dir, "dump.json")
	if got := h.run("/export", "s1", out); !strings.Contains(got, "已导出会话 s1") {
		t.Fatalf("/export s1 <path> -> %q", got)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("export did not write the file: %v", err)
	}

	h2 := newSlashHarness()
	if got := h2.run("/import"); !strings.Contains(got, "Usage: /import") {
		t.Errorf("/import without args -> %q", got)
	}
	h2.withStore()
	if got := h2.run("/import", filepath.Join(dir, "missing.json")); !strings.Contains(got, "读取失败") {
		t.Errorf("/import missing file -> %q", got)
	}
	// NOTE (pre-existing, unchanged): /import prints "已导入会话 …" and then
	// OnResume → LoadSession replaces the transcript, so only the resume line
	// survives; assert on the session switch instead.
	got := h2.run("/import", out)
	if !strings.Contains(got, "Resumed session") {
		t.Errorf("/import round trip -> %q", got)
	}
	if h2.c.sessionID == "" {
		t.Error("/import should activate the imported session")
	}
}

func TestSlash_Fork(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/fork"); !strings.Contains(got, "Usage: /fork") {
		t.Errorf("/fork without args -> %q", got)
	}
	store := h.withStore()
	seedSession(t, store, "src", 2)
	if got := h.run("/fork", "ghost"); !strings.Contains(got, "分叉失败") {
		t.Errorf("/fork unknown source -> %q", got)
	}
	got := h.run("/fork", "src@1")
	if !strings.Contains(got, "已从 src 分叉出独立会话") {
		t.Fatalf("/fork src@1 -> %q", got)
	}
	if !strings.Contains(got, "Resumed session") {
		t.Errorf("fork should activate the new session, got %q", got)
	}
}

func TestSlash_Goal(t *testing.T) {
	h := newSlashHarness()
	// No active session → the withSess guard fires.
	if got := h.run("/goal", "show"); !strings.Contains(got, "没有活跃会话") {
		t.Errorf("/goal show without session -> %q", got)
	}
	store := h.withStore()
	seedSession(t, store, "s1", 1)
	h.c.sessionID = "s1"

	if got := h.run("/goal", "set"); !strings.Contains(got, "用法: /goal set") {
		t.Errorf("/goal set without text -> %q", got)
	}
	if got := h.run("/goal", "set", "--verify"); !strings.Contains(got, "目标不能为空") {
		t.Errorf("/goal set with empty text -> %q", got)
	}
	if got := h.run("/goal", "set", "修好", "登录", "bug"); !strings.Contains(got, "已设置长目标") {
		t.Fatalf("/goal set -> %q", got)
	}
	sess, err := store.Get("s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sessionum.GetGoal(sess) != "修好 登录 bug" {
		t.Errorf("goal text = %q", sessionum.GetGoal(sess))
	}
	if got := h.run("/goal", "show"); !strings.Contains(got, "修好 登录 bug") {
		t.Errorf("/goal show -> %q", got)
	}
	if got := h.run("/goal", "set", "g", "--verify", "go test ./..."); !strings.Contains(got, "已设置可验收目标") {
		t.Errorf("/goal set --verify -> %q", got)
	}
	if got := h.run("/goal", "clear"); !strings.Contains(got, "已清除目标") {
		t.Errorf("/goal clear -> %q", got)
	}
	if got := h.run("/goal", "bogus"); !strings.Contains(got, "用法: /goal set <目标>") {
		t.Errorf("/goal bogus -> %q", got)
	}
}

func TestSlash_Budget(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/budget", "show"); !strings.Contains(got, "没有活跃会话") {
		t.Errorf("/budget show without session -> %q", got)
	}
	store := h.withStore()
	seedSession(t, store, "s1", 1)
	h.c.sessionID = "s1"

	if got := h.run("/budget", "set"); !strings.Contains(got, "用法: /budget set") {
		t.Errorf("/budget set without value -> %q", got)
	}
	if got := h.run("/budget", "set", "500"); !strings.Contains(got, "预算应为 ≥1000") {
		t.Errorf("/budget set 500 -> %q", got)
	}
	if got := h.run("/budget", "set", "16000"); !strings.Contains(got, "已设置 Token 预算: 16000") {
		t.Fatalf("/budget set 16000 -> %q", got)
	}
	if got := h.run("/budget", "show"); !strings.Contains(got, "当前 Token 预算: 16000") {
		t.Errorf("/budget show -> %q", got)
	}
	if got := h.run("/budget", "warn"); !strings.Contains(got, "当前预警阈值") {
		t.Errorf("/budget warn (read) -> %q", got)
	}
	if got := h.run("/budget", "warn", "80"); !strings.Contains(got, "已设置预算预警阈值: 80%") {
		t.Errorf("/budget warn 80 -> %q", got)
	}
	if got := h.run("/budget", "clear"); !strings.Contains(got, "已关闭 Token 预算") {
		t.Errorf("/budget clear -> %q", got)
	}
	if got := h.run("/budget", "zzz"); !strings.Contains(got, "用法: /budget set <上限>") {
		t.Errorf("/budget bogus -> %q", got)
	}
}

// ── clear / summarize / search ───────────────────────────────────

func TestSlash_Clear(t *testing.T) {
	h := newSlashHarness()
	store := h.withStore()
	sess := seedSession(t, store, "s1", 1)
	h.c.sessionID = "s1"
	h.tu.LoadSession([]tui.Message{{Role: tui.RoleUser, Content: "keep me"}})

	got := h.run("/clear")
	if !strings.Contains(got, "Session cleared (soft)") {
		t.Errorf("/clear -> %q", got)
	}
	if h.c.sessionID != "" {
		t.Errorf("sessionID should reset, got %q", h.c.sessionID)
	}
	if !sessionum.IsDeleted(sess) {
		t.Error("/clear should soft-delete (archive) the session")
	}
	// Without an active session it still resets the view.
	h2 := newSlashHarness()
	if got := h2.run("/clear"); !strings.Contains(got, "Session cleared (soft)") {
		t.Errorf("/clear without session -> %q", got)
	}
}

func TestSlash_Summarize(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/summarize"); got != "" {
		t.Errorf("/summarize without a session should be silent, got %q", got)
	}
	store := h.withStore()
	seedSession(t, store, "s1", 2)
	h.c.sessionID = "s1"
	if got := h.run("/summarize"); !strings.Contains(got, "会话摘要已存档") {
		t.Errorf("/summarize -> %q", got)
	}
	sess, err := store.Get("s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sessionum.Get(sess) == "" {
		t.Error("/summarize did not persist the archive summary")
	}
}

func TestSlash_Search(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/search"); !strings.Contains(got, "Usage: /search") {
		t.Errorf("/search without query -> %q", got)
	}
	if got := h.run("/search", "hello"); !strings.Contains(got, "No session store available") {
		t.Errorf("/search without store -> %q", got)
	}
	store := h.withStore()
	sess := seedSession(t, store, "s1", 1)
	if err := store.AppendMessage("s1", types.Message{Role: types.RoleUser, Content: "needle in the haystack"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := h.run("/search", "zzznotfound"); !strings.Contains(got, `No results for "zzznotfound"`) {
		t.Errorf("/search no matches -> %q", got)
	}
	got := h.run("/search", "needle")
	if !strings.Contains(got, "needle in the haystack") || !strings.Contains(got, sess.Title) {
		t.Errorf("/search matches -> %q", got)
	}
}

// ── staged-edits group ───────────────────────────────────────────

func TestSlash_ReviewNoStagedEdits(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/review"); !strings.Contains(got, "No staged edits") {
		t.Errorf("/review -> %q", got)
	}
}

func TestSlash_ApplyRejectNoStagedEdits(t *testing.T) {
	h := newSlashHarness()
	if got := h.run("/apply"); !strings.Contains(got, "No staged edits to apply") {
		t.Errorf("/apply -> %q", got)
	}
	if got := h.run("/reject"); !strings.Contains(got, "No staged edits to reject") {
		t.Errorf("/reject -> %q", got)
	}
}

func TestSlash_UndoWithoutUndoSystem(t *testing.T) {
	h := newSlashHarness()
	got := h.run("/undo")
	if !strings.Contains(got, "撤销") {
		t.Errorf("/undo should report the undo system state, got %q", got)
	}
}

func TestSlash_DispatchOrderIsDisjoint(t *testing.T) {
	// Every name the three groups claim must reach its own handler; a command
	// listed in two groups would silently change which one wins. /mode is the
	// TUI-forwarded one that the engine group owns, /clear the session group.
	for _, tc := range []struct {
		cmd  string
		args []string
		want string
	}{
		{"/mode", []string{"plan"}, "Mode set to: plan"},
		{"/clear", nil, "Session cleared"},
		{"/apply", nil, "No staged edits to apply"},
	} {
		h := newSlashHarness()
		if got := h.run(tc.cmd, tc.args...); !strings.Contains(got, tc.want) {
			t.Errorf("%s -> %q, want %q", tc.cmd, got, tc.want)
		}
	}
}
