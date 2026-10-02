package cmd

import "testing"

// TestSplitToolCSV locks the --allowedTools/--disallowedTools list parsing:
// nil for empty (no restriction), trimmed entries, empty entries dropped.
func TestSplitToolCSV(t *testing.T) {
	if got := splitToolCSV(""); got != nil {
		t.Fatalf("empty CSV must be nil, got %v", got)
	}
	if got := splitToolCSV("   "); got != nil {
		t.Fatalf("blank CSV must be nil, got %v", got)
	}
	if got := splitToolCSV(",, ,"); got != nil {
		t.Fatalf("only-empty-entries CSV must be nil, got %v", got)
	}
	got := splitToolCSV("bash, write_file ,mcp__fs__read,")
	if len(got) != 3 || !got["bash"] || !got["write_file"] || !got["mcp__fs__read"] {
		t.Fatalf("parse mismatch: %v", got)
	}
}

// TestMatchToolList covers exact names and the trailing-* prefix glob
// (e.g. "mcp__*" matching every namespaced MCP tool).
func TestMatchToolList(t *testing.T) {
	list := map[string]bool{"bash": true, "mcp__*": true}
	cases := []struct {
		tool string
		want bool
	}{
		{"bash", true},
		{"bash_execute", false}, // exact match only, no implicit prefixing
		{"mcp__fs__read", true},
		{"mcp__github__create_issue", true},
		{"read_file", false},
		{"", false},
	}
	for _, c := range cases {
		if got := matchToolList(list, c.tool); got != c.want {
			t.Errorf("matchToolList(%q) = %v, want %v", c.tool, got, c.want)
		}
	}
	// Empty list never matches.
	if matchToolList(nil, "bash") {
		t.Errorf("nil list must not match")
	}
}
