package sessionum

import (
	"fmt"

	"github.com/ponygates/icode/internal/types"
)

// TrashKey marks a session as soft-deleted (Metadata["deleted"]). Cleared
// sessions keep their transcript and archived summary but are hidden from
// lists and restorable via /restore — a cheap safeguard against accidental
// data loss.
const TrashKey = "deleted"

// IsDeleted reports whether the session is soft-deleted.
func IsDeleted(sess *types.Session) bool {
	if sess == nil || sess.Metadata == nil {
		return false
	}
	v, _ := sess.Metadata[TrashKey].(bool)
	return v
}

// MarkDeleted archives a summary (if missing) and soft-deletes the session.
// Nothing is destroyed — /restore brings it back.
func MarkDeleted(store types.SessionStore, sess *types.Session) error {
	if store == nil || sess == nil {
		return fmt.Errorf("trash: missing store or session")
	}
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	if Get(sess) == "" {
		sess.Metadata[MetadataKey] = Generate(sess, sess.ModelID, sess.ProviderName, "")
	}
	sess.Metadata[TrashKey] = true
	return store.Update(sess)
}

// Restore clears the soft-delete mark.
func Restore(store types.SessionStore, sess *types.Session) error {
	if store == nil || sess == nil {
		return fmt.Errorf("trash: missing store or session")
	}
	if sess.Metadata == nil {
		return nil
	}
	if _, ok := sess.Metadata[TrashKey]; ok {
		delete(sess.Metadata, TrashKey)
	}
	return store.Update(sess)
}
