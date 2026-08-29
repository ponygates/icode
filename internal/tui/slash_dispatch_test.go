package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/types"
)

func newTestTUI() *TUI {
	return New(Config{
		Mode:     ModeAuto,
		Model:    "openrouter/free",
		Provider: "openrouter",
		Version:  "test",
		Callback: nil,
	})
}

// countSystem returns how many system messages are currently rendered.
func countSystem(t *TUI) int {
	n := 0
	for _, m := range t.messages {
		if m.Role == RoleSystem {
			n++
		}
	}
	return n
}

func TestSubmitSlashHelp(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/help")
	if countSystem(tu) == 0 {
		t.Fatalf("/help produced no system message; messages=%d", len(tu.messages))
	}
	// The help text should mention at least one command like /model.
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "/model") {
		t.Errorf("/help output missing expected command list:\n%s", joined)
	}
}

func TestCompleteSlashCommand(t *testing.T) {
	tu := newTestTUI()
	// Exact command passes through untouched.
	if got, ok := tu.completeSlashCommand("/model gpt-4o"); ok || got != "/model gpt-4o" {
		t.Fatalf("exact command should pass through, got %q ok=%v", got, ok)
	}
	// Prefix "/c" completes to the first "/c*" command in slashDefs order.
	got, ok := tu.completeSlashCommand("/c")
	if !ok {
		t.Fatalf("prefix /c should complete, got ok=%v", ok)
	}
	if !strings.HasPrefix(got, "/c") || got == "/c" {
		t.Fatalf("completed /c should expand to a full command, got %q", got)
	}
	// Trailing arguments are preserved.
	got, ok = tu.completeSlashCommand("/m claude")
	if !ok {
		t.Fatalf("/m should complete with args, got ok=%v", ok)
	}
	if !strings.HasPrefix(got, "/m") || !strings.HasSuffix(got, "claude") {
		t.Fatalf("completed /m claude should keep args, got %q", got)
	}
	// Bare "/" never dispatches an empty command.
	got, ok = tu.completeSlashCommand("/")
	if !ok || !strings.HasPrefix(got, "/") || got == "/" {
		t.Fatalf("bare / should complete to a real command, got %q ok=%v", got, ok)
	}
	// Unknown commands are left alone (still routed to custom/callback paths).
	if got, ok := tu.completeSlashCommand("/zzznope"); ok || got != "/zzznope" {
		t.Fatalf("unknown command should pass through, got %q ok=%v", got, ok)
	}
	// Non-slash text is never touched.
	if got, ok := tu.completeSlashCommand("hello world"); ok || got != "hello world" {
		t.Fatalf("plain text should pass through, got %q ok=%v", got, ok)
	}
}

func TestEnterCompletesSlashPrefix(t *testing.T) {
	tu := newTestTUI()
	// Type "/mo" then press Enter — the dispatch must receive the completed
	// command ("/mode" is the first "/mo*" match), never a dangling "/mo".
	// handleKey swallows Enter for the welcome screen if open; dismiss first.
	tu.dismissWelcome()
	for _, ch := range "/mo" {
		if !tu.handleKey(ch) {
			t.Fatalf("handleKey(%q) returned false", string(ch))
		}
	}
	if tu.inputBuf != "/mo" {
		t.Fatalf("inputBuf = %q, want /mo", tu.inputBuf)
	}
	if !tu.handleKey('\r') {
		t.Fatalf("handleKey(Enter) returned false")
	}
	// The completed command should have produced a system response, proving
	// a full command (not "/mo") was dispatched.
	if countSystem(tu) == 0 {
		t.Fatalf("Enter on /mo produced no system response — a dangling prefix may have been submitted; messages=%d", len(tu.messages))
	}
}

func TestSubmitSlashModel(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/model claude-3-opus")
	if tu.model != "claude-3-opus" {
		t.Errorf("model not switched: got %q", tu.model)
	}
	if countSystem(tu) == 0 {
		t.Fatalf("/model produced no system message")
	}
}

