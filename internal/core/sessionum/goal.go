package sessionum

import (
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// GoalKey is where the long-goal lives on a session (Metadata["goal"]).
// A set goal puts the session into "long-goal mode": the engine injects it
// into the system prompt on every turn so the model keeps working toward it.
const GoalKey = "goal"

// GetGoal returns the session's long goal, or "" when none is set.
func GetGoal(sess *types.Session) string {
	if sess == nil || sess.Metadata == nil {
		return ""
	}
	g, _ := sess.Metadata[GoalKey].(string)
	return g
}

// SetGoal persists the session's long goal. An empty goal clears it.
func SetGoal(store types.SessionStore, sess *types.Session, goal string) error {
	if store == nil || sess == nil {
		return fmt.Errorf("goal: missing store or session")
	}
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	if strings.TrimSpace(goal) == "" {
		delete(sess.Metadata, GoalKey)
	} else {
		sess.Metadata[GoalKey] = strings.TrimSpace(goal)
	}
	return store.Update(sess)
}
