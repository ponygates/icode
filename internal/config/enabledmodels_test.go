package config

import "testing"

func TestIsModelEnabled_NoFilterEnablesEverything(t *testing.T) {
	// No Providers map at all — the common case for a fresh config.
	c := &Config{}
	if !c.IsModelEnabled("deepseek", "deepseek-v4-flash") {
		t.Fatal("an unknown provider must not filter anything")
	}

	// Provider present but no filter set.
	c.Providers = map[string]ProviderCfg{"deepseek": {}}
	if !c.IsModelEnabled("deepseek", "anything") {
		t.Fatal("an empty filter means no restriction")
	}
}

func TestIsModelEnabled_Filter(t *testing.T) {
	c := &Config{Providers: map[string]ProviderCfg{
		"deepseek": {EnabledModels: []string{"deepseek-v4-flash"}},
	}}

	if !c.IsModelEnabled("deepseek", "deepseek-v4-flash") {
		t.Fatal("a listed model must pass")
	}
	if c.IsModelEnabled("deepseek", "deepseek-v4-pro") {
		t.Fatal("an unlisted model must be filtered out")
	}
	// Filtering is per provider.
	if !c.IsModelEnabled("kimi", "moonshot-v1") {
		t.Fatal("another provider must be unaffected")
	}
}

func TestIsModelEnabled_MatchesPrefixedCatalogueIDs(t *testing.T) {
	// Ollama's catalogue stores ids as "ollama/llama3", but the fetched list and
	// the filter both use the vendor-native "llama3.1:latest". They must match.
	c := &Config{Providers: map[string]ProviderCfg{
		"ollama": {EnabledModels: []string{"llama3.1:latest"}},
	}}

	if !c.IsModelEnabled("ollama", "ollama/llama3.1:latest") {
		t.Fatal("a prefixed catalogue id must match its vendor-native filter entry")
	}
	if c.IsModelEnabled("ollama", "ollama/qwen2.5") {
		t.Fatal("an unlisted prefixed id must be filtered out")
	}
}

func TestVendorModelID(t *testing.T) {
	cases := []struct{ provider, in, want string }{
		{"ollama", "ollama/llama3", "llama3"},
		{"deepseek", "deepseek-v4-flash", "deepseek-v4-flash"},
		{"deepseek", "deepseek/deepseek-v4-flash", "deepseek-v4-flash"},
		{"", "whatever", "whatever"},
		// A slash inside the model id itself must survive the prefix strip.
		{"openrouter", "openrouter/anthropic/claude-3.5", "anthropic/claude-3.5"},
	}
	for _, tc := range cases {
		if got := VendorModelID(tc.provider, tc.in); got != tc.want {
			t.Errorf("VendorModelID(%q, %q) = %q, want %q", tc.provider, tc.in, got, tc.want)
		}
	}
}

func TestSetEnabledModels(t *testing.T) {
	c := &Config{}
	c.SetEnabledModels("deepseek", []string{"a", "b"})
	if got := c.Providers["deepseek"].EnabledModels; len(got) != 2 {
		t.Fatalf("want 2 entries, got %v", got)
	}
	if c.IsModelEnabled("deepseek", "zzz") {
		t.Fatal("an unlisted model must be filtered once a selection exists")
	}

	// Clearing the list lifts the restriction.
	c.SetEnabledModels("deepseek", nil)
	if !c.IsModelEnabled("deepseek", "zzz") {
		t.Fatal("clearing the selection must restore the full catalogue")
	}
}
