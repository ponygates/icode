package tui

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/sandbox"
)

func TestShellArgvMatchesPlatform(t *testing.T) {
	argv := shellArgv("echo hi")
	want := "sh"
	if runtime.GOOS == "windows" {
		want = "cmd"
	}
	if argv[0] != want {
		t.Fatalf("argv[0] = %q, want %q", argv[0], want)
	}
	if argv[len(argv)-1] != "echo hi" {
		t.Fatalf("command string missing from argv: %v", argv)
	}
}

// recordRunner stands in for a platform backend. It lets the routing decision
// be tested without provisioning a real AppContainer: what matters here is
// whether the boundary is entered at all, and with which arguments.
type recordRunner struct {
	starts   int
	lastArgv []string
	lastSpec sandbox.Spec
	output   string
	exitCode int
	startErr error
}

func (r *recordRunner) Name() string    { return "record" }
func (r *recordRunner) Available() bool { return true }
func (r *recordRunner) Close() error    { return nil }

func (r *recordRunner) Start(_ context.Context, argv []string, spec sandbox.Spec) (sandbox.Handle, error) {
	r.starts++
	r.lastArgv = append([]string(nil), argv...)
	r.lastSpec = spec
	if r.startErr != nil {
		return nil, r.startErr
	}
	return &recordHandle{out: []byte(r.output), code: r.exitCode}, nil
}

type recordHandle struct {
	out  []byte
	code int
}

func (h *recordHandle) Wait() (sandbox.Result, error) {
	return sandbox.Result{Output: h.out, ExitCode: h.code}, nil
}
func (h *recordHandle) Kill() error { return nil }

// setUserShellConfined installs the package-level intent without provisioning
// a backend: ModeOff records the setting and installs nothing, so tests can
// pair it with a fake runner and touch no real ACL.
func setUserShellConfined(t *testing.T, confined bool) {
	t.Helper()
	_, _, _ = sandbox.Init(sandbox.Config{Mode: sandbox.ModeOff, ConfineUserShell: confined})
	t.Cleanup(func() {
		sandbox.SetActive(nil)
		_, _, _ = sandbox.Init(sandbox.Config{Mode: sandbox.ModeOff})
	})
}

// TestUserShellRunsLocallyByDefault pins the default that keeps `! git push`
// working: a user command is not a model command, and confinement here is
// opt-in.
func TestUserShellRunsLocallyByDefault(t *testing.T) {
	setUserShellConfined(t, false)
	runner := &recordRunner{}
	sandbox.SetActive(runner)

	out, err := runUserShell(context.Background(), t.TempDir(), "echo local-marker")
	if err != nil {
		t.Fatalf("run: %v (output=%q)", err, out)
	}
	if runner.starts != 0 {
		t.Fatalf("the backend was used for a user command while confinement is off")
	}
	if !strings.Contains(out, "local-marker") {
		t.Fatalf("command did not actually run: %q", out)
	}
}

// TestUserShellUsesBackendWhenEnabled covers the opt-in: once the setting is
// on, the command must reach the backend with the working directory spelled
// out, because an inherited cwd is unreadable to the container token.
func TestUserShellUsesBackendWhenEnabled(t *testing.T) {
	setUserShellConfined(t, true)
	dir := t.TempDir()
	runner := &recordRunner{output: "from-backend"}
	sandbox.SetActive(runner)

	out, err := runUserShell(context.Background(), dir, "echo x")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if runner.starts != 1 {
		t.Fatalf("backend starts = %d, want 1", runner.starts)
	}
	if runner.lastSpec.Dir != dir {
		t.Fatalf("spec.Dir = %q, want %q", runner.lastSpec.Dir, dir)
	}
	if len(runner.lastArgv) == 0 || runner.lastArgv[len(runner.lastArgv)-1] != "echo x" {
		t.Fatalf("command string not passed through: %v", runner.lastArgv)
	}
	if out != "from-backend" {
		t.Fatalf("output = %q, want the backend's output", out)
	}
}

// TestUserShellReportsExitStatus keeps error text identical to os/exec, so the
// transcript reads the same whether or not the command was confined.
func TestUserShellReportsExitStatus(t *testing.T) {
	setUserShellConfined(t, true)
	runner := &recordRunner{output: "boom", exitCode: 3}
	sandbox.SetActive(runner)

	out, err := runUserShell(context.Background(), t.TempDir(), "false")
	if err == nil {
		t.Fatalf("a non-zero exit must return an error")
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("error = %q, want it to match os/exec's wording", err)
	}
	if out != "boom" {
		t.Fatalf("output lost on failure: %q", out)
	}
}

// TestUserShellRefusesWithoutADirectory: passing an empty Dir would make the
// backend inherit the parent's cwd, which the container cannot open. Refusing
// is better than a confusing access-denied further down.
func TestUserShellRefusesWithoutADirectory(t *testing.T) {
	setUserShellConfined(t, true)
	runner := &recordRunner{}
	sandbox.SetActive(runner)

	if _, err := runUserShell(context.Background(), "", "echo x"); err == nil {
		t.Fatalf("an empty working directory must be refused")
	}
	if runner.starts != 0 {
		t.Fatalf("the backend was started without a working directory")
	}
}

// TestUserShellDoesNotFallBackWhenStartFails is the same rule as every other
// entry point: a boundary that steps aside on failure is not a boundary.
func TestUserShellDoesNotFallBackWhenStartFails(t *testing.T) {
	setUserShellConfined(t, true)
	runner := &recordRunner{startErr: errRefused}
	sandbox.SetActive(runner)

	out, err := runUserShell(context.Background(), t.TempDir(), "echo should-not-run")
	if err == nil {
		t.Fatalf("a backend start failure must surface as an error")
	}
	if strings.Contains(out, "should-not-run") {
		t.Fatalf("the command ran unconfined after the backend refused: %q", out)
	}
	if runner.starts != 1 {
		t.Fatalf("backend starts = %d, want exactly 1 (no retry)", runner.starts)
	}
}

var errRefused = &refusalError{}

type refusalError struct{}

func (e *refusalError) Error() string { return "refused by backend" }
