package hooks

import (
	"context"
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
