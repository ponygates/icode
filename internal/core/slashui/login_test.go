package slashui

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

// isolateAuth gives the test an empty HOME/cwd so it writes its own config.
func isolateAuth(t *testing.T) string {
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

func TestSlashLoginSavesVerifiesAndPushes(t *testing.T) {
	home := isolateAuth(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "glm-4"}}})
	}))
	t.Cleanup(srv.Close)

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

	var pushedProvider, pushedKey string
	b := &Backend{SetCredentials: func(provider, key string) string {
		pushedProvider, pushedKey = provider, key
		return ""
	}}
	const secret = "sk-slash-abcdefghijklmnop"
	res := Execute(context.Background(), b, &State{}, "/login gateway "+secret)
	if res.IsError {
		t.Fatalf("login errored: %s", res.Output)
	}
	if !strings.Contains(res.Output, "已保存 gateway") {
		t.Fatalf("unexpected output: %s", res.Output)
	}
	if strings.Contains(res.Output, secret) {
		t.Fatalf("raw key echoed to the frontend: %s", res.Output)
	}
	if !strings.Contains(res.Output, "连通性验证通过") {
		t.Fatalf("expected a verification line, got: %s", res.Output)
	}
	if pushedProvider != "gateway" || pushedKey != secret {
		t.Fatalf("live push got %q/%q", pushedProvider, pushedKey)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("plaintext key on disk: %s", data)
	}

	// /logout with the provider named clears it and pushes the removal.
	out := Execute(context.Background(), b, &State{Provider: "gateway"}, "/logout gateway")
	if out.IsError || !strings.Contains(out.Output, "已清除") {
		t.Fatalf("logout output: %s", out.Output)
	}
	if pushedKey != "" {
		t.Fatalf("logout should push an empty key, got %q", pushedKey)
	}
	again, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again.Providers["gateway"].APIKey != "" {
		t.Fatal("key survived logout")
	}
}

func TestSlashLoginUsageWithoutKey(t *testing.T) {
	isolateAuth(t)
	res := Execute(context.Background(), &Backend{}, &State{}, "/login deepseek")
	if !res.IsError {
		t.Fatalf("expected usage error, got %+v", res)
	}
	if !strings.Contains(res.Output, "deepseek") {
		t.Fatalf("usage should name the provider: %s", res.Output)
	}
}
