package sessionum

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// ErrParse is wrapped by Import when the supplied JSON is not a valid session
// document, letting callers distinguish a bad payload from a storage failure.
var ErrParse = errors.New("parse session JSON")

// Export serializes a session (including its messages) for backup or transfer
// to another machine.
func Export(sess *types.Session) ([]byte, error) {
	return json.MarshalIndent(sess, "", "  ")
}

// Import creates a new session from an exported JSON document. The imported
// payload is treated as a fresh session: a new identity is assigned, stale
// counters/metadata are dropped and every message id is regenerated (ids are
// globally unique) while message content and timestamps are preserved. A
// partial import is rolled back if any message fails to persist.
func Import(store types.SessionStore, data []byte) (*types.Session, int, error) {
	var sess types.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrParse, err)
	}
	if sess.Title == "" {
		sess.Title = "Imported session"
	}

	msgs := sess.Messages
	sess.ID = ""
	sess.Messages = nil
	sess.Metadata = nil
	sess.TotalTokens = types.TokenUsage{}

	if err := store.Create(&sess); err != nil {
		return nil, 0, err
	}

	imported := 0
	for _, m := range msgs {
		m.ID = fmt.Sprintf("%x", time.Now().UnixNano()+int64(imported))
		if err := store.AppendMessage(sess.ID, m); err != nil {
			_ = store.Delete(sess.ID) // roll back the partial import
			return nil, imported, fmt.Errorf("import message: %w", err)
		}
		imported++
	}
	return &sess, imported, nil
}
