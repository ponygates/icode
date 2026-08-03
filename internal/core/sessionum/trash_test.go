package sessionum

import (
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func TestTrash_MarkRestoreRoundTrip(t *testing.T) {
	store := mkStore(t)
	sess := mkSession(t, store, "trash")
	if IsDeleted(sess) {
		t.Fatalf("fresh session should not be deleted")
	}
	sess.Messages = []types.Message{{Role: types.RoleUser, Content: "hi"}}
	if err := store.Update(sess); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := MarkDeleted(store, sess); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}

	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get after mark: %v", err)
	}
	if !IsDeleted(got) {
		t.Fatalf("session should be soft-deleted after MarkDeleted")
	}
	if Get(got) == "" {
		t.Fatalf("MarkDeleted should archive a summary before hiding the session")
	}

	if err := Restore(store, got); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got2, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}
	if IsDeleted(got2) {
		t.Fatalf("session should be restorable, still deleted after Restore")
	}
	if Get(got2) == "" {
		t.Fatalf("summary must survive the restore round-trip")
	}
}

func TestTrash_NilGuard(t *testing.T) {
	if IsDeleted(nil) {
		t.Fatalf("nil session must not report deleted")
	}
	store := mkStore(t)
	sess := mkSession(t, store, "nilguard")
	if err := MarkDeleted(nil, sess); err == nil {
		t.Fatalf("nil store should error")
	}
	if err := Restore(nil, sess); err == nil {
		t.Fatalf("nil store should error")
	}
	if err := Restore(nil, nil); err == nil {
		t.Fatalf("nil everything should error")
	}
}
