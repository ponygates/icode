package hooks

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMatches(t *testing.T) {
	cases := []struct {
		pattern, tool string
		want          bool
	}{
		{"", "bash", true},
		{"*", "edit", true},
		{"bash", "bash", true},
		{"bash", "edit", false},
		{"bash|edit", "edit", true},
		{"write_.*", "write_file", true},
		{"write_.*", "read_file", false},
	}
	for _, c := range cases {
		if got := matches(c.pattern, c.tool); got != c.want {
			t.Errorf("matches(%q,%q)=%v want %v", c.pattern, c.tool, got, c.want)
		}
	}
}

func TestFireNilRunner(t *testing.T) {
	var r *Runner
	res := r.Fire(context.Background(), PreToolUse, Input{ToolName: "bash"})
	if res.Block {
		t.Fatal("nil runner must never block")
	}
	if r.HasHooks(PreToolUse) {
		t.Fatal("nil runner has no hooks")
	}
}

func TestFireBlockAndPass(t *testing.T) {
	blockCmd := "exit 2"
	passCmd := "exit 0"
	if runtime.GOOS == "windows" {
		blockCmd = "exit /b 2"
		passCmd = "exit /b 0"
	}
	r := NewRunner(map[string][]Rule{
		"PreToolUse": {
			{Matcher: "bash", Command: blockCmd},
			{Matcher: "edit", Command: passCmd},
		},
	}, ".")

	if !r.HasHooks(PreToolUse) {
		t.Fatal("expected hooks registered")
	}
	// bash matches the blocking rule
	if res := r.Fire(context.Background(), PreToolUse, Input{ToolName: "bash"}); !res.Block {
		t.Error("expected bash to be blocked by exit-2 hook")
	}
	// edit matches only the passing rule
	if res := r.Fire(context.Background(), PreToolUse, Input{ToolName: "edit"}); res.Block {
		t.Error("edit should not be blocked")
	}
	// unmatched tool fires nothing
	if res := r.Fire(context.Background(), PreToolUse, Input{ToolName: "grep"}); res.Block {
		t.Error("grep matches no rule; must not block")
	}
}

func TestFireUserPromptSubmitRewrite(t *testing.T) {
	// A hook that echoes back a rewritten prompt as stdout JSON. Use a
	// temp script so the test is immune to shell quoting differences.
	dir := t.TempDir()
	cmd := "python -c \"import sys; sys.stdout.write('{\\\"prompt\\\":\\\"REWRITTEN\\\"}')\""
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "rewrite.cmd")
		// cmd echo mangles quotes; a here-file written by Go is exact.
		if err := os.WriteFile(script, []byte("@echo {\"prompt\":\"REWRITTEN\"}"), 0644); err != nil {
			t.Fatal(err)
		}
		cmd = script
	}
	r := NewRunner(map[string][]Rule{
		"UserPromptSubmit": {{Command: cmd}},
	}, ".")

	if !r.HasHooks(UserPromptSubmit) {
		t.Fatal("expected UserPromptSubmit hooks registered")
	}
	res := r.Fire(context.Background(), UserPromptSubmit, Input{Prompt: "original"})
	if res.Block {
		t.Fatal("rewrite hook must not block")
	}
	if res.Prompt != "REWRITTEN" {
		t.Errorf("expected rewritten prompt, got %q", res.Prompt)
	}
}

func TestFireUserPromptSubmitBlock(t *testing.T) {
	blockCmd := "exit 2"
	if runtime.GOOS == "windows" {
		blockCmd = "exit /b 2"
	}
	r := NewRunner(map[string][]Rule{
		"UserPromptSubmit": {{Command: blockCmd}},
	}, ".")

	res := r.Fire(context.Background(), UserPromptSubmit, Input{Prompt: "original"})
	if !res.Block {
		t.Fatal("exit-2 UserPromptSubmit hook must block the message")
	}
}

func TestFireUserPromptSubmitPlainStdoutIgnored(t *testing.T) {
	// Plain stdout (not JSON) must NOT be treated as a rewrite.
	cmd := "echo hello"
	r := NewRunner(map[string][]Rule{
		"UserPromptSubmit": {{Command: cmd}},
	}, ".")

	res := r.Fire(context.Background(), UserPromptSubmit, Input{Prompt: "original"})
	if res.Block || res.Prompt != "" {
		t.Fatalf("plain stdout must be ignored, got block=%v prompt=%q", res.Block, res.Prompt)
	}
}

// TestFireLifecycleEvents verifies the newer lifecycle events (Notification /
// PreCompact / SessionStart / PermissionRequest / SubagentStart / SubagentStop)
// dispatch to their configured rules and carry the payload fields.
func TestFireLifecycleEvents(t *testing.T) {
	passCmd := "exit 0"
	if runtime.GOOS == "windows" {
		passCmd = "exit /b 0"
	}
	dir := t.TempDir()
	trackCmd := "python -c \"import sys; open(sys.argv[1],'a').write('hit')\" " + filepath.Join(dir, "hits.txt")
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "hit.cmd")
		if err := os.WriteFile(script, []byte("@echo hit>>\""+filepath.Join(dir, "hits.txt")+"\""), 0644); err != nil {
			t.Fatal(err)
		}
		trackCmd = script
	}
	r := NewRunner(map[string][]Rule{
		"SessionStart":      {{Command: passCmd}},
		"Notification":      {{Command: trackCmd}},
		"PreCompact":        {{Command: passCmd}},
		"PermissionRequest": {{Command: passCmd}},
		"SubagentStart":     {{Command: passCmd}},
		"SubagentStop":      {{Command: passCmd}},
	}, ".")

	for _, ev := range []Event{SessionStart, Notification, PreCompact, PermissionRequest, SubagentStart, SubagentStop} {
		if !r.HasHooks(ev) {
			t.Errorf("%s hook not registered", ev)
		}
		if res := r.Fire(context.Background(), ev, Input{SessionID: "s1", ToolName: "bash", Prompt: "p"}); res.Block {
			t.Errorf("%s must not block: %+v", ev, res)
		}
	}
	// Notification rule wrote a marker file.
	if data, err := os.ReadFile(filepath.Join(dir, "hits.txt")); err != nil || len(data) == 0 {
		t.Errorf("Notification hook did not run: %v", err)
	}
}
