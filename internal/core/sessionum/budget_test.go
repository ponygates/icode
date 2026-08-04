package sessionum

import (
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func TestBudgetWarning_NoBudget(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "nobudget")
	warn, used, budget := BudgetWarning(store, sess)
	if warn || used != 0 || budget != 0 {
		t.Fatalf("no budget should never warn (warn=%v used=%d budget=%d)", warn, used, budget)
	}
}

func TestBudgetWarning_FiresOnceUntilDrops(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "warn")
	if err := SetBudget(store, sess, 100); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	// ~91 tokens of content crosses the 80% bar (80) for a 100-token budget.
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: strings.Repeat("你", 270)}, // ~91 tokens
	}
	if err := store.Update(sess); err != nil {
		t.Fatalf("update: %v", err)
	}

	// First crossing warns and persists the mark.
	warn, used, budget := BudgetWarning(store, sess)
	if !warn {
		t.Fatalf("expected warning at 80%%+ (used=%d budget=%d)", used, budget)
	}

	// Second call on the same (still over-threshold) session stays quiet.
	if warn, _, _ := BudgetWarning(store, sess); warn {
		t.Fatalf("warning must fire only once per budget period")
	}

	// Once usage drops below the threshold the mark is cleared.
	sess.Messages = []types.Message{{Role: types.RoleUser, Content: "hi"}}
	if err := store.Update(sess); err != nil {
		t.Fatalf("update: %v", err)
	}
	if warn, _, _ := BudgetWarning(store, sess); warn {
		t.Fatalf("below-threshold session must not warn")
	}

	// Climbing back above threshold warns again.
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: strings.Repeat("你", 270)},
	}
	if err := store.Update(sess); err != nil {
		t.Fatalf("update: %v", err)
	}
	if warn, _, _ := BudgetWarning(store, sess); !warn {
		t.Fatalf("expected a fresh warning after dropping below threshold")
	}
}

func TestBudgetWarning_NilGuard(t *testing.T) {
	if warn, _, budget := BudgetWarning(nil, nil); warn || budget != 0 {
		t.Fatalf("nil session must not warn")
	}
}

func TestWarnPctDefaultAndBounds(t *testing.T) {
	if got := BudgetWarnPct(nil); got != 80 {
		t.Fatalf("nil session should default to 80, got %d", got)
	}
	store := mkStore(t)
	sess := mkSession(t, store, "pct")
	if got := BudgetWarnPct(sess); got != 80 {
		t.Fatalf("fresh session should default to 80, got %d", got)
	}
	if err := SetWarnPct(store, sess, 60); err != nil {
		t.Fatalf("set warn pct: %v", err)
	}
	if got := BudgetWarnPct(sess); got != 60 {
		t.Fatalf("expected 60, got %d", got)
	}
	if err := SetWarnPct(store, sess, 30); err == nil {
		t.Fatalf("out-of-range pct should be rejected")
	}
	if err := SetWarnPct(store, sess, 100); err == nil {
		t.Fatalf("out-of-range pct should be rejected")
	}
	if err := SetWarnPct(nil, sess, 60); err == nil {
		t.Fatalf("nil store should error")
	}
}

func TestBudgetWarningUsesCustomPct(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "customwarn")
	if err := SetBudget(store, sess, 100); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	if err := SetWarnPct(store, sess, 95); err != nil {
		t.Fatalf("set warn pct: %v", err)
	}
	// ~91 tokens is 91% — above the 80 default but below the custom 95.
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: strings.Repeat("你", 270)},
	}
	if err := store.Update(sess); err != nil {
		t.Fatalf("update: %v", err)
	}
	if warn, _, _ := BudgetWarning(store, sess); warn {
		t.Fatalf("91%% must not warn with a 95%% threshold")
	}
	// Crossing 95 does warn.
	sess.Messages = []types.Message{
		{Role: types.RoleUser, Content: strings.Repeat("你", 300)},
	}
	if err := store.Update(sess); err != nil {
		t.Fatalf("update: %v", err)
	}
	if warn, _, _ := BudgetWarning(store, sess); !warn {
		t.Fatalf("crossing a 95%% threshold should warn")
	}
}

func TestTrimStats(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "stats")
	if cnt, last, wc := TrimStats(sess); cnt != 0 || last != "" || wc != 0 {
		t.Fatalf("fresh session should have empty stats (cnt=%d last=%q wc=%d)", cnt, last, wc)
	}
	if err := RecordTrim(store, sess); err != nil {
		t.Fatalf("record trim: %v", err)
	}
	if err := RecordTrim(store, sess); err != nil {
		t.Fatalf("record trim: %v", err)
	}
	if err := RecordWarn(store, sess); err != nil {
		t.Fatalf("record warn: %v", err)
	}
	cnt, last, wc := TrimStats(sess)
	if cnt != 2 || last == "" || wc != 1 {
		t.Fatalf("expected 2 trims + 1 warn with a timestamp, got cnt=%d last=%q wc=%d", cnt, last, wc)
	}
}
