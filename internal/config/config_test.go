package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSaveCreatesFileWithPersistedContent verifies Save round-trips to disk.
func TestSaveCreatesFileWithPersistedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	c := Default()
	c.Language = "en"
	c.Defaults.Mode = "plan"
	c.Defaults.WorkingDir = filepath.Join(dir, "work")
	c.Providers["deepseek"] = ProviderCfg{APIKey: "sk-test", APIBase: "https://example.com", Timeout: 99}

	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	reloaded := Default()
	if err := yaml.Unmarshal(data, reloaded); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if reloaded.Language != "en" || reloaded.Defaults.Mode != "plan" {
		t.Fatalf("reloaded mismatch: lang=%q mode=%q", reloaded.Language, reloaded.Defaults.Mode)
	}
	if reloaded.Defaults.WorkingDir != filepath.Join(dir, "work") {
		t.Fatalf("reloaded working dir mismatch: %q", reloaded.Defaults.WorkingDir)
	}
	if p, ok := reloaded.Provider("deepseek"); !ok {
		t.Fatalf("deepseek provider missing")
	} else if p.APIKey != "" {
		t.Fatalf("plaintext api_key persisted: %q", p.APIKey)
	} else if p.APIKeyEnc == "" {
		t.Fatalf("expected api_key_enc in persisted config")
	} else if p.Timeout != 99 {
		t.Fatalf("reloaded deepseek timeout mismatch: %d", p.Timeout)
	}
	// The plaintext is recovered by the decrypt pass used during Load.
	decryptConfigKeys(reloaded)
	if p, ok := reloaded.Provider("deepseek"); !ok || p.APIKey != "sk-test" {
		t.Fatalf("reloaded deepseek key after decrypt: %+v ok=%v", p, ok)
	}
}

// TestSaveEncryptsAPIKeys verifies the plaintext key never reaches the config
// file and that a Save → reload → decrypt round-trip restores it.
func TestSaveEncryptsAPIKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()
	c.Providers["deepseek"] = ProviderCfg{APIKey: "sk-super-secret", APIBase: "https://example.com"}

	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "sk-super-secret") {
		t.Fatalf("plaintext API key persisted to disk:\n%s", data)
	}
	if !strings.Contains(string(data), "api_key_enc") {
		t.Fatalf("expected encrypted api_key_enc in saved config:\n%s", data)
	}

	reloaded := Default()
	if err := yaml.Unmarshal(data, reloaded); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	decryptConfigKeys(reloaded)
	if p, ok := reloaded.Provider("deepseek"); !ok || p.APIKey != "sk-super-secret" {
		t.Fatalf("round-trip failed: %+v ok=%v", p, ok)
	}
}

// TestDecryptIgnoresMissingCiphertext guards against decryptConfigKeys wiping
// keys that were never encrypted (e.g. env-var overrides or plaintext legacy
// configs still on disk).
func TestDecryptIgnoresMissingCiphertext(t *testing.T) {
	c := Default()
	c.Providers["zhipu"] = ProviderCfg{APIKey: "env-key"}
	decryptConfigKeys(c)
	if p, ok := c.Provider("zhipu"); !ok || p.APIKey != "env-key" {
		t.Fatalf("env key clobbered: %+v ok=%v", p, ok)
	}
}

// TestSaveCreatesDir verifies Save creates missing parent directories.
func TestSaveCreatesDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "config.yaml")
	if err := Default().Save(path); err != nil {
		t.Fatalf("Save with nested dir: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}
}

// TestSaveFileModeOwnerOnly checks the saved file is not world/group readable
// (it contains plaintext API keys). POSIX only — Windows ignores the mode bits.
func TestSaveFileModeOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are ignored on Windows")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Default().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected 0600, got %o", perm)
	}
}

// TestWithLockPersistAtomically verifies mutations inside WithLock + SaveLocked
// are persisted in the same critical section.
func TestWithLockPersistAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()

	err := c.WithLock(func() error {
		// Mutate the map directly — calling c.SetProvider (which takes the
		// write lock again) inside WithLock would deadlock.
		c.Providers["newp"] = ProviderCfg{APIBase: "https://new.example", Timeout: 7}
		return c.SaveLocked(path)
	})
	if err != nil {
		t.Fatalf("WithLock: %v", err)
	}

	data, _ := os.ReadFile(path)
	reloaded := Default()
	if err := yaml.Unmarshal(data, reloaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p, ok := reloaded.Provider("newp"); !ok || p.APIBase != "https://new.example" {
		t.Fatalf("expected newp persisted, got %+v ok=%v", p, ok)
	}
}

