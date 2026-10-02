package conversation

import (
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/session"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/types"
)

// Auto-compact unit tests: the engine threshold trigger drives the existing
// tokenopt compaction pipeline (no second summariser), with an anti-thrash
// turn gap and an explicit disable path.

func newAutoCompactEngine() *Engine {
	return NewEngine(&fakeRegistry{}, session.NewStore(), nil)
}

// fillerMessage is a ~500-ASCII-token message (countTokens ≈ chars/4).
func fillerMessage(role types.Role) types.Message {
	return types.Message{Role: role, Content: strings.Repeat("a", 2000)}
}

// bigOptimizer returns an optimizer whose 40-message log (~20K tokens) far
// exceeds the 85%-of-4000 threshold (3400), plus the matching session.
func bigSetup() (*tokenopt.Optimizer, *types.Session) {
	opt := tokenopt.New(tokenopt.Config{
		ModelInfo: types.ModelInfo{ID: "fake-model", Provider: "fake", ContextWindow: 4000},
	})
	msgs := make([]types.Message, 0, 40)
	for i := 0; i < 40; i++ {
		m := fillerMessage(types.RoleUser)
		if i%2 == 1 {
			m = fillerMessage(types.RoleAssistant)
		}
		opt.AddMessage(m)
		msgs = append(msgs, m)
	}
	sess := &types.Session{ID: "ac-sess", Messages: msgs}
	return opt, sess
}

func TestAutoCompact_TriggersAtThreshold(t *testing.T) {
	e := newAutoCompactEngine()
	opt, sess := bigSetup()

	saved, pct := e.maybeAutoCompact(sess.ID, sess, opt)
	if saved <= 0 || pct != DefaultAutoCompactPct {
		t.Fatalf("expected compaction at threshold, got saved=%d pct=%d", saved, pct)
	}
	if opt.CompactionSummary() == "" {
		t.Fatal("expected the existing summariser to produce a compaction summary")
	}
	if opt.EstimateTokens() >= 3400 {
		t.Fatalf("expected estimate under threshold after compaction, got %d", opt.EstimateTokens())
	}
}

func TestAutoCompact_DoesNotTriggerUnderThreshold(t *testing.T) {
	e := newAutoCompactEngine()
	opt := tokenopt.New(tokenopt.Config{
		ModelInfo: types.ModelInfo{ID: "fake-model", Provider: "fake", ContextWindow: 4000},
	})
	opt.AddMessage(fillerMessage(types.RoleUser)) // ~500 tokens, way under 85%
	sess := &types.Session{ID: "ac-sess", Messages: []types.Message{fillerMessage(types.RoleUser)}}

	before := opt.EstimateTokens()
	saved, _ := e.maybeAutoCompact(sess.ID, sess, opt)
	if saved != 0 {
		t.Fatalf("must not compact under the threshold, saved=%d", saved)
	}
	if opt.EstimateTokens() != before {
		t.Fatal("message log changed although below threshold")
	}
}

func TestAutoCompact_DoesNotThrashOnConsecutiveTurns(t *testing.T) {
	e := newAutoCompactEngine()
	opt, sess := bigSetup()

	saved, _ := e.maybeAutoCompact(sess.ID, sess, opt)
	if saved <= 0 {
		t.Fatal("first compaction should fire at the threshold")
	}
	// Re-inflate the optimizer past the threshold (a huge recent message can
	// keep a session over threshold right after compaction). The next two
	// turns must NOT re-compact (anti-thrash gap), the turn after may.
	for i := 0; i < 40; i++ {
		opt.AddMessage(fillerMessage(types.RoleAssistant))
	}
	if saved, _ := e.maybeAutoCompact(sess.ID, sess, opt); saved != 0 {
		t.Fatalf("same turn count must be blocked by the thrash guard, saved=%d", saved)
	}
	sess2 := &types.Session{ID: sess.ID}
	for i := 0; i < autoCompactMinTurnGapDelta; i++ {
		sess2.Messages = append(sess2.Messages, fillerMessage(types.RoleUser))
	}
	sess2.Messages = append(sess2.Messages, sess.Messages...)
	if saved, _ := e.maybeAutoCompact(sess.ID, sess2, opt); saved <= 0 {
		t.Fatalf("after the gap elapses compaction must be allowed again, saved=%d", saved)
	}
}

func TestAutoCompact_RespectsDisableSetting(t *testing.T) {
	e := newAutoCompactEngine()
	e.SetAutoCompactPct(0) // config tools.auto_compact_pct: 0 = manual /compact only
	opt, sess := bigSetup()

	if saved, pct := e.maybeAutoCompact(sess.ID, sess, opt); saved != 0 || pct != 0 {
		t.Fatalf("disabled auto-compact must be a no-op, saved=%d pct=%d", saved, pct)
	}
	if opt.CompactionSummary() != "" {
		t.Fatal("disabled auto-compact must not run the summariser")
	}
	// With auto-compact disabled the manual /compact nudge stays available.
	sess.Messages = make([]types.Message, longSessionThreshold)
	for i := range sess.Messages {
		sess.Messages[i] = fillerMessage(types.RoleUser)
	}
	if e.longSessionHint(sess) == "" {
		t.Fatal("long-session hint must return when auto-compact is off")
	}
}

func TestAutoCompact_InFlightTurnIsNotCompacted(t *testing.T) {
	e := newAutoCompactEngine()
	opt, sess := bigSetup()

	// maybeAutoCompact runs BEFORE the current user message enters the
	// optimizer (Send wiring); the current turn must survive verbatim.
	e.maybeAutoCompact(sess.ID, sess, opt)
	current := types.Message{Role: types.RoleUser, Content: "CURRENT-USER-TURN-MUST-SURVIVE"}
	opt.AddMessage(current)
	req := opt.CompactRequest("")
	found := false
	for _, m := range req {
		if strings.Contains(m.Content, "CURRENT-USER-TURN-MUST-SURVIVE") {
			found = true
		}
	}
	if !found {
		t.Fatal("current in-flight user message was folded into the summary")
	}
}

func TestAutoCompact_HintSuppressedWhenEnabled(t *testing.T) {
	e := newAutoCompactEngine()
	sess := &types.Session{ID: "ac-sess", Messages: make([]types.Message, longSessionThreshold)}
	for i := range sess.Messages {
		sess.Messages[i] = fillerMessage(types.RoleUser)
	}
	if hint := e.longSessionHint(sess); hint != "" {
		t.Fatalf("with auto-compact on the /compact nudge must be silent, got %q", hint)
	}
}
