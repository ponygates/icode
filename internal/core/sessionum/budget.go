package sessionum

import (
	"fmt"
	"time"

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

// WarnPctKey stores a per-session warning threshold (default 80) — the usage
// percentage of the budget at which the user is nudged before the hard trim.
const WarnPctKey = "budget_warn_pct"

// TrimCountKey / TrimLastKey / WarnCountKey keep a lightweight audit trail on
// the session of how often the budget tripped, so /budget show can surface it.
const (
	TrimCountKey = "budget_trim_count"
	TrimLastKey  = "budget_trim_last"
	WarnCountKey = "budget_warn_count"
)

// RecordTrim bumps the per-session trim counter and timestamp after a
// budget-driven compaction so users can see how often context was clipped.
func RecordTrim(store types.SessionStore, sess *types.Session) error {
	if store == nil || sess == nil {
		return fmt.Errorf("budget: missing store or session")
	}
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	n, _ := sess.Metadata[TrimCountKey].(int)
	sess.Metadata[TrimCountKey] = n + 1
	sess.Metadata[TrimLastKey] = time.Now().Format(time.RFC3339)
	return store.Update(sess)
}

// RecordWarn bumps the per-session warning counter.
func RecordWarn(store types.SessionStore, sess *types.Session) error {
	if store == nil || sess == nil {
		return fmt.Errorf("budget: missing store or session")
	}
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	n, _ := sess.Metadata[WarnCountKey].(int)
	sess.Metadata[WarnCountKey] = n + 1
	return store.Update(sess)
}

// TrimStats returns the recorded audit trail (trim count, last trim time,
// warning count) for the session.
func TrimStats(sess *types.Session) (count int, last string, warnCount int) {
	if sess == nil || sess.Metadata == nil {
		return 0, "", 0
	}
	count, _ = sess.Metadata[TrimCountKey].(int)
	last, _ = sess.Metadata[TrimLastKey].(string)
	warnCount, _ = sess.Metadata[WarnCountKey].(int)
	return count, last, warnCount
}

// BudgetWarnPct returns the session's warning threshold in percent (default 80).
func BudgetWarnPct(sess *types.Session) int {
	if sess == nil || sess.Metadata == nil {
		return 80
	}
	switch v := sess.Metadata[WarnPctKey].(type) {
	case int:
		if v >= 50 && v <= 95 {
			return v
		}
	case float64: // JSON round-trip
		if v >= 50 && v <= 95 {
			return int(v)
		}
	}
	return 80
}

// SetWarnPct persists the session's warning threshold. pct must be 50-95.
func SetWarnPct(store types.SessionStore, sess *types.Session, pct int) error {
	if store == nil || sess == nil {
		return fmt.Errorf("budget: missing store or session")
	}
	if pct < 50 || pct > 95 {
		return fmt.Errorf("预算预警阈值应在 50%%~95%% 之间")
	}
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	sess.Metadata[WarnPctKey] = pct
	return store.Update(sess)
}

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
	threshold := BudgetWarnPct(sess)
	atThreshold := used*100 >= budget*threshold
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