// TestProviderLifecycle covers Provider/SetProvider/DeleteProvider/ProvidersCopy.
func TestProviderLifecycle(t *testing.T) {
	c := Default()
	if _, ok := c.Provider("nonexistent"); ok {
		t.Fatal("unexpected provider present")
	}

	c.SetProvider("acme", ProviderCfg{APIBase: "https://acme.example", Timeout: 5})
	pc, ok := c.Provider("acme")
	if !ok || pc.APIBase != "https://acme.example" {
		t.Fatalf("expected acme provider, got %+v ok=%v", pc, ok)
	}
	if got := c.APIKey("acme"); got != "" {
		t.Fatalf("expected empty key, got %q", got)
	}

	copy := c.ProvidersCopy()
	if got := copy["acme"]; got.APIBase != "https://acme.example" {
		t.Fatalf("copy mismatch: %+v", got)
	}

	c.DeleteProvider("acme")
	if _, ok := c.Provider("acme"); ok {
		t.Fatal("provider should be deleted")
	}
	if _, ok := c.ProvidersCopy()["acme"]; ok {
		t.Fatal("deleted provider still in copy")
	}
}

// TestMCPLifecycle covers UpsertMCP (replace + append) and RemoveMCP.
func TestMCPLifecycle(t *testing.T) {
	c := Default()

	c.UpsertMCP(MCPServerCfg{Name: "srv-a", Type: "stdio", Command: "npx", Enabled: true})
	c.UpsertMCP(MCPServerCfg{Name: "srv-b", Type: "sse", URL: "http://x"})
	if len(c.MCP) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(c.MCP))
	}

	// Upsert replaces an existing entry by name instead of appending.
	c.UpsertMCP(MCPServerCfg{Name: "srv-a", Type: "stdio", Command: "uvx", Enabled: false})
	if len(c.MCP) != 2 {
		t.Fatalf("expected replace, got %d entries", len(c.MCP))
	}
	for _, m := range c.MCP {
		if m.Name == "srv-a" && m.Command != "uvx" {
			t.Fatalf("expected srv-a replaced, got %+v", m)
		}
	}

	c.RemoveMCP("srv-b")
	if len(c.MCP) != 1 || c.MCP[0].Name != "srv-a" {
		t.Fatalf("expected only srv-a left, got %+v", c.MCP)
	}
	c.RemoveMCP("does-not-exist")
	if len(c.MCP) != 1 {
		t.Fatalf("remove of missing entry should be a no-op")
	}
}

// TestModelLifecycle covers UpsertModel/DeleteModel/FindModel/ModelDisplayName.
func TestModelLifecycle(t *testing.T) {
	c := Default()

	c.UpsertModel(ModelCfg{Provider: "p", ModelID: "m1", Name: "M One"})
	if _, ok := c.FindModel("p/m1"); !ok {
		t.Fatal("expected p/m1 present")
	}
	if got := c.ModelDisplayName("p", "m1"); got != "M One" {
		t.Fatalf("expected display name, got %q", got)
	}

	// Upsert replaces the same stable ID.
	c.UpsertModel(ModelCfg{Provider: "p", ModelID: "m1", Name: "M One v2"})
	if len(c.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(c.Models))
	}
	if got := c.ModelDisplayName("p", "m1"); got != "M One v2" {
		t.Fatalf("expected replaced name, got %q", got)
	}

	// Invalid upserts are ignored.
	c.UpsertModel(ModelCfg{Provider: "", ModelID: ""})
	if len(c.Models) != 1 {
		t.Fatalf("invalid model should be ignored")
	}

	if !c.DeleteModel("p/m1") {
		t.Fatal("expected delete to succeed")
	}
	if c.DeleteModel("p/m1") {
		t.Fatal("second delete should return false")
	}
	if _, ok := c.FindModel("p/m1"); ok {
		t.Fatal("model should be gone")
	}
}

