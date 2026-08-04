package sessionum

import (
	"fmt"
	"time"

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

// TrashTTLDays is how long a soft-deleted session lingers in the trash before
// it is hard-purged (restore window / accidental-deletion safety net).
const TrashTTLDays = 30

// listFiltered gathers up to limit sessions matching wantDeleted, paging past
// the store's first pages so deleted sessions cannot crowd out live ones (and
// vice versa) when the trash grows large. Both in-memory (map iteration) and
// SQLite stores honour offset.
func listFiltered(store types.SessionStore, limit int, wantDeleted bool) ([]types.Session, error) {
	if store == nil {
		return nil, fmt.Errorf("trash: missing store")
	}
	const page = 500
	var out []types.Session
	for offset := 0; offset < 10000 && (limit <= 0 || len(out) < limit); offset += page {
		batch, err := store.List(page, offset)
		if err != nil {
			return nil, err
		}
		for i := range batch {
			if IsDeleted(&batch[i]) != wantDeleted {
				continue
			}
			out = append(out, batch[i])
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
		}
		if len(batch) < page {
			break
		}
	}
	return out, nil
}

// ListNonDeleted returns up to limit sessions that are not soft-deleted.
func ListNonDeleted(store types.SessionStore, limit int) ([]types.Session, error) {
	return listFiltered(store, limit, false)
}

// ListDeleted returns up to limit soft-deleted sessions.
func ListDeleted(store types.SessionStore, limit int) ([]types.Session, error) {
	return listFiltered(store, limit, true)
}

// listAllTrashed pulls every soft-deleted session from the store. The store's
// List has no filtering, so we fetch a generous cap — purging is rare and
// bounded, unlike session listing which is filtered server-side.
func listAllTrashed(store types.SessionStore) ([]*types.Session, error) {
	sessions, err := ListDeleted(store, 5000)
	if err != nil {
		return nil, err
	}
	out := make([]*types.Session, 0, len(sessions))
	for i := range sessions {
		out = append(out, &sessions[i])
	}
	return out, nil
}

// PurgeExpiredTrash hard-deletes soft-deleted sessions older than ttlDays
// (default TrashTTLDays). Called at server start so the trash never grows
// unbounded.
func PurgeExpiredTrash(store types.SessionStore, ttlDays int) (removed int, err error) {
	if store == nil {
		return 0, fmt.Errorf("trash: missing store")
	}
	if ttlDays <= 0 {
		ttlDays = TrashTTLDays
	}
	cutoff := time.Now().AddDate(0, 0, -ttlDays)
	list, err := listAllTrashed(store)
	if err != nil {
		return 0, err
	}
	for _, s := range list {
		if s.UpdatedAt.Before(cutoff) {
			if err := store.Delete(s.ID); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

// PurgeAllTrash hard-deletes every soft-deleted session (the sidebar's
// "empty trash" action). It is destructive and irreversible.
func PurgeAllTrash(store types.SessionStore) (removed int, err error) {
	if store == nil {
		return 0, fmt.Errorf("trash: missing store")
	}
	list, err := listAllTrashed(store)
	if err != nil {
		return 0, err
	}
	for _, s := range list {
		if err := store.Delete(s.ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