func TestSubmitSlashUnknown(t *testing.T) {
	tu := newTestTUI()
	// Unknown command with nil callback should not panic and should not
	// echo the literal text as a user message.
	tu.submit("/thisdoesnotexist")
	for _, m := range tu.messages {
		if m.Role == RoleUser {
			t.Errorf("unknown slash command echoed as user message: %q", m.Content)
		}
	}
}

func TestSubmitUserMessage(t *testing.T) {
	tu := newTestTUI()
	tu.submit("hello world")
	found := false
	for _, m := range tu.messages {
		if m.Role == RoleUser && m.Content == "hello world" {
			found = true
		}
	}
	if !found {
		t.Errorf("user message not recorded; messages=%d", len(tu.messages))
	}
}

// messagesText flattens all messages to their content for substring checks.
func messagesText(t *TUI) []string {
	out := make([]string, 0, len(t.messages))
	for _, m := range t.messages {
		out = append(out, m.Content)
	}
	return out
}

// TestRawKeyInputSlash simulates the real raw-mode path: a fresh session
// starts with the welcome banner visible; the user types "/help" one rune at
// a time (which also dismisses the welcome screen) and presses Enter. This
// exercises welcome dismissal + autocomplete + Enter routing — the layers the
// direct submit() test skips.
func TestRawKeyInputSlash(t *testing.T) {
	tu := newTestTUI()
	if !tu.welcomeVisible {
		t.Fatalf("expected welcomeVisible=true on fresh TUI")
	}
	for _, r := range "/help" {
		if !tu.handleKey(r) {
			t.Fatalf("handleKey(%q) signalled exit", r)
		}
	}
	if tu.welcomeVisible {
		t.Errorf("welcome banner should have been dismissed by first keystroke")
	}
	if tu.inputBuf != "/help" {
		t.Fatalf("inputBuf after typing = %q, want /help", tu.inputBuf)
	}
	// Press Enter.
	if !tu.handleKey('\r') {
		t.Fatalf("handleKey(Enter) signalled exit")
	}
	if countSystem(tu) == 0 {
		t.Fatalf("typing /help + Enter produced no system message; messages=%d", len(tu.messages))
	}
}

// TestModelPicker verifies /model with no argument prints the model list (in
// line mode) and that /model <n> (index) and /model <id> both switch the active
// model. This is the regression test for the "typed /model but no switcher
// appeared" report.
func TestModelPicker(t *testing.T) {
	tu := newTestTUI()
	tu.SetModels([]string{"openrouter/free", "claude-3-opus", "gpt-4"})
	tu.model = "openrouter/free"
	tu.modelIdx = 0

	// Non-raw mode: /model prints a static list (no interactive overlay).
	tu.submit("/model")
	if tu.modelPickerOpen {
		t.Errorf("non-raw /model should not open the interactive overlay")
	}
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "openrouter/free") || !strings.Contains(joined, "gpt-4") {
		t.Errorf("/model list missing models:\n%s", joined)
	}
	if !strings.Contains(joined, "(当前)") {
		t.Errorf("/model list missing current-model marker:\n%s", joined)
	}

	tu.submit("/model 2")
	if tu.model != "claude-3-opus" {
		t.Errorf("/model 2 did not switch to 2nd model: got %q", tu.model)
	}

	tu.submit("/model gpt-4")
	if tu.model != "gpt-4" {
		t.Errorf("/model gpt-4 did not switch by id: got %q", tu.model)
	}
}

// TestHistoryRecall verifies the up/down arrow history navigation logic used
// by raw mode (and now also recorded in line mode). Regression for the
// "arrow keys don't show history" report.
func TestHistoryRecall(t *testing.T) {
	tu := newTestTUI()
	tu.pushHistory("first")
	tu.pushHistory("second")
	tu.pushHistory("third")
	if len(tu.history) != 3 {
		t.Fatalf("history len = %d, want 3", len(tu.history))
	}
	tu.historyPrev()
	if tu.inputBuf != "third" {
		t.Errorf("historyPrev#1 = %q, want third", tu.inputBuf)
	}
	tu.historyPrev()
	if tu.inputBuf != "second" {
		t.Errorf("historyPrev#2 = %q, want second", tu.inputBuf)
	}
	tu.historyNext()
	if tu.inputBuf != "third" {
		t.Errorf("historyNext = %q, want third", tu.inputBuf)
	}
	tu.historyNext()
	if tu.inputBuf != "" {
		t.Errorf("historyNext past end = %q, want empty", tu.inputBuf)
	}
}

