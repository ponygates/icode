package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExpandImportsInline verifies the @import directive splices a peer file
// into the parent when both live in the same directory.
func TestExpandImportsInline(t *testing.T) {
	dir := t.TempDir()

	peer := filepath.Join(dir, "peer.md")
	if err := os.WriteFile(peer, []byte("PEER CONTENT"), 0o644); err != nil {
		t.Fatalf("write peer: %v", err)
	}

	src := "before\n@peer.md\nafter"
	out := expandImports(src, dir, 0, map[string]bool{})

	if !strings.Contains(out, "PEER CONTENT") {
		t.Fatalf("import not inlined, got %q", out)
	}
	if !strings.Contains(out, "before") || !strings.Contains(out, "after") {
		t.Fatalf("surrounding lines lost, got %q", out)
	}
}

// TestExpandImportsCycle guarantees a cycle is broken with a clear marker
// rather than blowing the stack or looping forever.
func TestExpandImportsCycle(t *testing.T) {
	dir := t.TempDir()

	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	if err := os.WriteFile(a, []byte("A\n@b.md"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(b, []byte("B\n@a.md"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}

	src := "root\n@a.md"
	out := expandImports(src, dir, 0, map[string]bool{})

	if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
		t.Fatalf("expected both files inlined, got %q", out)
	}
	if !strings.Contains(out, "skipped cyclic") {
		t.Fatalf("cycle not detected, got %q", out)
	}
}

// TestExpandImportsMissing keeps a broken directive in place with a diagnostic
// so operators can spot the problem in the rendered prompt.
func TestExpandImportsMissing(t *testing.T) {
	dir := t.TempDir()

	src := "@does-not-exist.md"
	out := expandImports(src, dir, 0, map[string]bool{})

	if !strings.Contains(out, "import failed") {
		t.Fatalf("missing-file diagnostic absent, got %q", out)
	}
}

// TestAppendUserMemoryRoundtrip covers the `#` shortcut path: writing a note
// and reading it back through the user memory loader.
func TestAppendUserMemoryRoundtrip(t *testing.T) {
	tmp := t.TempDir()

	// Redirect HOME so ~/.icode/... resolves inside the test's tmpdir.
	oldHome := os.Getenv("HOME")
	oldUP := os.Getenv("USERPROFILE")
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("USERPROFILE", oldUP)
	}()

	if err := AppendUserMemory("prefer pnpm over npm"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := AppendUserMemory("使用 Go 1.26"); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	got := loadUserMemory()
	if !strings.Contains(got, "prefer pnpm") {
		t.Fatalf("first note missing: %q", got)
	}
	if !strings.Contains(got, "使用 Go 1.26") {
		t.Fatalf("second note missing: %q", got)
	}
}
