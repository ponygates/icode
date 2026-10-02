package cmd

import (
	"strings"
	"testing"
)

// TestParseMCPAddArgs locks the "--" separator handling for stdio servers.
func TestParseMCPAddArgs(t *testing.T) {
	// fs -- npx -y @mcp/filesystem /data  → command + args
	cmd, args, err := parseMCPAddArgs([]string{"fs", "npx", "-y", "@mcp/filesystem", "/data"}, 1)
	if err != nil || cmd != "npx" || strings.Join(args, " ") != "-y @mcp/filesystem /data" {
		t.Fatalf("parse mismatch: cmd=%q args=%v err=%v", cmd, args, err)
	}

	// No "--" → guided error.
	if _, _, err := parseMCPAddArgs([]string{"fs", "npx"}, -1); err == nil {
		t.Fatalf("missing '--' must error")
	}

	// "--" at the end → no command after it.
	if _, _, err := parseMCPAddArgs([]string{"fs"}, 1); err == nil {
		t.Fatalf("empty command after '--' must error")
	}

	// Command only, no args.
	cmd, args, err = parseMCPAddArgs([]string{"fs", "uvx", "mcp-server-fetch"}, 1)
	if err != nil || cmd != "uvx" || len(args) != 1 || args[0] != "mcp-server-fetch" {
		t.Fatalf("parse mismatch: cmd=%q args=%v err=%v", cmd, args, err)
	}
}

// TestContainsShellMetachars: the stdio command is exec'd without a shell;
// metacharacters are always a configuration mistake.
func TestContainsShellMetachars(t *testing.T) {
	for _, bad := range []string{"npx; rm -rf /", "a && b", "a | b", "a`b", "a$(b)", "a\nb", "../escape"} {
		if !containsShellMetachars(bad) {
			t.Errorf("containsShellMetachars(%q) = false, want true", bad)
		}
	}
	for _, ok := range []string{"npx", "node", `C:\Tools\server.exe`, "uvx"} {
		if containsShellMetachars(ok) {
			t.Errorf("containsShellMetachars(%q) = true, want false", ok)
		}
	}
}
