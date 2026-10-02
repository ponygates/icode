//go:build windows

package executil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFixCmdQuoteBuildsRawCmdLine(t *testing.T) {
	cmd := Command("cmd", "/C", `type "C:\a b\huge.txt"`)
	want := `"cmd" /S /C "type "C:\a b\huge.txt""`
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CmdLine != want {
		t.Fatalf("CmdLine = %q, want %q", cmd.SysProcAttr.CmdLine, want)
	}
	// The console-hiding attributes set by hide() must survive.
	if !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags != 0x08000000 {
		t.Fatalf("hide() attributes lost: %+v", cmd.SysProcAttr)
	}
}

func TestFixCmdQuoteNoopForNonCmd(t *testing.T) {
	cases := [][]string{
		{"powershell", "-NoProfile", "-Command", "echo hi"},
		{"cmd", "/K", "echo hi"},
		{"cmd", "/C"},
	}
	for _, args := range cases {
		cmd := Command(args[0], args[1:]...)
		if cmd.SysProcAttr.CmdLine != "" {
			t.Fatalf("args %v: CmdLine unexpectedly set: %q", args, cmd.SysProcAttr.CmdLine)
		}
	}
}

// TestCmdQuotedPathEndToEnd reproduces the original bug end-to-end: cmd.exe
// chokes on Go's EscapeArg re-quoting when the command embeds quotes (e.g. a
// path with spaces), reporting "The filename, directory name, or volume
// label syntax is incorrect." with exit 1. With fixCmdQuote the verbatim
// line reaches cmd and `type` succeeds.
func TestCmdQuotedPathEndToEnd(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "a b.txt")
	if err := os.WriteFile(fp, []byte("quoted-path-ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := CommandContext(context.Background(), "cmd", "/C", `type "`+fp+`"`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\noutput: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "quoted-path-ok" {
		t.Fatalf("output = %q, want %q", got, "quoted-path-ok")
	}
	// Belt-and-braces: prove the test harness can still observe failures.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := CommandContext(ctx, "cmd", "/C", `type "`+filepath.Join(dir, "missing x.txt")+`"`).CombinedOutput(); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
