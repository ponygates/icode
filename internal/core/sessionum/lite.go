package sessionum

import (
	"fmt"

	"github.com/ponygates/icode/internal/types"
)

// LiteKey marks a session as "lite-resumed": the engine then feeds the model
// only the archived summary plus the most recent n messages instead of the
// full transcript — the real token saver behind summary-on-exit.
const LiteKey = "lite_n"

// LiteN returns how many of the most recent messages to replay (0 = full
// context, no lite mode).
func LiteN(sess *types.Session) int {
	if sess == nil || sess.Metadata == nil {
		return 0
	}
	switch v := sess.Metadata[LiteKey].(type) {
	case int:
		return v
	case float64: // JSON round-trip (persisted stores)
		return int(v)
	}
	return 0
}

// SetLite toggles lite-resume mode for a session. n <= 0 clears it and the
// session returns to full-context behavior.
func SetLite(store types.SessionStore, sess *types.Session, n int) error {
	if store == nil || sess == nil {
		return fmt.Errorf("lite: missing store or session")
	}
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	if n <= 0 {
		delete(sess.Metadata, LiteKey)
	} else {
		sess.Metadata[LiteKey] = n
	}
	return store.Update(sess)
}
