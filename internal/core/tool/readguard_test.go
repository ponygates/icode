package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Read-before-edit guard tests (per-conversation tracking).

func rgCtx(sessionID string) context.Context {
	return WithSessionID(context.Background(), sessionID)
}

func rgWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func rgEditArgs(t *testing.T, path, oldStr, newStr string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"file_path":  path,
		"old_string": oldStr,
		"new_string": newStr,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestReadGuard_RefusesFirstBlindEdit: an edit to a file the conversation
// never read (and whose old_string does not match uniquely+verbatim) must be
// refused with an actionable "read it first" error.
func TestReadGuard_RefusesFirstBlindEdit(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "blind.txt")
	rgWriteFile(t, fp, "alpha beta\ngamma delta\n")

	res, err := (&EditTool{}).Execute(rgCtx("rg-blind"), rgEditArgs(t, fp, "ALPHA-BETA-GUESS", "X"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatalf("blind edit must be refused, got: %s", res.Content)
	}
	if !strings.Contains(res.Error, "read_file") {
		t.Fatalf("refusal must tell the model to read first, got: %s", res.Error)
	}
}

// TestReadGuard_AllowsAfterReadFile: read_file seeds the per-session tracker.
func TestReadGuard_AllowsAfterReadFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "seen.txt")
	rgWriteFile(t, fp, "one\n two \nthree\n") // odd spacing → fuzzy path, no exact match

	ctx := rgCtx("rg-after-read")
	if _, err := (&ReadFileTool{}).Execute(ctx, fmt.Sprintf(`{"path": %q}`, fp)); err != nil {
		t.Fatal(err)
	}
	res, err := (&EditTool{}).Execute(ctx, rgEditArgs(t, fp, "one\ntwo\nthree", "A\nB\nC"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("edit after read_file must pass the guard: %s", res.Error)
	}
}

// TestReadGuard_TrackerIsPerConversation: a file read in one session is NOT
// considered read in another (desktop multi-tab isolation).
func TestReadGuard_TrackerIsPerConversation(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "shared-name.txt")
	rgWriteFile(t, fp, "one\n two \nthree\n")

	readCtx := rgCtx("rg-session-read")
	if _, err := (&ReadFileTool{}).Execute(readCtx, fmt.Sprintf(`{"path": %q}`, fp)); err != nil {
		t.Fatal(err)
	}
	other := rgCtx("rg-session-other")
	res, _ := (&EditTool{}).Execute(other, rgEditArgs(t, fp, "one\ntwo\nthree", "X"))
	if res.Success {
		t.Fatal("session B must not inherit session A's read tracker")
	}
}

// TestReadGuard_AllowsSessionCreatedFile: write_file seeds the tracker, so a
// later edit on the file this session created is allowed.
func TestReadGuard_AllowsSessionCreatedFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "created.txt")

	ctx := rgCtx("rg-created")
	res, err := (&WriteFileTool{}).Execute(ctx, fmt.Sprintf(
		`{"path": %q, "content": "hello\nworld\n"}`, fp))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("creating a new file must pass: %s", res.Error)
	}
	eres, _ := (&EditTool{}).Execute(ctx, rgEditArgs(t, fp, "hello", "HELLO"))
	if !eres.Success {
		t.Fatalf("edit of a session-created file must pass: %s", eres.Error)
	}
}

// TestReadGuard_UniqueExactEscapeHatch: an untracked file can still be edited
// headlessly when the old_string matches uniquely and verbatim — with a
// warning appended instead of a hard refusal.
func TestReadGuard_UniqueExactEscapeHatch(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "escape.txt")
	rgWriteFile(t, fp, "needle in the haystack\npadding\n")

	res, err := (&EditTool{}).Execute(rgCtx("rg-escape"), rgEditArgs(t, fp, "needle in the haystack", "found it"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("unique exact match must pass via the escape hatch: %s", res.Error)
	}
	if !strings.Contains(res.Content, "read-before-edit") {
		t.Fatalf("escape hatch must warn in the result, got: %s", res.Content)
	}
	data, _ := os.ReadFile(fp)
	if !strings.Contains(string(data), "found it") {
		t.Fatalf("edit not applied: %s", data)
	}
}

// TestReadGuard_WriteFileOverUntrackedRefused: whole-file writes have no
// self-verifying needle, so overwriting an unread existing file is refused.
func TestReadGuard_WriteFileOverUntrackedRefused(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "over.txt")
	rgWriteFile(t, fp, "precious user data\n")

	res, _ := (&WriteFileTool{}).Execute(rgCtx("rg-overwrite"), fmt.Sprintf(
		`{"path": %q, "content": "clobbered"}`, fp))
	if res.Success {
		t.Fatal("blind overwrite of an existing untracked file must be refused")
	}
	data, _ := os.ReadFile(fp)
	if string(data) != "precious user data\n" {
		t.Fatalf("refused write must not touch the file: %s", data)
	}
}
