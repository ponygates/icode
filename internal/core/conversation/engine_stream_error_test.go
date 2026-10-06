package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// TestStreamErrorKeepsPartialOutput guards the C-batch resilience fix: a
// mid-stream EventError (network drop, provider reset, quota hit) must
// persist the partial assistant output into the session — the same
// guarantee as the user-interrupt path — and the surfaced error must tell
// the user the partial output survived.
func TestStreamErrorKeepsPartialOutput(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	p := &stagedProvider{replies: [][]types.StreamEvent{
		{
			{Type: types.EventText, Content: "已经写了一半"},
			{Type: types.EventError, Content: "read tcp 1.2.3.4:443: connection reset by peer"},
		},
	}}
	e, sess, _ := newRepairEngine(p, t)

	out, err := e.Send(context.Background(), sess.ID, "写点东西")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var sawText, sawKeptNotice bool
	var errContent string
	for ev := range out {
		switch ev.Type {
		case types.EventText:
			if ev.Content == "已经写了一半" {
				sawText = true
			}
		case types.EventError:
			errContent = ev.Content
			if strings.Contains(ev.Content, "已保留") {
				sawKeptNotice = true
			}
		}
	}
	if !sawText {
		t.Fatal("partial text event not forwarded to the UI")
	}
	if errContent == "" {
		t.Fatal("no error event surfaced")
	}
	if !sawKeptNotice {
		t.Fatalf("error event missing the kept-partial notice: %q", errContent)
	}

	// The partial output must be persisted into the session store so the
	// next turn (and the resumed session) see it.
	stored, err := e.sessionSt.Get(sess.ID)
	if err != nil {
		t.Fatalf("read session back: %v", err)
	}
	if len(stored.Messages) == 0 {
		t.Fatal("stored session has no messages")
	}
	last := stored.Messages[len(stored.Messages)-1]
	if last.Role != types.RoleAssistant || last.Content != "已经写了一半" {
		t.Fatalf("last stored message = %+v, want the partial assistant output", last)
	}
}

// TestStreamErrorEmptyOutputNoJunkNotice: an error before any output must
// not claim partial output was kept (nothing to keep).
func TestStreamErrorEmptyOutputNoJunkNotice(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	p := &stagedProvider{replies: [][]types.StreamEvent{
		{
			{Type: types.EventError, Content: "HTTP 500 — upstream exploded"},
		},
	}}
	e, sess, _ := newRepairEngine(p, t)

	out, err := e.Send(context.Background(), sess.ID, "写点东西")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	for ev := range out {
		if ev.Type == types.EventError {
			if strings.Contains(ev.Content, "已保留") {
				t.Fatalf("empty-output error falsely claims kept partial: %q", ev.Content)
			}
			if !strings.Contains(ev.Content, "500") {
				t.Fatalf("error content lost the original cause: %q", ev.Content)
			}
			return
		}
	}
	t.Fatal("no error event surfaced")
}