// TestModelPickerInteractive drives the arrow-key / Enter selection path that
// raw mode uses, including the ESC[ A/B CSI sequences. This is the regression
// test for "can't use up/down to select and switch" — the picker must respond
// to navigation keys, not just the /model <n> syntax.
func TestModelPickerInteractive(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.SetModels([]string{"a", "b", "c", "d"})
	tu.modelIdx = 0

	// /model with no arg opens the interactive picker.
	tu.submit("/model")
	if !tu.modelPickerOpen {
		t.Fatalf("picker did not open on /model")
	}

	// Down, Down -> highlight index 2.
	feedEsc(t, tu, "\x1b[B")
	feedEsc(t, tu, "\x1b[B")
	if tu.modelPickerIdx != 2 {
		t.Fatalf("after two downs modelPickerIdx=%d, want 2", tu.modelPickerIdx)
	}
	// Up -> highlight index 1.
	feedEsc(t, tu, "\x1b[A")
	if tu.modelPickerIdx != 1 {
		t.Fatalf("after up modelPickerIdx=%d, want 1", tu.modelPickerIdx)
	}
	// Clamp: many downs stay at last index.
	for i := 0; i < 10; i++ {
		feedEsc(t, tu, "\x1b[B")
	}
	if tu.modelPickerIdx != 3 {
		t.Fatalf("down should clamp at last index, got %d", tu.modelPickerIdx)
	}
	// Enter confirms the highlighted model.
	if !tu.handleKey('\r') {
		t.Fatalf("Enter signalled exit")
	}
	if tu.model != "d" {
		t.Errorf("Enter did not select highlighted model: got %q, want d", tu.model)
	}
	if tu.modelPickerOpen {
		t.Errorf("picker should be closed after Enter")
	}
}

// TestModelPickerEscCancel verifies Esc cancels the picker without changing
// the active model.
func TestModelPickerEscCancel(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.SetModels([]string{"a", "b", "c"})
	tu.model = "a"
	tu.modelIdx = 0
	tu.submit("/model")
	// Move highlight, then Esc cancels.
	feedEsc(t, tu, "\x1b[B")
	if !tu.modelPickerOpen {
		t.Fatalf("picker should be open before Esc")
	}
	feedEscLone(t, tu) // plain Esc (empty follow-up buffer) cancels
	if tu.modelPickerOpen {
		t.Errorf("Esc did not cancel the picker")
	}
	if tu.model != "a" {
		t.Errorf("Esc should not change model, got %q", tu.model)
	}
}

// TestEscInterruptStreaming verifies the ESC-interrupt fix: a lone Esc while
// streaming must call the callback's OnInterrupt (previously it blocked
// forever on ReadRune waiting for a second byte). The streaming flag itself
// is cleared by drainStream when the engine closes the event channel, not by
// the ESC handler.
func TestEscInterruptStreaming(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.streaming = true
	interrupted := false
	tu.callback = &testCallback{onInterrupt: func() { interrupted = true }}
	feedEscLone(t, tu)
	if !interrupted {
		t.Error("lone Esc while streaming must call OnInterrupt")
	}
}

// testCallback is a minimal Callback that records interrupt calls.
type testCallback struct {
	onInterrupt func()
}

