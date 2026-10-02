package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/config"
)

// isolate points HOME/USERPROFILE and the working directory at empty temp dirs
// so a developer's own ~/.icode/config.yaml cannot join the test.
func isolate(t *testing.T) string {
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

func TestMasked(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"short", "*****"},
		{"sk-abcdefghijklmnop", "sk-******mnop"},
	}
	for _, c := range cases {
		if got := Masked(c.in); got != c.want {
			t.Errorf("Masked(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if strings.Contains(Masked("sk-abcdefghijklmnop"), "abcdefg") {
		t.Error("masked form must not leak the middle of the key")
	}
}

func TestSaveStoresEncryptedAndClears(t *testing.T) {
	isolate(t)
	const secret = "sk-test-abcdefghijklmnop"

	res, err := Save("deepseek", secret)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if res.Masked == secret || strings.Contains(res.Masked, "abcdefg") {
		t.Fatalf("result leaked the raw key: %q", res.Masked)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("config file contains the plaintext key:\n%s", data)
	}
	if !strings.Contains(string(data), "api_key_enc") {
		t.Fatalf("config file should hold api_key_enc, got:\n%s", data)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Providers["deepseek"].APIKey != secret {
		t.Fatalf("round-tripped key mismatch: %q", cfg.Providers["deepseek"].APIKey)
	}

	cl, err := Clear("deepseek")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !cl.Cleared {
		t.Fatal("clear should report it removed something")
	}
	again, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again.Providers["deepseek"].APIKey != "" {
		t.Fatal("key survived clear")
	}
	// Clearing twice is not an error — it just had nothing to remove.
	second, err := Clear("deepseek")
	if err != nil || second.Cleared {
		t.Fatalf("second clear: err=%v cleared=%v", err, second.Cleared)
	}
}

func TestSaveRejectsBlankInput(t *testing.T) {
	isolate(t)
	if _, err := Save("", "abc"); err == nil {
		t.Error("empty provider must be rejected")
	}
	if _, err := Save("deepseek", "   "); err == nil {
		t.Error("empty key must be rejected (it would silently log the user out)")
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-live" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "test-model"}, {"id": "test-model-2"}},
		})
	}))
	t.Cleanup(srv.Close)

	provider := "gateway"
	if _, err := Save(provider, "sk-live"); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	pc := cfg.Providers[provider]
	pc.APIBase = srv.URL + "/v1"
	cfg.Providers[provider] = pc
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.Fatalf("write base url: %v", err)
	}

	v := Verify(context.Background(), provider)
	if !v.OK {
		t.Fatalf("verify failed: err=%q skipped=%q", v.Err, v.Skipped)
	}
	if v.ModelN != 2 || v.Sample != "test-model" {
		t.Fatalf("unexpected verify result: %+v", v)
	}

	// A rejected key must come back as a failure with the vendor's message,
	// not as "0 models available".
	if _, err := Save(provider, "sk-wrong"); err != nil {
		t.Fatalf("save bad key: %v", err)
	}
	bad := Verify(context.Background(), provider)
	if bad.OK || bad.Err == "" {
		t.Fatalf("expected failure for a rejected key, got %+v", bad)
	}

	// No saved key at all is a skip, not a failure.
	if _, err := Clear("zhipu"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	none := Verify(context.Background(), "zhipu")
	if none.Skipped == "" || none.Err != "" {
		t.Fatalf("expected skip without a key, got %+v", none)
	}
}

func TestProviderFallsBackToOpenAICompat(t *testing.T) {
	p := Provider("some-gateway", config.ProviderCfg{APIKey: "k", APIBase: "https://example.invalid/v1"})
	if p.Name() != "some-gateway" {
		t.Fatalf("custom vendor name not preserved: %q", p.Name())
	}
	if !IsBuiltin("anthropic") || IsBuiltin("some-gateway") {
		t.Error("IsBuiltin disagrees with the built-in table")
	}
	if len(BuiltinNames()) != len(builtins) {
		t.Fatal("BuiltinNames must cover the whole table")
	}
}
