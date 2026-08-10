package slashui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/types"
)

func TestCmdCD_NoArgsPrintsCwd(t *testing.T) {
	res := cmdCD(nil, nil)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "当前工作目录") {
		t.Fatalf("expected cwd message, got: %s", res.Output)
	}
	if res.CWD != "" {
		t.Fatalf("no-arg /cd should not set CWD, got %q", res.CWD)
	}
}

func TestCmdCD_ChangesDirectory(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)

	dir := t.TempDir()
	res := cmdCD(&State{NoPersistCWD: true}, []string{dir})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	got, _ := os.Getwd()
	if got != dir {
		t.Fatalf("cwd = %q, want %q", got, dir)
	}
	if res.CWD != dir {
		t.Fatalf("res.CWD = %q, want %q", res.CWD, dir)
	}
}

func TestCmdCD_RejectsMissingDir(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)

	res := cmdCD(&State{NoPersistCWD: true}, []string{filepath.Join(orig, "definitely-missing-dir-xyz")})
	if !res.IsError {
		t.Fatalf("expected error for missing dir, got: %s", res.Output)
	}
	if res.CWD != "" {
		t.Fatalf("failed /cd must not set CWD, got %q", res.CWD)
	}
}

func TestCmdRename_TitlesSession(t *testing.T) {
	store := session.NewStore()
	b := &Backend{SessStore: store}
	st := &State{SessionID: "src"}
	sess := &types.Session{ID: "src", Title: "old", ModelID: "m1", ProviderName: "p1"}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	res := cmdRename(b, st, []string{"我的新会话"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	got, err := store.Get("src")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "我的新会话" {
		t.Fatalf("title = %q, want %q", got.Title, "我的新会话")
	}
}
