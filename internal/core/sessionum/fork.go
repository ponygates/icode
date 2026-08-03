package sessionum

import (
	"fmt"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// Fork branches a new, independent session from the first n messages of a
// source session (Claude Code / opencode style session fork). The fork shares
// the prefix history but diverges from there — later edits to either side do
// not affect the other. It is a pure store operation: no model call.
//
// n <= 0 clones the whole session. n > len(Messages) is clamped. The archived
// summary is NOT copied (the fork's prefix differs, so its summary is
// regenerated on its own exit).
func Fork(store types.SessionStore, srcID string, n int) (*types.Session, error) {
	if store == nil || srcID == "" {
		return nil, fmt.Errorf("fork: missing store or source session")
	}
	src, err := store.Get(srcID)
	if err != nil {
		return nil, fmt.Errorf("fork: source session: %w", err)
	}
	if n <= 0 {
		n = len(src.Messages)
	}
	if n > len(src.Messages) {
		n = len(src.Messages)
	}
	if n == 0 {
		return nil, fmt.Errorf("fork: source session has no messages to branch from")
	}

	forked := &types.Session{
		ID:           newSessionID(),
		Title:        "Fork: " + src.Title,
		ModelID:      src.ModelID,
		ProviderName: src.ProviderName,
		Messages:     src.Messages[:n],
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		TotalTokens:  src.TotalTokens,
	}
	if err := store.Create(forked); err != nil {
		return nil, fmt.Errorf("fork: create: %w", err)
	}
	return forked, nil
}

// newSessionID mirrors the CLI's session id scheme.
func newSessionID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
