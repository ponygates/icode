package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// newSnapshot creates a FileSnapshot backed by a temp project root, so tests
// never touch the real workspace.
func newSnapshot(t *testing.T) *FileSnapshot {
	t.Helper()
	root := t.TempDir()
	fs, err := NewFileSnapshot(root)
	if err != nil {
		t.Fatalf("NewFileSnapshot: %v", err)
	}
	return fs
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestSnapshotFile_NewFileReturnsEmpty(t *testing.T) {
	fs := newSnapshot(t)
	// A file that does not exist yet → empty hash, no error.
	hash, err := fs.SnapshotFile(context.Background(), filepath.Join(fs.projectRoot, "missing.go"))
	if err != nil {
		t.Fatalf("SnapshotFile missing: %v", err)
	}
	if hash != "" {
		t.Errorf("SnapshotFile on missing file should return empty hash, got %q", hash)
	}
}

func TestSnapshotFile_ExistingFileReturnsHash(t *testing.T) {
	fs := newSnapshot(t)
	path := filepath.Join(fs.projectRoot, "main.go")
	writeFile(t, path, "package main\n")

	hash, err := fs.SnapshotFile(context.Background(), path)
	if err != nil {
		t.Fatalf("SnapshotFile: %v", err)
	}
	if hash == "" {
		t.Error("SnapshotFile on existing file should return a non-empty commit hash")
	}
}

func TestSnapshotFile_OutsideProjectRejected(t *testing.T) {
	fs := newSnapshot(t)
	other := t.TempDir()
	path := filepath.Join(other, "outside.go")
	writeFile(t, path, "x")

	_, err := fs.SnapshotFile(context.Background(), path)
	if err == nil {
		t.Error("SnapshotFile on a path outside project root should error")
	}
}

func TestUndo_NoSnapshotsReturnsError(t *testing.T) {
	fs := newSnapshot(t)
	// Undo on a fresh repo with no commits should return the documented error.
	_, err := fs.Undo(context.Background(), 1)
	if err == nil {
		t.Error("Undo with no snapshots should return an error")
	}
}

func TestSnapshotAndUndo_RoundTrip(t *testing.T) {
	fs := newSnapshot(t)
	path := filepath.Join(fs.projectRoot, "a.txt")

	// commit 1: an initial snapshot so the shadow repo has history to diff
	// against (Undo diffs HEAD~N..HEAD; a single commit has no HEAD~1).
	writeFile(t, path, "v0")
	if _, err := fs.SnapshotFile(context.Background(), path); err != nil {
		t.Fatalf("snapshot v0: %v", err)
	}

	// commit 2: snapshot the ORIGINAL state ("one") before modifying — this
	// is the documented SnapshotFile contract: call it BEFORE a tool edits the
	// file, so the shadow repo captures the pre-edit version.
	writeFile(t, path, "one")
	if _, err := fs.SnapshotFile(context.Background(), path); err != nil {
		t.Fatalf("snapshot pre-edit: %v", err)
	}

	// Simulate a tool modifying the file in the real workspace.
	writeFile(t, path, "two")
	if got, _ := os.ReadFile(path); string(got) != "two" {
		t.Fatalf("setup: file should be 'two' after edit, got %q", string(got))
	}

	// Undo one step should restore the pre-edit snapshot ("one") from the
	// shadow worktree back into the real workspace.
	restored, err := fs.Undo(context.Background(), 1)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(restored) == 0 {
		t.Fatal("Undo should list restored files")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after undo: %v", err)
	}
	if string(got) != "one" {
		t.Errorf("after undo content = %q, want %q (pre-edit state)", string(got), "one")
	}
}

// TestUndoMultiStep verifies the fix for "multi-step /undo restores the
// latest snapshot instead of the target one": with three snapshots, /undo 2
// must restore the content captured by HEAD~1, not the most recent snapshot.
func TestUndoMultiStep(t *testing.T) {
	fs := newSnapshot(t)
	path := filepath.Join(fs.projectRoot, "a.txt")

	writeFile(t, path, "v0")
	if _, err := fs.SnapshotFile(context.Background(), path); err != nil {
		t.Fatalf("snapshot v0: %v", err)
	}
	writeFile(t, path, "one")
	if _, err := fs.SnapshotFile(context.Background(), path); err != nil {
		t.Fatalf("snapshot one: %v", err)
	}
	writeFile(t, path, "two")
	if _, err := fs.SnapshotFile(context.Background(), path); err != nil {
		t.Fatalf("snapshot two: %v", err)
	}
	// Simulate a tool leaving the file at a post-edit state.
	writeFile(t, path, "three")

	restored, err := fs.Undo(context.Background(), 2)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(restored) == 0 {
		t.Fatal("Undo should list restored files")
	}
	got, _ := os.ReadFile(path)
	// /undo 2 skips the latest snapshot (two) and restores "one" (HEAD~1).
	if string(got) != "one" {
		t.Errorf("after undo 2 content = %q, want %q (captured by HEAD~1)", string(got), "one")
	}
}

func TestListSnapshots_AfterSnapshot(t *testing.T) {
	fs := newSnapshot(t)
	path := filepath.Join(fs.projectRoot, "b.txt")
	writeFile(t, path, "hello")
	if _, err := fs.SnapshotFile(context.Background(), path); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	entries, err := fs.ListSnapshots(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(entries) == 0 {
		t.Error("ListSnapshots after a snapshot should return at least 1 entry")
	}
}