// TestJSONDoesNotLeakAPIKeys ensures provider and multimodal API keys are
// excluded from JSON marshaling (they are — used by the desktop HTTP API).
func TestJSONDoesNotLeakAPIKeys(t *testing.T) {
	c := Default()
	c.Providers["openrouter"] = ProviderCfg{APIKey: "sk-json-secret", APIBase: "https://x"}
	c.Multimodal.APIKey = "sk-multimodal-secret"

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	raw := string(data)
	for _, secret := range []string{"sk-json-secret", "sk-multimodal-secret"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("JSON output leaked secret %q", secret)
		}
	}
}

// TestYAMLKeepsAPIKeysRoundTrip ensures encrypted keys survive a save → load
// cycle (decryption restores the plaintext needed for auth).
func TestYAMLKeepsAPIKeysRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()
	c.Providers["openrouter"] = ProviderCfg{APIKey: "sk-yaml-secret"}

	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded := Default()
	data, _ := os.ReadFile(path)
	if err := yaml.Unmarshal(data, reloaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	decryptConfigKeys(reloaded)
	if got := reloaded.APIKey("openrouter"); got != "sk-yaml-secret" {
		t.Fatalf("expected key round-trip, got %q", got)
	}
}

// TestConcurrentAccess hammers the config from many goroutines mixing reads
// and writes. Run under `go test -race` to detect data races; without the
// race detector this still catches panics / lost updates.
func TestConcurrentAccess(t *testing.T) {
	c := Default()

	var wg sync.WaitGroup
	const workers = 32
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("p%d", i)
			c.SetProvider(name, ProviderCfg{APIKey: fmt.Sprintf("k%d", i), Timeout: i})
			_, _ = c.Provider(name)
			_ = c.ProvidersCopy()
			c.WithRLock(func() {
				// Read a field directly — calling a locking method inside a
				// read lock (e.g. c.APIKey) deadlocks under Go's writer-priority
				// RWMutex when a writer is queued.
				_ = c.Language
			})
			c.UpsertModel(ModelCfg{Provider: name, ModelID: "m", Name: name})
			_, _ = c.FindModel(name + "/m")
			_ = c.ModelsCopy()
		}(i)
	}
	wg.Wait()

	if got := len(c.ProvidersCopy()); got < workers {
		t.Fatalf("expected >= %d providers, got %d", workers, got)
	}
}

// TestConcurrentSave ensures concurrent Save calls (e.g. two desktop settings
// toggles racing) do not corrupt the file or panic.
func TestConcurrentSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Save(path)
		}()
	}
	wg.Wait()

	reloaded := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := yaml.Unmarshal(data, reloaded); err != nil {
		t.Fatalf("config corrupted by concurrent saves: %v", err)
	}
}

// TestEnvOverrides checks applyEnvOverrides injects keys and language.
func TestEnvOverrides(t *testing.T) {
	t.Setenv("ICODE_LANG", "zh-TW")
	t.Setenv("DEEPSEEK_API_KEY", "sk-env")
	t.Setenv("ZHIPU_API_KEY", "")

	c := Default()
	applyEnvOverrides(c)

	if c.Language != "zh-TW" {
		t.Fatalf("expected zh-TW from env, got %q", c.Language)
	}
	if got := c.APIKey("deepseek"); got != "sk-env" {
		t.Fatalf("expected deepseek env key, got %q", got)
	}
	// Empty env value must not clobber an existing key.
	c.Providers["zhipu"] = ProviderCfg{APIKey: "existing"}
	applyEnvOverrides(c)
	if got := c.APIKey("zhipu"); got != "existing" {
		t.Fatalf("empty env clobbered existing key: %q", got)
	}
}

// TestParseSecurityLevel verifies level parsing with safe fallback.
func TestParseSecurityLevel(t *testing.T) {
	if ParseSecurityLevel("unrestricted") != SecUnrestricted {
		t.Fatal("unrestricted parse failed")
	}
	if ParseSecurityLevel("garbage") != SecLocal {
		t.Fatal("unknown level should fall back to local")
	}
	if ParseSecurityLevel("") != SecLocal {
		t.Fatal("empty level should fall back to local")
	}
}

