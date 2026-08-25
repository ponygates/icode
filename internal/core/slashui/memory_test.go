package slashui

import (
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/core/prefmem"
)

// memoryBackend returns a Backend whose engine has a fresh prefmem store,
// plus a way to grab that store.
func memoryBackend() (*Backend, *prefmem.Store) {
	eng := conversation.NewEngine(nil, nil, nil)
	eng.SetPreferenceMemory(prefmem.New(prefmem.Options{}))
	return &Backend{Engine: eng}, eng.PreferenceMemory()
}

func TestCmdMemory_PrefListEmpty(t *testing.T) {
	b, _ := memoryBackend()
	res := cmdMemory(b, []string{"prefs"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "暂无已记忆") && !strings.Contains(res.Output, "偏好记忆") {
		t.Fatalf("expected empty-prefs message, got: %s", res.Output)
	}
}

func TestCmdMemory_PrefListShowsEntries(t *testing.T) {
	b, store := memoryBackend()
	store.Remember("以后都用简体中文回答")
	res := cmdMemory(b, []string{"prefs"})
	if !strings.Contains(res.Output, "简体中文回答") {
		t.Fatalf("prefs list should include remembered entry, got: %s", res.Output)
	}
}

func TestCmdMemory_ForgetAndClear(t *testing.T) {
	b, store := memoryBackend()
	store.Remember("以后都用简体中文回答")
	store.Remember("优先用 Go 写后台服务")

	res := cmdMemory(b, []string{"forget", "简体中文"})
	if res.IsError {
		t.Fatalf("forget unexpectedly errored: %s", res.Output)
	}
	remain := store.Snapshot()
	if len(remain) != 1 {
		t.Fatalf("expected 1 pref after forget, got %d: %+v", len(remain), remain)
	}

	// Clear requires confirmation.
	res = cmdMemory(b, []string{"clear"})
	if strings.Contains(res.Output, "已清空") {
		t.Fatalf("clear should require confirmation, got: %s", res.Output)
	}
	res = cmdMemory(b, []string{"clear", "yes"})
	if !strings.Contains(res.Output, "已清空") {
		t.Fatalf("clear yes should wipe, got: %s", res.Output)
	}
	if n := len(store.Snapshot()); n != 0 {
		t.Fatalf("expected 0 prefs after clear, got %d", n)
	}
}
