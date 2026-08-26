package tool

import (
	"strings"
	"testing"
)

// Tool order must be deterministic and cache-friendly: built-ins first
// (alphabetical), MCP tools after. Random map iteration would silently
// invalidate the provider prompt cache on every request.
func TestListDefsCacheFriendlyOrder(t *testing.T) {
	r := NewRegistry()
	// Order differs between calls (map iteration); collect twice.
	a := r.ListDefs()
	b := r.ListDefs()
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			t.Fatalf("order unstable at %d: %q vs %q", i, a[i].Name, b[i].Name)
		}
	}
	// Built-ins strictly precede MCP tools; each group alphabetical.
	seenMCP := false
	for i, d := range a {
		isMCP := strings.HasPrefix(d.Name, "mcp_")
		if isMCP {
			seenMCP = true
		} else if seenMCP {
			t.Fatalf("built-in %q appears after MCP tools at %d", d.Name, i)
		}
		if i > 0 && !isMCP && !seenMCP && a[i-1].Name > d.Name {
			t.Fatalf("built-ins not alphabetical at %d: %q > %q", i, a[i-1].Name, d.Name)
		}
		if i > 0 && isMCP && strings.HasPrefix(a[i-1].Name, "mcp_") && a[i-1].Name > d.Name {
			t.Fatalf("mcp tools not alphabetical at %d: %q > %q", i, a[i-1].Name, d.Name)
		}
	}
}
