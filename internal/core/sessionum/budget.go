package sessionum

import (
	"fmt"

	"github.com/ponygates/icode/internal/types"
)

// BudgetKey stores the session's hard token budget (Metadata["budget_tokens"]).
// When set, the engine shrinks the context to fit (archived summary + recent
// messages) instead of letting the conversation grow unbounded.
const BudgetKey = "budget_tokens"

// BudgetMax returns the session's token budget, or 0 when unset.
func BudgetMax(sess *types.Session) int {
	if sess == nil || sess.Metadata == nil {
		return 0
	}
	switch v := sess.Metadata[BudgetKey].(type) {
	case int:
		return v
	case float64: // JSON round-trip (persisted stores)
		return int(v)
	}
	return 0
}

// SetBudget persists the session's token budget. n <= 0 clears it.
func SetBudget(store types.SessionStore, sess *types.Session, n int) error {
	if store == nil || sess == nil {
		return fmt.Errorf("budget: missing store or session")
	}
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	if n <= 0 {
		delete(sess.Metadata, BudgetKey)
	} else {
		sess.Metadata[BudgetKey] = n
	}
	return store.Update(sess)
}

// EstimatedUsage returns a cheap estimate of the session's current token
// usage (same approximation TrimToBudget/BudgetWarning rely on), so /budget
// show can report how close the conversation is to the limit.
func EstimatedUsage(sess *types.Session) int {
	if sess == nil {
		return 0
	}
	n := 0
	for _, m := range sess.Messages {
		n += approxTokens(m)
	}
	return n
}

// WarnKey records that the 80% pre-budget warning has been emitted for this
// session, so the user is nudged only once per budget period.
const WarnKey = "budget_warned"

// BudgetWarning reports whether the session has crossed the 80% warning
// threshold of its budget. On the first crossing it marks the session (via
// store.Update) so the nudge fires exactly once; when usage drops back below
// the threshold the mark is cleared so a future climb warns again.
func BudgetWarning(store types.SessionStore, sess *types.Session) (warn bool, used, budget int) {
	budget = BudgetMax(sess)
	if budget <= 0 || sess == nil {
		return false, 0, 0
	}
	used = EstimatedUsage(sess)
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	warned, _ := sess.Metadata[WarnKey].(bool)
	atThreshold := used*100 >= budget*80
	switch {
	case atThreshold && !warned:
		sess.Metadata[WarnKey] = true
		if store != nil {
			_ = store.Update(sess)
		}
		return true, used, budget
	case !atThreshold && warned:
		delete(sess.Metadata, WarnKey)
		if store != nil {
			_ = store.Update(sess)
		}
	}
	return false, used, budget
}
