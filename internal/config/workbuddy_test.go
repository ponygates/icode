package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWorkBuddyMCP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	content := `{
  "mcpServers": {
    "playwright": {"command": "npx", "args": ["@playwright/mcp@latest"], "env": {"B": "2", "A": "1"}},
    "remote": {"url": "https://example.com/sse", "headers": {"Authorization": "Bearer x"}},
    "off": {"command": "foo", "disabled": true},
    "dup": {"command": "bar"}
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	existing := []MCPServerCfg{{Name: "dup", Type: "stdio", Command: "mine"}}
	got, err := LoadWorkBuddyMCP(path, existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 imported servers, got %d: %+v", len(got), got)
	}
	// Sorted by name: playwright, remote.
	pw := got[0]
	if pw.Name != "playwright" || pw.Type != "stdio" || pw.Command != "npx" || !pw.Enabled {
		t.Fatalf("bad playwright entry: %+v", pw)
	}
	if len(pw.Env) != 2 || pw.Env[0] != "A=1" || pw.Env[1] != "B=2" {
		t.Fatalf("env not flattened/sorted: %v", pw.Env)
	}
	rm := got[1]
	if rm.Name != "remote" || rm.Type != "sse" || rm.URL != "https://example.com/sse" {
		t.Fatalf("bad remote entry: %+v", rm)
	}
	if rm.Headers["Authorization"] != "Bearer x" {
		t.Fatalf("headers lost: %+v", rm.Headers)
	}
}

func TestLoadWorkBuddyMCPMissingFile(t *testing.T) {
	got, err := LoadWorkBuddyMCP(filepath.Join(t.TempDir(), "nope.json"), nil)
	if err != nil || got != nil {
		t.Fatalf("missing file should be nil/nil, got %v / %v", got, err)
	}
}