func (c *testCallback) OnSend(text string, _ []types.Attachment) {}
func (c *testCallback) OnSlashCommand(cmd string, args []string) {}
func (c *testCallback) OnPermissionResponse(decision string)     {}
func (c *testCallback) OnPlanConfirm()                           {}
func (c *testCallback) OnInterrupt() {
	if c.onInterrupt != nil {
		c.onInterrupt()
	}
}
func (c *testCallback) OnListSessions() string                                 { return "" }
func (c *testCallback) OnResume(id string) string                              { return "" }
func (c *testCallback) OnCompactSummarize(instruction string) string           { return "" }
func (c *testCallback) TodoCounts() (int, int, int, int)                       { return 0, 0, 0, 0 }
func (c *testCallback) SessionID() string                                      { return "" }
func (c *testCallback) OnStatus() string                                       { return "" }
func (c *testCallback) OnTokenStats() string                                   { return "" }
func (c *testCallback) OnOutputStyle(style string) string                      { return "" }
func (c *testCallback) OnAddDir(dir string) string                             { return "" }
func (c *testCallback) OnUpdateModels() string                                 { return "" }
func (c *testCallback) OnSetMode(mode string) string                           { return "" }
func (c *testCallback) OnRenameSession(title string) string                    { return "" }
func (c *testCallback) OnSetAskUser(fn func(string, []string) (int, error))     {}
func (c *testCallback) OnAddCustomModel(provider, modelID, name string) string { return "" }
func (c *testCallback) OnRemoveCustomModel(id string) string                   { return "" }
func (c *testCallback) LSPQuery(sub string, args []string) string              { return "" }
func (c *testCallback) KnowledgeQuery(query string) string                     { return "" }
func (c *testCallback) CreateIdleTask(name, prompt string) string              { return "" }
func (c *testCallback) OnPermissionNote(toolPrompt, note string)               {}
func (c *testCallback) OnListSessionsStructured(limit int) []SessionInfo       { return nil }

// feedEsc pushes an ESC sequence (leading 0x1b + follow-up bytes) into the
// key channel — the same path the key pump uses in production — then dispatches
// ESC exactly as the raw-mode main loop does. handleKey reads the follow-up
// bytes from keyCh, matching the live input path.
func feedEsc(t *testing.T, tu *TUI, seq string) {
	if len(seq) == 0 || seq[0] != 0x1b {
		t.Fatalf("feedEsc expects a sequence starting with ESC, got %q", seq)
	}
	// Push only the follow-up bytes: ESC itself is handed to handleKey, just
	// like the main loop passes the rune it received.
	for _, r := range seq[1:] {
		tu.keyCh <- r
	}
	if !tu.handleKey('\x1b') {
		t.Fatalf("handleKey(ESC %q) signalled exit", seq)
	}
}

// feedEscLone dispatches a plain Esc with no follow-up bytes — keyCh stays
// empty so handleKey's ESC branch hits the escFollowTimeout window and treats
// it as a standalone Esc (the real-terminal shape of a lone Esc keypress).
func feedEscLone(t *testing.T, tu *TUI) {
	if !tu.handleKey('\x1b') {
		t.Fatalf("handleKey(ESC lone) signalled exit")
	}
}

// TestModelPickerOverlayWindowing guards the fix for "highlight scrolls out of
// view": with a tall model list (100) in a short viewport (bodyH=17), the
// overlay must scroll its internal window so the highlighted row stays visible
// and the panel never exceeds the available body height.
func TestModelPickerOverlayWindowing(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.models = make([]string, 100)
	for i := range tu.models {
		tu.models[i] = fmt.Sprintf("m%d", i)
	}
	tu.modelIdx = 0
	tu.modelPickerOpen = true
	tu.modelPickerIdx = 50 // jump the highlight far down
	tu.modelPickerTop = 0

	lines := tu.modelPickerOverlay(80, 17)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "m50") {
		t.Errorf("highlighted model m50 not visible in overlay window (bodyH=17):\n%s", joined)
	}
	if len(lines) > 17 {
		t.Errorf("overlay exceeds body height: %d lines (max 17)", len(lines))
	}
}

// TestSubmitSlashKeys verifies that /keys (documented in slashDefs) actually
// takes effect and prints the API-key status, instead of silently falling
// through to the default branch (which used to route it to the engine as a
// no-op). Regression for the "documented command does nothing" report.
func TestSubmitSlashKeys(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/keys")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "API 密钥状态") {
		t.Errorf("/keys produced no API-key-status output:\n%s", joined)
	}
}

