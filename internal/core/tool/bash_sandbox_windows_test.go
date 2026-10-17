//go:build windows

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/sandbox"
)

// installSandbox wires a real AppContainer backend for the duration of a test.
// It skips on machines where AppContainer provisioning is unavailable rather
// than pretending the wiring was exercised.
func installSandbox(t *testing.T) {
	t.Helper()
	r, _, err := sandbox.Init(sandbox.Config{
		Mode:           sandbox.ModeOn,
		Network:        sandbox.NetworkDeny,
		GrantWorkspace: true,
	})
	if err != nil || r == nil {
		t.Skipf("AppContainer unavailable: %v", err)
	}
	t.Cleanup(func() {
		sandbox.SetActive(nil)
		r.Close()
	})
}

func bashArgs(t *testing.T, command, dir string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"command": command, "cwd": dir})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return string(b)
}

// TestBashToolExecutesInsideSandbox proves the tool actually routes through the
// backend. A package that compiles but is never called is the failure mode this
// test exists to prevent.
func TestBashToolExecutesInsideSandbox(t *testing.T) {
	installSandbox(t)
	work := t.TempDir()

	bt := &BashTool{}
	res, err := bt.Execute(context.Background(), bashArgs(t, "echo confined-ok", work))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("command failed inside sandbox: %s", res.Error)
	}
	if !strings.Contains(res.Content, "confined-ok") {
		t.Fatalf("unexpected output: %q", res.Content)
	}
}

// TestBashToolCannotEscapeWorkspace is the acceptance criterion from the design
// doc: reading a file outside the workspace must fail even though the command
// carries no path argument the permission gate could inspect.
func TestBashToolCannotEscapeWorkspace(t *testing.T) {
	installSandbox(t)
	home := os.Getenv("USERPROFILE")
	if home == "" {
		t.Skip("USERPROFILE unset")
	}
	probe := filepath.Join(home, fmt.Sprintf("icode-bash-escape-%d.txt", os.Getpid()))
	if err := os.WriteFile(probe, []byte("outside"), 0o600); err != nil {
		t.Skipf("cannot create probe: %v", err)
	}
	defer os.Remove(probe)

	bt := &BashTool{}
	res, err := bt.Execute(context.Background(), bashArgs(t, "type "+probe, t.TempDir()))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Fatalf("reading %s must be denied, got success with %q", probe, res.Content)
	}
	if !strings.Contains(res.Content, "denied") && res.Error == "" && res.Content == "" {
		t.Logf("denial surfaced without output; error=%q", res.Error)
	}
}

// TestBashToolWritesInsideWorkspace keeps the other direction honest: a
// sandbox that blocks everything is useless.
func TestBashToolWritesInsideWorkspace(t *testing.T) {
	installSandbox(t)
	work := t.TempDir()

	bt := &BashTool{}
	res, err := bt.Execute(context.Background(), bashArgs(t, "echo artifact> built.txt && type built.txt", work))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("write failed: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(work, "built.txt")); err != nil {
		t.Fatalf("workspace file missing: %v", err)
	}
}
