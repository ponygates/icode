package conversation

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/types"
)

func TestFriendlyModelError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantSub string // substring expected in the friendly message
	}{
		{"401", errors.New("anthropic: HTTP 401 — invalid api key"), "API Key"},
		{"unauthorized", errors.New("openai: unauthorized"), "API Key"},
		{"403", errors.New("HTTP 403 forbidden"), "403"},
		{"429", errors.New("HTTP 429 rate limit exceeded"), "限流"},
		{"quota", errors.New("insufficient_quota"), "额度"},
		{"5xx", errors.New("HTTP 502 bad gateway"), "5xx"},
		{"overloaded", errors.New("overloaded_error"), "过载"},
		{"timeout", errors.New("context deadline exceeded"), "超时"},
		{"conn-refused", errors.New("dial tcp: connection refused"), "无法连接"},
		{"dns", errors.New("no such host"), "无法连接"},
		{"tls", errors.New("tls: handshake failure"), "无法连接"},
		{"model-not-found", errors.New("model \"x\" not found in any provider"), "模型 ID 不存在"},
		{"ctx-window", errors.New("maximum context length exceeded"), "上下文窗口"},
		{"passthrough", errors.New("some unknown error"), "some unknown error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := friendlyModelError(tc.err)
			if !strings.Contains(got, tc.wantSub) {
				t.Fatalf("friendlyModelError(%q) = %q, want substring %q", tc.err, got, tc.wantSub)
			}
		})
	}
	if friendlyModelError(nil) != "" {
		t.Fatal("nil error should yield empty string")
	}
}

func TestLongSessionHint(t *testing.T) {
	st := session.NewStore()
	e := NewEngine(nil, st, nil)

	short := &types.Session{ID: "short"}
	short.Messages = []types.Message{{Role: types.RoleUser, Content: "hi"}}
	if got := e.longSessionHint(short); got != "" {
		t.Fatalf("short session must not hint, got %q", got)
	}

	long := &types.Session{ID: "long"}
	for i := 0; i < 45; i++ {
		long.Messages = append(long.Messages,
			types.Message{Role: types.RoleUser, Content: fmt.Sprintf("q%d", i)},
			types.Message{Role: types.RoleAssistant, Content: fmt.Sprintf("a%d", i)},
		)
	}
	if got := e.longSessionHint(long); got == "" {
		t.Fatal("long session should hint once")
	} else if !strings.Contains(got, "/compact") {
		t.Fatalf("hint should mention /compact: %q", got)
	}
	// Fires only once per session (no nagging).
	if got := e.longSessionHint(long); got != "" {
		t.Fatalf("hint must fire only once, got %q", got)
	}

	// A session with an active budget/lite never hints.
	compacted := &types.Session{ID: "compacted"}
	for i := 0; i < 50; i++ {
		compacted.Messages = append(compacted.Messages,
			types.Message{Role: types.RoleUser, Content: "x"},
			types.Message{Role: types.RoleAssistant, Content: "y"},
		)
	}
	if err := st.Create(compacted); err != nil {
		t.Fatalf("create compacted: %v", err)
	}
	if err := sessionum.SetBudget(st, compacted, 16000); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	if got := e.longSessionHint(compacted); got != "" {
		t.Fatalf("budgeted session must not hint, got %q", got)
	}
}
