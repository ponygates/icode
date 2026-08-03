package xgo

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriter_WritesAndRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	// 100-byte cap so a couple of writes trigger rotation.
	rw, err := NewRotatingWriter(path, 100)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	t.Cleanup(func() { rw.Close() })

	// Write enough to cross the cap twice.
	if _, err := rw.Write([]byte(strings.Repeat("a", 60) + "\n")); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if _, err := rw.Write([]byte(strings.Repeat("b", 60) + "\n")); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	// The backup file must exist after rotation.
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("expected backup file %s.1 after rotation, got: %v", path, err)
	}

	// Current file should be small (post-rotation fresh write).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat current: %v", err)
	}
	if info.Size() > 100 {
		t.Errorf("current file size = %d, should be <= cap after rotation", info.Size())
	}
}

func TestRotatingWriter_DisabledRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nolimit.log")
	rw, err := NewRotatingWriter(path, 0) // 0 = no rotation
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	t.Cleanup(func() { rw.Close() })

	// Write a lot; no backup should appear.
	if _, err := rw.Write([]byte(strings.Repeat("x", 5000))); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error("backup file should NOT exist when rotation is disabled")
	}
}

func TestRotatingWriter_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "append.log")
	// Pre-create a file with content.
	if err := os.WriteFile(path, []byte("preexisting\n"), 0644); err != nil {
		t.Fatalf("precreate: %v", err)
	}
	rw, err := NewRotatingWriter(path, 0)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	t.Cleanup(func() { rw.Close() })

	if _, err := rw.Write([]byte("appended\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "preexisting") || !strings.Contains(string(got), "appended") {
		t.Errorf("expected both preexisting and appended content, got %q", string(got))
	}
}

// Ensure RotatingWriter satisfies io.Writer at compile time.
var _ io.Writer = (*RotatingWriter)(nil)
