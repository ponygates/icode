package slashcmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadAndExpandBasic covers the happy path: a frontmatter'd file loads
// with its metadata, and $ARGUMENTS expands into the body.
func TestLoadAndExpandBasic(t *testing.T) {
	dir := t.TempDir()
	body := `---
description: Generate changelog
argument-hint: [tag]
---
Please summarise commits since $ARGUMENTS. First one is $1.`
	if err := os.WriteFile(filepath.Join(dir, "changelog.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	reg := Load(dir)
	c, ok := reg.Get("/changelog")
	if !ok {
		t.Fatal("command not registered")
	}
	if c.Description != "Generate changelog" {
		t.Fatalf("description = %q", c.Description)
	}
	if c.ArgumentHint != "[tag]" {
		t.Fatalf("argument-hint = %q", c.ArgumentHint)
	}

	out, err := c.Expand(context.Background(), "v1.0 rc")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if !strings.Contains(out, "since v1.0 rc") {
		t.Fatalf("$ARGUMENTS not substituted: %q", out)
	}
	if !strings.Contains(out, "First one is v1.0.") {
		t.Fatalf("$1 not substituted: %q", out)
	}
}

// TestLoadNoFrontmatter checks that a plain markdown file without frontmatter
// is still registered, using a default description.
func TestLoadNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "explain.md"),
		[]byte("Explain $ARGUMENTS in one paragraph."), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	reg := Load(dir)
	c, ok := reg.Get("/explain")
	if !ok {
		t.Fatal("command not registered")
	}
	if c.Description == "" {
		t.Fatal("default description empty")
	}
}

// TestExpandShellPrefix ensures `!command` lines are executed and their
// output is inlined. Uses `echo` which is available in cmd.exe and sh.
func TestExpandShellPrefix(t *testing.T) {
	dir := t.TempDir()
	body := "Before\n!echo hello-shell\nAfter"
	if err := os.WriteFile(filepath.Join(dir, "greet.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	reg := Load(dir)
	c, _ := reg.Get("/greet")
	out, err := c.Expand(context.Background(), "")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if !strings.Contains(out, "hello-shell") {
		t.Fatalf("shell output missing: %q", out)
	}
	if !strings.Contains(out, "```") {
		t.Fatalf("shell output not fenced: %q", out)
	}
}

// TestProjectOverridesUser verifies that project-scoped commands override
// user-scoped commands of the same name.
func TestProjectOverridesUser(t *testing.T) {
	user := t.TempDir()
	project := t.TempDir()

	os.WriteFile(filepath.Join(user, "dup.md"), []byte("USER VERSION"), 0o644)
	os.WriteFile(filepath.Join(project, "dup.md"), []byte("PROJECT VERSION"), 0o644)

	reg := Load(user, project)
	c, ok := reg.Get("/dup")
	if !ok {
		t.Fatal("not registered")
	}
	out, _ := c.Expand(context.Background(), "")
	if !strings.Contains(out, "PROJECT VERSION") {
		t.Fatalf("expected project override, got %q", out)
	}
}
