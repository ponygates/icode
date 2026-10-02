package conversation

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/types"
)

// stopBlockCmd returns a hook command that exits 2 the first `blockN` times
// it is invoked (persistent state via a counter file), then exits 0. This
// exercises the Stop-block continuation loop without an infinite hook.
func stopBlockCmd(t *testing.T, dir string, blockN int) string {
	t.Helper()
	cnt := filepath.Join(dir, "count.txt")
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "stopblock.cmd")
		// Redirect-first echo form avoids the `echo 1>` handle-redirect
		// parsing trap when the counter is 1.
		body := "@echo off\r\n" +
			"set n=0\r\n" +
			"if exist \"" + cnt + "\" set /p n=<\"" + cnt + "\"\r\n" +
			"set /a n+=1\r\n" +
			">\"" + cnt + "\" echo %n%\r\n" +
			"if %n% LSS " + itoa(blockN+1) + " exit /b 2\r\n" +
			"exit /b 0\r\n"
		if err := os.WriteFile(script, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		return script
	}
	script := filepath.Join(dir, "stopblock.sh")
	body := "#!/bin/sh\n" +
		"n=$(cat \"" + cnt + "\" 2>/dev/null || echo 0)\n" +
		"n=$((n+1))\n" +
		"echo $n >\"" + cnt + "\"\n" +
		"if [ \"$n\" -lt " + itoa(blockN+1) + " ]; then exit 2; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return script
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestStopHookBlockContinuesTurn is the full Stop-block loop: round 1 ends,
// the Stop hook exits 2 ("tests not green"), the engine injects the reason
// as a continuation message, re-opens the provider stream, and only the
// second round's natural completion emits the final EventDone.
func TestStopHookBlockContinuesTurn(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	p := &stagedProvider{replies: [][]types.StreamEvent{
		{ // Round 1 — plain text, then Stop hook blocks.
			{Type: types.EventText, Content: "第一轮"},
			{Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}},
		},
		{ // Round 2 — the forced continuation, Stop hook now allows.
			{Type: types.EventText, Content: "第二轮"},
			{Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}},
		},
	}}
	e, sess, _ := newRepairEngine(p, t)

	dir := t.TempDir()
	e.SetHooksRunner(hooks.NewRunner(map[string][]hooks.Rule{
		"Stop": {{Command: stopBlockCmd(t, dir, 1)}},
	}, dir))

	out, err := e.Send(context.Background(), sess.ID, "开始")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var texts, systems []string
	doneCount := 0
	for ev := range out {
		switch ev.Type {
		case types.EventText:
			texts = append(texts, ev.Content)
		case types.EventSystem:
			systems = append(systems, ev.Content)
		case types.EventDone:
			doneCount++
		}
	}

	if p.call != 2 {
		t.Fatalf("expected 2 model calls (blocked stop → continuation), got %d", p.call)
	}
	if !listContains(texts, "第一轮") || !listContains(texts, "第二轮") {
		t.Fatalf("expected both rounds' text, got %v", texts)
	}
	if !listContains(systems, "Stop 钩子要求继续工作") {
		t.Fatalf("expected continuation system notice, got %v", systems)
	}
	// One EventDone per completed round (single-emitter design), never a
	// duplicate from the outer loop — the old double-Done tripped the UI's
	// turn-end handler during continuation rounds.
	if doneCount != 2 {
		t.Fatalf("expected exactly 2 EventDone (one per round), got %d", doneCount)
	}
	// The continuation message rides in the round-2 request so the model
	// knows why it was resurrected.
	if len(p.seen) < 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(p.seen))
	}
	round2 := p.seen[1]
	foundCont := false
	for _, m := range round2.Messages {
		if m.Role == types.RoleUser && strings.Contains(m.Content, "[stop-hook]") {
			foundCont = true
			break
		}
	}
	if !foundCont {
		t.Fatalf("round-2 request missing [stop-hook] continuation message")
	}
	// The counter must be reset after the natural finish so the NEXT turn
	// gets a fresh budget.
	if n := e.bumpStopHookBlocks(sess.ID); n != 1 {
		t.Fatalf("block counter should reset after a natural finish, got %d", n)
	}
}

// TestStopHookBlockCapForcesEnd verifies the 3-consecutive-block cap: a hook
// that never relents gets 3 continuations, then the turn ends anyway.
func TestStopHookBlockCapForcesEnd(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	round := []types.StreamEvent{
		{Type: types.EventText, Content: "又完成一轮"},
		{Type: types.EventDone, Meta: types.StreamMeta{FinishReason: "stop"}},
	}
	p := &stagedProvider{replies: [][]types.StreamEvent{round, round, round, round, round}}
	e, sess, _ := newRepairEngine(p, t)

	dir := t.TempDir()
	// blockN=10: the hook would block forever if uncapped.
	e.SetHooksRunner(hooks.NewRunner(map[string][]hooks.Rule{
		"Stop": {{Command: stopBlockCmd(t, dir, 10)}},
	}, dir))

	out, err := e.Send(context.Background(), sess.ID, "开始")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var forced bool
	doneCount := 0
	for ev := range out {
		switch ev.Type {
		case types.EventSystem:
			if strings.Contains(ev.Content, "强制结束本轮") {
				forced = true
			}
		case types.EventDone:
			doneCount++
		}
	}
	if !forced {
		t.Fatal("expected the forced-end notice after 3 consecutive blocks")
	}
	// 4 rounds (1 initial + 3 forced continuations), each ending in exactly
	// one EventDone from the single-emitter design.
	if doneCount != 4 {
		t.Fatalf("expected 4 EventDone (one per round), got %d", doneCount)
	}
	// 1 initial round + 3 forced continuations = 4 model calls.
	if p.call != 4 {
		t.Fatalf("expected 4 model calls (1 + 3 continuations), got %d", p.call)
	}
}

func listContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
