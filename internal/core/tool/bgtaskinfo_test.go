package tool

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTailStr(t *testing.T) {
	// Short string untouched.
	if got := tailStr("hello", 10); got != "hello" {
		t.Fatalf("tailStr short = %q", got)
	}
	// Long ASCII string keeps exactly the last n bytes.
	long := strings.Repeat("a", 100) + "END"
	if got := tailStr(long, 10); len(got) != 10 || !strings.HasSuffix(got, "END") {
		t.Fatalf("tailStr ascii = %q (len %d)", got, len(got))
	}
	// Chinese text is never cut mid-rune: with a 7-byte budget the cut
	// lands inside "法", so it slides forward and keeps whole runes only
	// ("内容" = 6 bytes ≤ 7; adding "法" would exceed the budget).
	zh := "保险普法内容"
	if got := tailStr(zh, 7); got != "内容" {
		t.Fatalf("tailStr rune-safe = %q, want 内容", got)
	}
	if got := tailStr(zh, 9); got != "法内容" {
		t.Fatalf("tailStr rune-safe 9 = %q, want 法内容", got)
	}
}

func TestListBgTaskInfos_Agent(t *testing.T) {
	fr := &fakeRunner{delay: 40 * time.Millisecond}
	id, err := launchBackgroundAgent(fr, "explore", "find permission code")
	if err != nil {
		t.Fatalf("launch: %v", err)
	}

	var running BgTaskInfo
	found := false
	for _, info := range ListBgTaskInfos() {
		if info.ID == id {
			found = true
			running = info
			break
		}
	}
	if !found {
		t.Fatal("running agent task not listed")
	}
	if running.Kind != "agent" || running.Status != "running" {
		t.Fatalf("running info = %+v", running)
	}
	if running.Label != "explore" || running.Brief != "find permission code" {
		t.Fatalf("label/brief = %q/%q", running.Label, running.Brief)
	}

	waitFor(t, 2*time.Second, func() bool {
		for _, info := range ListBgTaskInfos() {
			if info.ID == id {
				return info.Status == "finished"
			}
		}
		return false
	})
	for _, info := range ListBgTaskInfos() {
		if info.ID == id {
			if info.Tokens != 42 {
				t.Fatalf("tokens = %d, want 42", info.Tokens)
			}
			if !strings.Contains(info.Tail, "done: find permission code") {
				t.Fatalf("tail = %q", info.Tail)
			}
			return
		}
	}
	t.Fatal("finished agent task disappeared from listing")
}

func TestListBgTaskInfos_Shell(t *testing.T) {
	id, err := bgTasks.Start("echo hi-from-shell", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		for _, info := range ListBgTaskInfos() {
			if info.ID == id {
				return info.Status == "finished"
			}
		}
		return false
	})
	for _, info := range ListBgTaskInfos() {
		if info.ID == id {
			if info.Kind != "shell" {
				t.Fatalf("kind = %q", info.Kind)
			}
			if info.Label != "echo hi-from-shell" {
				t.Fatalf("label = %q", info.Label)
			}
			if !strings.Contains(info.Tail, "hi-from-shell") {
				t.Fatalf("tail = %q", info.Tail)
			}
			return
		}
	}
	t.Fatal("shell task disappeared from listing")
}

func TestCancelShellTask(t *testing.T) {
	slow := "sleep 30"
	if runtime.GOOS == "windows" {
		slow = "ping -n 30 127.0.0.1"
	}
	id, err := bgTasks.Start(slow, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Unknown / already-finished ids refuse politely.
	if CancelShellTask("bg-999999") {
		t.Fatal("cancel unknown id returned true")
	}

	if !CancelShellTask(id) {
		t.Fatal("cancel running task returned false")
	}
	waitFor(t, 5*time.Second, func() bool {
		for _, info := range ListBgTaskInfos() {
			if info.ID == id {
				return info.Status == "cancelled"
			}
		}
		return false
	})
	// Cancelling twice: the task is done now, so it must report false.
	if CancelShellTask(id) {
		t.Fatal("cancel of finished task returned true")
	}
}

func TestBgStatusPrecedence(t *testing.T) {
	cases := []struct {
		done, cancelled bool
		errMsg          string
		want            string
	}{
		{false, false, "", "running"},
		{true, false, "", "finished"},
		{true, false, "boom", "failed"},
		{true, true, "context canceled", "cancelled"}, // cancel beats error
	}
	for _, c := range cases {
		if got := bgStatus(c.done, c.errMsg, c.cancelled); got != c.want {
			t.Fatalf("bgStatus(%v,%q,%v) = %q, want %q", c.done, c.errMsg, c.cancelled, got, c.want)
		}
	}
}
