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
