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
