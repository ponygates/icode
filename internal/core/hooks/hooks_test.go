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
		// New lifecycle events (C4 batch).
		"SessionEnd":  {{Command: passCmd}},
		"PostCompact": {{Command: passCmd}},
		"ToolError":   {{Command: passCmd}},
		"AgentStart":  {{Command: passCmd}},
		"AgentStop":   {{Command: passCmd}},
	}, ".")

	all := []Event{
		SessionStart, Notification, PreCompact, PermissionRequest,
		SubagentStart, SubagentStop,
		SessionEnd, PostCompact, ToolError, AgentStart, AgentStop,
	}
	for _, ev := range all {
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

// jsonEchoCmd builds a cross-platform hook command that prints the given
// single-line JSON on stdout and exits 0. On Windows a temp .cmd file is
// used so the quoting survives cmd.exe verbatim.
func jsonEchoCmd(t *testing.T, jsonLine string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "printf '" + jsonLine + "'"
	}
	script := filepath.Join(t.TempDir(), "hook.cmd")
	if err := os.WriteFile(script, []byte("@echo "+jsonLine), 0644); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestFireJSONDecisionBlock(t *testing.T) {
	r := NewRunner(map[string][]Rule{
		"PreToolUse": {{Matcher: "bash", Command: jsonEchoCmd(t, `{"decision":"block","reason":"NOPE"}`)}},
	}, ".")
	res := r.Fire(context.Background(), PreToolUse, Input{ToolName: "bash"})
	if !res.Block || res.Message != "NOPE" {
		t.Fatalf("expected block with reason NOPE, got %+v", res)
	}
	// Matcher still applies: a non-matching tool is untouched.
	if res := r.Fire(context.Background(), PreToolUse, Input{ToolName: "edit"}); res.Block {
		t.Fatal("edit must not be blocked (matcher mismatch)")
	}
}

func TestFireJSONPermissionDecision(t *testing.T) {
	r := NewRunner(map[string][]Rule{
		"PreToolUse": {{Command: jsonEchoCmd(t, `{"permissionDecision":"deny","reason":"not on prod"}`)}},
	}, ".")
	res := r.Fire(context.Background(), PreToolUse, Input{ToolName: "bash"})
	if res.Block {
		t.Fatal("permissionDecision must not set Block")
	}
	if res.PermissionDecision != "deny" {
		t.Fatalf("expected deny, got %q", res.PermissionDecision)
	}
	if res.Message != "not on prod" {
		t.Fatalf("expected reason surfaced as message, got %q", res.Message)
	}
	// Bogus values are ignored rather than half-applied.
	r2 := NewRunner(map[string][]Rule{
		"PreToolUse": {{Command: jsonEchoCmd(t, `{"permissionDecision":"maybe"}`)}},
	}, ".")
	if res := r2.Fire(context.Background(), PreToolUse, Input{ToolName: "bash"}); res.PermissionDecision != "" {
		t.Fatalf("unknown permissionDecision must be ignored, got %q", res.PermissionDecision)
	}
}

func TestFireJSONSuppressOutputAndSystemMessage(t *testing.T) {
	r := NewRunner(map[string][]Rule{
		"PostToolUse": {{Command: jsonEchoCmd(t, `{"suppressOutput":true,"systemMessage":"heads up"}`)}},
	}, ".")
	res := r.Fire(context.Background(), PostToolUse, Input{ToolName: "bash"})
	if !res.SuppressOutput || res.SystemMessage != "heads up" {
		t.Fatalf("expected suppress+systemMessage, got %+v", res)
	}
}

func TestFireStopContinueFalse(t *testing.T) {
	r := NewRunner(map[string][]Rule{
		"Stop": {{Command: jsonEchoCmd(t, `{"continue":false,"stopReason":"tests failing"}`)}},
	}, ".")
	res := r.Fire(context.Background(), Stop, Input{SessionID: "s1"})
	if !res.Block || res.Message != "tests failing" {
		t.Fatalf("expected Stop block with stopReason, got %+v", res)
	}
	// continue:true (or absent) must NOT block.
	r2 := NewRunner(map[string][]Rule{
		"Stop": {{Command: jsonEchoCmd(t, `{"continue":true}`)}},
	}, ".")
	if res := r2.Fire(context.Background(), Stop, Input{SessionID: "s1"}); res.Block {
		t.Fatalf("continue:true must not block, got %+v", res)
	}
}

func TestFireTimeoutMarksResult(t *testing.T) {
	// A hook that sleeps far past its 1s timeout. ping is the canonical
	// portable-ish Windows delay; sleep everywhere else.
	sleepCmd := "sleep 5"
	if runtime.GOOS == "windows" {
		sleepCmd = "ping -n 5 127.0.0.1 > nul"
	}
	r := NewRunner(map[string][]Rule{
		"PreToolUse": {{Command: sleepCmd, Timeout: 1}},
	}, ".")
	res := r.Fire(context.Background(), PreToolUse, Input{ToolName: "bash"})
	if res.Block {
		t.Fatal("timeout must never block")
	}
	if !res.TimedOut {
		t.Fatal("expected TimedOut=true for a killed hook")
	}
	if res.SystemMessage == "" {
		t.Fatal("timeout should surface a system message for observability")
	}
}

func TestParseHookJSON(t *testing.T) {
	if parseHookJSON("") != nil || parseHookJSON("hello") != nil || parseHookJSON("[1,2]") != nil {
		t.Fatal("non-JSON-object stdout must parse to nil")
	}
	ho := parseHookJSON(`{"decision":"block","reason":"r"}`)
	if ho == nil || ho.Decision != "block" || ho.Reason != "r" {
		t.Fatalf("unexpected parse: %+v", ho)
	}
	// Invalid JSON that still starts with '{' is treated as no output.
	if parseHookJSON(`{"decision":`) != nil {
		t.Fatal("truncated JSON must be nil")
	}
}
