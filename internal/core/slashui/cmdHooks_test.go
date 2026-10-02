package slashui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/conversation"
)

// isolateHooks gives the test an isolated HOME + cwd so /hooks reads and
// writes its own config.yaml.
func isolateHooks(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ICODE_MANAGED_CONFIG", filepath.Join(home, "nonexistent-managed.yaml"))
	t.Chdir(t.TempDir())
	return home
}

func TestCmdHooksLifecycleAddListRm(t *testing.T) {
	home := isolateHooks(t)
	b := &Backend{Engine: conversation.NewEngine(nil, nil, nil)}
	ctx := context.Background()
	st := &State{SessionID: "s1"}

	// Empty listing.
	res := Execute(ctx, b, st, "/hooks")
	if res.IsError || !strings.Contains(res.Output, "没有配置任何钩子") {
		t.Fatalf("empty list: %+v", res)
	}

	// Add with matcher + timeout.
	res = Execute(ctx, b, st, "/hooks add PreToolUse -m bash -t 10 -- go vet ./...")
	if res.IsError {
		t.Fatalf("add: %s", res.Output)
	}
	if !strings.Contains(res.Output, "已添加钩子 PreToolUse（匹配 bash）") {
		t.Errorf("add output: %s", res.Output)
	}
	// Config persisted?
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	list := cfg.Hooks["PreToolUse"]
	if len(list) != 1 || list[0].Matcher != "bash" || list[0].Command != "go vet ./..." || list[0].Timeout != 10 {
		t.Fatalf("persisted hooks: %+v", list)
	}
	// Written to disk under the isolated home?
	data, err := os.ReadFile(filepath.Join(home, ".icode", "config.yaml"))
	if err != nil {
		t.Fatalf("config file: %v", err)
	}
	if !strings.Contains(string(data), "go vet ./...") {
		t.Error("command not found in config.yaml on disk")
	}

	// Add a second rule under the same event.
	res = Execute(ctx, b, st, "/hooks add pretooluse echo second")
	if res.IsError {
		t.Fatalf("add 2: %s", res.Output)
	}

	// Listing shows both with per-event numbering.
	res = Execute(ctx, b, st, "/hooks")
	if res.IsError {
		t.Fatalf("list: %s", res.Output)
	}
	for _, want := range []string{"共 2 条", "go vet ./...", "echo second", "timeout=10s", "timeout=30s"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("list missing %q:\n%s", want, res.Output)
		}
	}

	// rm by per-event index.
	res = Execute(ctx, b, st, "/hooks rm PreToolUse 1")
	if res.IsError {
		t.Fatalf("rm: %s", res.Output)
	}
	if !strings.Contains(res.Output, "go vet ./...") {
		t.Errorf("rm output should echo removed command: %s", res.Output)
	}
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	list = cfg.Hooks["PreToolUse"]
	if len(list) != 1 || list[0].Command != "echo second" {
		t.Fatalf("after rm: %+v", list)
	}

	// rm last rule drops the empty event key.
	res = Execute(ctx, b, st, "/hooks rm pretooluse 1")
	if res.IsError {
		t.Fatalf("rm 2: %s", res.Output)
	}
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("reload 2: %v", err)
	}
	if _, exists := cfg.Hooks["PreToolUse"]; exists {
		t.Error("empty event key should be deleted after removing its last rule")
	}
}

func TestCmdHooksValidationAndReload(t *testing.T) {
	isolateHooks(t)
	b := &Backend{Engine: conversation.NewEngine(nil, nil, nil)}
	ctx := context.Background()
	st := &State{SessionID: "s1"}

	if res := Execute(ctx, b, st, "/hooks add Nope x"); !res.IsError || !strings.Contains(res.Output, "未知事件") {
		t.Errorf("unknown event: %+v", res)
	}
	if res := Execute(ctx, b, st, "/hooks rm PreToolUse 5"); !res.IsError || !strings.Contains(res.Output, "越界") {
		t.Errorf("rm out-of-range: %+v", res)
	}
	if res := Execute(ctx, b, st, "/hooks bogus"); !res.IsError || !strings.Contains(res.Output, "未知子命令") {
		t.Errorf("bad subcommand: %+v", res)
	}

	// events subcommand lists the protocol cheatsheet.
	res := Execute(ctx, b, st, "/hooks events")
	if res.IsError || !strings.Contains(res.Output, "PreToolUse") || !strings.Contains(res.Output, "SubagentStop") {
		t.Errorf("events: %s", res.Output)
	}

	// Seed config directly, then reload hot-pushes it into the engine.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Hooks = map[string][]config.HookRule{
		"Stop": {{Command: "echo seeded"}},
	}
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.Fatalf("save: %v", err)
	}
	res = Execute(ctx, b, st, "/hooks reload")
	if res.IsError || !strings.Contains(res.Output, "已从 config.yaml 重载 1 条钩子") {
		t.Fatalf("reload: %s", res.Output)
	}

	// Nil engine backend must not panic (config-only mode).
	res = Execute(ctx, &Backend{}, st, "/hooks add Stop echo ok")
	if res.IsError {
		t.Fatalf("add with nil engine: %s", res.Output)
	}
}
