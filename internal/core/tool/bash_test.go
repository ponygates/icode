package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// BashTool robustness tests: per-call timeout, Windows shell detection, and
// the tool-side output cap.

// toMSYSPath converts a Windows drive path (C:\Users\x) into the POSIX form
// Git Bash understands (/c/Users/x). Non-drive paths get backslash→slash
// only. WSL bash is out of scope (see resolveBashShell).
func toMSYSPath(p string) string {
	if len(p) >= 2 && p[1] == ':' {
		return "/" + strings.ToLower(p[:1]) + strings.ReplaceAll(p[2:], `\`, "/")
	}
	return strings.ReplaceAll(p, `\`, "/")
}

func TestResolveBashTimeoutSec(t *testing.T) {
	cases := []struct {
		args string
		want int
	}{
		{`{"command":"x"}`, bashDefaultTimeoutSec},                 // unset → default
		{`{"command":"x","timeout":30}`, 30},                       // explicit
		{`{"command":"x","timeout":9999}`, bashMaxTimeoutSec},      // capped at 600
		{`{"command":"x","timeout":0}`, bashDefaultTimeoutSec},     // non-positive → default
		{`{"command":"x","timeout":-5}`, bashDefaultTimeoutSec},    // negative → default
		{`{"command":"x","timeout":"abc"}`, bashDefaultTimeoutSec}, // unparsable → default
	}
	for _, tc := range cases {
		if got := resolveBashTimeoutSec(tc.args); got != tc.want {
			t.Errorf("resolveBashTimeoutSec(%s) = %d, want %d", tc.args, got, tc.want)
		}
	}
}

// TestBashTool_TimeoutArgHonored runs a short sleep with a tight timeout.
// The sleep form follows the shell resolveBashShell actually picked, so the
// test never depends on `sleep` existing on Windows CI.
func TestBashTool_TimeoutArgHonored(t *testing.T) {
	name, _ := resolveBashShell()
	var cmd string
	if name == "cmd" {
		// cmd.exe has no sleep; ping -n 11 ≈ 10s (robust on every Windows box).
		cmd = "ping -n 11 127.0.0.1 >nul"
	} else {
		cmd = "sleep 30"
	}

	start := time.Now()
	res, err := (&BashTool{}).Execute(context.Background(), fmt.Sprintf(
		`{"command": %q, "timeout": 2}`, cmd))
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if res.Success {
		t.Fatalf("long command must time out with timeout=2: %s", res.Content)
	}
	if !strings.Contains(res.Error, "timed out after 2 seconds") {
		t.Fatalf("timeout error must echo the per-call seconds, got: %s", res.Error)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("timeout not enforced, took %v", elapsed)
	}
}

// TestBashTool_TimeoutArgAllowsQuickCommand guards the happy path: a command
// finishing under the custom timeout still succeeds.
func TestBashTool_TimeoutArgAllowsQuickCommand(t *testing.T) {
	res, err := (&BashTool{}).Execute(context.Background(), `{"command": "echo hi", "timeout": 30}`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("quick command with generous timeout must succeed: %s", res.Error)
	}
}

func TestPickWindowsShell(t *testing.T) {
	// $SHELL wins when it points at an existing binary.
	sh, args := pickWindowsShell(
		func(k string) string { return `C:\msys64\usr\bin\bash.exe` },
		func(p string) bool { return p == `C:\msys64\usr\bin\bash.exe` },
	)
	if sh != `C:\msys64\usr\bin\bash.exe` || len(args) != 1 || args[0] != "-c" {
		t.Fatalf("expected $SHELL to win, got %v %v", sh, args)
	}
	// Git Bash is probed when $SHELL is unset or dangling.
	gitBash := filepath.Join(`C:\Program Files\Git`, "bin", "bash.exe")
	sh, args = pickWindowsShell(
		func(k string) string {
			switch k {
			case "SHELL":
				return `C:\dangling\bash.exe`
			case "ProgramFiles":
				return `C:\Program Files`
			}
			return ""
		},
		func(p string) bool { return p == gitBash },
	)
	if sh != gitBash || args[0] != "-c" {
		t.Fatalf("expected Git Bash fallback, got %v %v", sh, args)
	}
	// cmd /C only as the last resort.
	sh, args = pickWindowsShell(func(k string) string { return "" }, func(p string) bool { return false })
	if sh != "cmd" || args[0] != "/C" {
		t.Fatalf("expected cmd fallback, got %v %v", sh, args)
	}
}

func TestResolveBashShell_NonWindowsKeepsSh(t *testing.T) {
	if runtime.GOOS != "windows" {
		name, args := resolveBashShell()
		if name != "sh" || args[0] != "-c" {
			t.Fatalf("non-Windows must keep sh -c, got %v %v", name, args)
		}
	}
	// On Windows the resolver must return one of the three supported shapes.
	if runtime.GOOS == "windows" {
		name, args := resolveBashShell()
		if name == "" || len(args) == 0 {
			t.Fatal("resolveBashShell must always return a shell + arg prefix")
		}
	}
}

func TestCapBashOutput(t *testing.T) {
	small := capBashOutput("short output")
	if small != "short output" {
		t.Fatalf("small output must pass through untouched, got %q", small)
	}
	huge := strings.Repeat("line of text\n", bashOutputCapChars/13+100)
	capped := capBashOutput(huge)
	if len(capped) >= len(huge) {
		t.Fatalf("expected truncation, len stayed %d", len(capped))
	}
	if !strings.Contains(capped, "chars omitted") {
		t.Fatalf("expected head+tail elision marker, got tail %q", capped[len(capped)-100:])
	}
	// Head+tail: the very start and the very end of the original survive.
	if !strings.HasPrefix(capped, "line of text") || !strings.HasSuffix(capped, "line of text\n") {
		t.Fatal("elision must keep both head and tail")
	}
}

// TestBashTool_OutputCapEndToEnd: cat/type a large temp file through the
// real shell and verify the tool-side cap fires before the engine budget.
func TestBashTool_OutputCapEndToEnd(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "huge.txt")
	f, err := os.Create(fp)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 79) + "\n"
	for i := 0; i < bashOutputCapChars/len(line)+200; i++ {
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	name, _ := resolveBashShell()
	cmdStr := fmt.Sprintf(`cat "%s"`, fp)
	if name == "cmd" {
		cmdStr = fmt.Sprintf(`type "%s"`, fp)
	} else {
		// Git Bash's cat rejects Windows backslash paths ("exit status 1"
		// on a file that clearly exists): rewrite to the MSYS POSIX form
		// (/c/Users/...) the bash toolchain understands.
		cmdStr = fmt.Sprintf(`cat "%s"`, toMSYSPath(fp))
	}
	res, err := (&BashTool{}).Execute(context.Background(), fmt.Sprintf(`{"command": %q}`, cmdStr))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("command failed: %s", res.Error)
	}
	if !strings.Contains(res.Content, "chars omitted") {
		t.Fatalf("expected the tool-side cap marker in bash output, len=%d", len(res.Content))
	}
	if len(res.Content) > bashOutputCapChars+1000 {
		t.Fatalf("output exceeded the cap: %d", len(res.Content))
	}
}
