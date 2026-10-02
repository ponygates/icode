package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/config"
)

// isolateLogin points HOME/USERPROFILE and the cwd at empty temp dirs so the
// test writes its own config file instead of the developer's.
func isolateLogin(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ICODE_MANAGED_CONFIG", filepath.Join(home, "nonexistent-managed.yaml"))
	t.Chdir(t.TempDir())
	for _, k := range []string{"DEEPSEEK_API_KEY", "OPENROUTER_API_KEY", "ZHIPU_API_KEY", "KIMI_API_KEY"} {
		t.Setenv(k, "")
	}
	return home
}

func TestPromptKeyMasksAndSubmits(t *testing.T) {
	tu := newTestTUI()
	var got string
	tu.startPrompt("API Key: ", true, func(text string) { got = text }, nil)
	if !tu.promptActive() {
		t.Fatal("prompt should be active after startPrompt")
	}
	for _, r := range "sk-abc123" {
		if !tu.handlePromptKey(r) {
			t.Fatalf("prompt should swallow printable key %q", r)
		}
	}
	if tu.inputBuf != "" {
		t.Errorf("secret leaked into the input buffer: %q", tu.inputBuf)
	}
	box := tu.drawPromptBox(60, 24)
	if strings.Contains(box, "abc123") {
		t.Errorf("prompt box must mask the secret, got %q", box)
	}
	if !strings.Contains(box, "******") {
		t.Errorf("prompt box should show masked dots, got %q", box)
	}
	if !tu.handlePromptKey(0x08) { // backspace one char
		t.Fatal("backspace should be handled")
	}
	if !tu.handlePromptKey('\r') {
		t.Fatal("enter should be handled")
	}
	if tu.promptActive() {
		t.Fatal("prompt should close on submit")
	}
	if got != "sk-abc12" {
		t.Fatalf("callback got %q, want %q", got, "sk-abc12")
	}
}

func TestPromptCancelKeepsTypedInput(t *testing.T) {
	tu := newTestTUI()
	tu.inputBuf = "half-typed message"
	tu.startPrompt("提供商: ", false, func(string) { t.Error("onDone must not fire on cancel") }, nil)
	if !tu.handlePromptKey(0x1b) { // Esc
		t.Fatal("esc should be handled")
	}
	if tu.inputBuf != "half-typed message" {
		t.Fatalf("cancel should restore the parked input, got %q", tu.inputBuf)
	}
}

func TestScrubHistoryRemovesInlineKey(t *testing.T) {
	isolateLogin(t)
	tu := newTestTUI()
	tu.pushHistory("hello")
	// The real key handler records the typed line before dispatching the
	// slash command; /login must take it back out again.
	typed := "/login gateway sk-tui-abcdefghijklmnop"
	tu.pushHistory(typed)
	tu.pushHistory("again " + typed)
	if len(tu.history) != 3 {
		t.Fatalf("history not seeded: %v", tu.history)
	}
	tu.scrubHistory("/login")
	for _, h := range tu.history {
		if strings.Contains(h, "sk-tui") || strings.HasPrefix(h, "/login") {
			t.Fatalf("inline key survived history scrub: %v", tu.history)
		}
	}
	if len(tu.history) != 1 || tu.history[0] != "hello" {
		t.Fatalf("scrub removed unrelated entries: %v", tu.history)
	}
}

func TestLoginCommandSavesWithoutLeaking(t *testing.T) {
	home := isolateLogin(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m1"}}})
	}))
	t.Cleanup(srv.Close)

	// The vendor's endpoint has to be on disk first, otherwise the async
	// verification would dial the real api.openai.com with a fake key.
	cfg := config.Default()
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.ProviderCfg{}
	}
	pc := cfg.Providers["gateway"]
	pc.APIBase = srv.URL + "/v1"
	cfg.Providers["gateway"] = pc
	path := filepath.Join(home, ".icode", "config.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	const secret = "sk-tui-abcdefghijklmnop"
	tu := newTestTUI()
	tu.submit("/login gateway " + secret)

	joined := strings.Join(messagesText(tu), "\n")
	if !strings.Contains(joined, "已保存 gateway") {
		t.Fatalf("/login did not report the save:\n%s", joined)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("plaintext key on disk:\n%s", data)
	}
	if strings.Contains(joined, secret) {
		t.Fatalf("raw key echoed into the transcript:\n%s", joined)
	}

	// /logout needs an explicit yes, so a stray keystroke cannot delete a key.
	// The question lives in the modal prompt overlay, not in the transcript.
	tu.submit("/logout gateway")
	tu.mu.Lock()
	p := tu.prompt
	tu.mu.Unlock()
	if p == nil || !strings.Contains(p.label, "确认清除") {
		t.Fatalf("/logout should ask for confirmation, prompt=%+v", p)
	}
	if p.secret {
		t.Error("confirmation prompt is not a secret")
	}
}