// TestSubmitSlashMultiline verifies that /multiline only toggles multiline mode
// and does NOT leak the API-key-status block that used to be erroneously merged
// into its case.
func TestSubmitSlashMultiline(t *testing.T) {
	tu := newTestTUI()
	before := tu.multiline
	tu.submit("/multiline")
	if tu.multiline == before {
		t.Errorf("/multiline did not toggle: still %v", tu.multiline)
	}
	joined := strings.Join(messagesText(tu), "\n")
	if strings.Contains(joined, "API 密钥状态") {
		t.Errorf("/multiline should not print API-key status:\n%s", joined)
	}
}

// TestSubmitSlashCostPanel verifies /cost renders the token/cost panel.
func TestSubmitSlashCostPanel(t *testing.T) {
	tu := newTestTUI()
	tu.promptTokens = 1234
	tu.completionTokens = 56
	tu.cacheHitRate = 0.72
	tu.submit("/cost")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "费用（本次会话）") {
		t.Errorf("/cost did not render the cost panel:\n%s", joined)
	}
	if !strings.Contains(joined, "72%") {
		t.Errorf("/cost did not show cache hit rate:\n%s", joined)
	}
}

// TestSubmitSlashUndo verifies /undo falls back to the no-session message when
// there is no active session (mirrors /rewind behavior).
func TestSubmitSlashUndo(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/undo")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "没有活跃会话") {
		t.Errorf("/undo without session did not print the expected message:\n%s", joined)
	}
}

// TestSubmitSlashShare verifies /share exports a timestamped Markdown file and
// prints its absolute path. The generated file is removed afterwards.
func TestSubmitSlashShare(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/share")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "可分享副本") {
		t.Errorf("/share did not print the shareable path:\n%s", joined)
	}
	// Clean up the generated export file(s).
	matches, _ := filepath.Glob("icode-share-*.md")
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// TestSubmitSlashOutputStyle verifies /output-style validates input, shows the
// current style with no arg, and persists a valid style (config restored).
func TestSubmitSlashOutputStyle(t *testing.T) {
	snap := snapshotConfig(t)
	defer restoreConfig(t, snap)

	tu := newTestTUI()
	tu.submit("/output-style bogus")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "无效风格") {
		t.Errorf("/output-style bogus did not error:\n%s", joined)
	}

	tu2 := newTestTUI()
	tu2.submit("/output-style")
	joined = strings.Join(messagesText(tu2), "\n")
	if !strings.Contains(joined, "当前输出风格") {
		t.Errorf("/output-style did not show current style:\n%s", joined)
	}

	tu3 := newTestTUI()
	tu3.submit("/output-style concise")
	joined = strings.Join(messagesText(tu3), "\n")
	if !strings.Contains(joined, "concise") {
		t.Errorf("/output-style concise did not confirm:\n%s", joined)
	}
}

// TestSubmitSlashUpdate verifies /update without an engine prints the
// not-initialized message.
func TestSubmitSlashUpdate(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/update")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "引擎未初始化") {
		t.Errorf("/update without engine did not print the expected message:\n%s", joined)
	}
}

// TestSubmitSlashAddDir verifies /add-dir without an engine prints the
// not-initialized message (with arg), and lists/usage without arg.
func TestSubmitSlashAddDir(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/add-dir /tmp")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "引擎未初始化") {
		t.Errorf("/add-dir without engine did not print the expected message:\n%s", joined)
	}

	tu2 := newTestTUI()
	tu2.submit("/add-dir")
	joined = strings.Join(messagesText(tu2), "\n")
	if !strings.Contains(joined, "工作目录") {
		t.Errorf("/add-dir (no arg) did not list or show usage:\n%s", joined)
	}
}