// TestEffectiveSystemPrompt covers directive + extra-dirs composition.
func TestEffectiveSystemPrompt(t *testing.T) {
	c := Default()
	c.Defaults.SystemPrompt = "Base prompt."
	c.Defaults.OutputStyle = "concise"
	c.Defaults.ExtraDirs = []string{"/tmp/a", "/tmp/b"}

	out := EffectiveSystemPrompt(c)
	for _, want := range []string{"Base prompt.", "extremely concise", "/tmp/a", "/tmp/b"} {
		if !strings.Contains(out, want) {
			t.Fatalf("EffectiveSystemPrompt missing %q:\n%s", want, out)
		}
	}
	// Default locale (zh-CN) must inject its language directive.
	if !strings.Contains(out, "简体中文") {
		t.Fatalf("default config missing zh-CN language directive:\n%s", out)
	}
	if EffectiveSystemPrompt(nil) != "" {
		t.Fatal("nil config should produce empty prompt")
	}
}

func TestLanguageDirective(t *testing.T) {
	cases := []struct {
		lang string
		want string
	}{
		{"zh-CN", "简体中文"},
		{"zh-TW", "繁體中文"},
		{"en", "in English"},
		{"", ""},
		{"xx-unknown", ""},
	}
	for _, tc := range cases {
		got := LanguageDirective(tc.lang)
		if tc.want == "" {
			if got != "" {
				t.Errorf("LanguageDirective(%q) = %q, want empty", tc.lang, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("LanguageDirective(%q) missing %q: %q", tc.lang, tc.want, got)
		}
	}
	// Comments are explicitly covered in every non-empty directive.
	for _, lang := range []string{"zh-CN", "zh-TW", "en"} {
		d := LanguageDirective(lang)
		if !strings.Contains(d, "注释") && !strings.Contains(d, "註解") && !strings.Contains(d, "comments") {
			t.Errorf("directive for %q does not mention code comments", lang)
		}
	}
}

func TestEffectiveLanguageProjectOverride(t *testing.T) {
	// No project file → falls back to config language.
	c := Default()
	c.Language = "zh-CN"
	if got := EffectiveLanguage(c); got != "zh-CN" {
		t.Errorf("fallback = %q, want zh-CN", got)
	}

	// Project .icode/language wins.
	if err := os.MkdirAll(".icode", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".icode", "language"), []byte("en\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Join(".icode", "language"))
	if got := EffectiveLanguage(c); got != "en" {
		t.Errorf("override = %q, want en", got)
	}
	// The effective system prompt honours the override too.
	out := EffectiveSystemPrompt(c)
	if !strings.Contains(out, "in English") {
		t.Error("system prompt did not honour project language override")
	}

	// Invalid override content is ignored (falls back to config).
	if err := os.WriteFile(filepath.Join(".icode", "language"), []byte("klingon"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := EffectiveLanguage(c); got != "zh-CN" {
		t.Errorf("invalid override = %q, want fallback zh-CN", got)
	}
}

// TestSaveEncryptsMCPHeaders verifies MCP request headers (e.g. Authorization
// bearer tokens) never reach the config file in plaintext, and that a
// Save → reload → decrypt round-trip restores them.
func TestSaveEncryptsMCPHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()
	c.MCP = []MCPServerCfg{
		{Name: "srv-a", Type: "sse", URL: "http://localhost:8080", Headers: map[string]string{"Authorization": "Bearer super-secret-token"}},
		{Name: "srv-b", Type: "stdio", Command: "npx"},
	}

	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "super-secret-token") {
		t.Fatalf("plaintext MCP header persisted to disk:\n%s", data)
	}
	if !strings.Contains(string(data), "headers_enc") {
		t.Fatalf("expected encrypted headers_enc in saved config:\n%s", data)
	}

	reloaded := Default()
	if err := yaml.Unmarshal(data, reloaded); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	decryptConfigKeys(reloaded)
	if len(reloaded.MCP) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(reloaded.MCP))
	}
	got := reloaded.MCP[0].Headers["Authorization"]
	if got != "Bearer super-secret-token" {
		t.Fatalf("round-trip header mismatch: %q", got)
	}
}