// snapshotConfig captures the current on-disk config bytes so a test that
// persists changes (/vim, /statusline, /config set, ...) can restore them
// afterwards and avoid polluting the developer's real ~/.icode/config.yaml.
// We snapshot the raw file (not the struct) because config.Config embeds a
// sync.RWMutex and would trip go vet's copylocks check on a by-value copy.
func snapshotConfig(t *testing.T) []byte {
	b, err := os.ReadFile(config.DefaultPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("snapshot config: %v", err)
	}
	return b
}

func restoreConfig(t *testing.T, b []byte) {
	p := config.DefaultPath()
	if b == nil {
		// Original file did not exist; remove anything the test may have created.
		_ = os.Remove(p)
		return
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("restore config: %v", err)
	}
}

// TestSubmitSlashVim verifies /vim toggles vim key-binding mode (Claude Code
// parity) and flips the in-memory flag.
func TestSubmitSlashVim(t *testing.T) {
	snap := snapshotConfig(t)
	defer restoreConfig(t, snap)
	tu := newTestTUI()
	before := tu.vimMode
	tu.submit("/vim")
	if tu.vimMode == before {
		t.Errorf("/vim did not toggle: still %v", tu.vimMode)
	}
}

// TestSubmitSlashStatusline verifies /statusline toggles the bottom status bar
// (Claude Code parity) and flips the in-memory flag.
func TestSubmitSlashStatusline(t *testing.T) {
	snap := snapshotConfig(t)
	defer restoreConfig(t, snap)
	tu := newTestTUI()
	before := tu.statusVisible
	tu.submit("/statusline")
	if tu.statusVisible == before {
		t.Errorf("/statusline did not toggle: still %v", tu.statusVisible)
	}
}

// TestSubmitSlashConfigSet verifies /config set <key> <value> applies common
// settings immediately.
func TestSubmitSlashConfigSet(t *testing.T) {
	snap := snapshotConfig(t)
	defer restoreConfig(t, snap)
	tu := newTestTUI()
	tu.submit("/config set theme dark")
	if tu.theme != "dark" {
		t.Errorf("/config set theme dark did not apply: got %q", tu.theme)
	}
	tu.submit("/config set lang en")
	if tu.lang != "en" {
		t.Errorf("/config set lang en did not apply: got %q", tu.lang)
	}
}

// TestSubmitSlashMCP verifies /mcp (list) renders without error and is the
// Claude Code-parity management command.
func TestSubmitSlashMCP(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/mcp")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "MCP") {
		t.Errorf("/mcp produced no MCP output:\n%s", joined)
	}
}

// TestSubmitSlashReleaseNotes verifies /release-notes produces output (either
// the notes or a clear "not found" message) without panicking.
func TestSubmitSlashReleaseNotes(t *testing.T) {
	tu := newTestTUI()
	tu.submit("/release-notes")
	if countSystem(tu) == 0 {
		t.Fatalf("/release-notes produced no system message")
	}
}

// TestSubmitSlashModeShortcuts verifies /plan, /ask, /debug map to the right
// modes (Claude Code / Codex parity shortcuts for the mode switch).
func TestSubmitSlashModeShortcuts(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"/plan", "plan"},
		{"/ask", "ask"},
		{"/debug", "agent"},
	}
	for _, c := range cases {
		tu := newTestTUI()
		tu.submit(c.cmd)
		if tu.mode != c.want {
			t.Errorf("%s -> mode %q, want %q", c.cmd, tu.mode, c.want)
		}
	}
}

// TestSubmitSlashRename verifies /rename drives the callback (backend store
// retitle) and reports the new title. It should not panic when the callback
// is nil (engine not ready).
func TestSubmitSlashRename(t *testing.T) {
	tu := newTestTUI()
	tu.callback = &testCallback{} // OnRenameSession returns ""
	tu.submit("/rename 我的新会话")
	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "我的新会话") {
		t.Errorf("/rename output missing new title:\n%s", joined)
	}
	// No callback → friendly message, no panic.
	tu2 := newTestTUI()
	tu2.submit("/rename 会话")
	if countSystem(tu2) == 0 {
		t.Fatalf("/rename with no callback produced no system message")
	}
}
