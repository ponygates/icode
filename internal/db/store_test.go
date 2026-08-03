package db

import (
	"testing"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// newTestStore opens an in-memory SQLite store (no file on disk) for use
// across the table-driven tests below. Each test gets a fresh DB.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStore_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	sess := &types.Session{
		ID:           "s1",
		Title:        "hello",
		ModelID:      "deepseek/deepseek-chat",
		ProviderName: "deepseek",
		Metadata:     map[string]any{"cwd": "/tmp"},
	}
	if err := s.Create(sess); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get("s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "hello" {
		t.Errorf("title = %q, want hello", got.Title)
	}
	if got.ModelID != "deepseek/deepseek-chat" {
		t.Errorf("model_id = %q", got.ModelID)
	}
	if got.ProviderName != "deepseek" {
		t.Errorf("provider = %q", got.ProviderName)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}
}

func TestStore_CreateAutoID(t *testing.T) {
	s := newTestStore(t)
	sess := &types.Session{Title: "auto"}
	if err := s.Create(sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.ID == "" {
		t.Error("Create should populate ID when empty")
	}
}

func TestStore_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get("does-not-exist"); err == nil {
		t.Error("Get on missing id should error")
	}
}

func TestStore_List(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		if err := s.Create(&types.Session{
			ID:           string(rune('a' + i)),
			Title:        "s",
			ModelID:      "m",
			ProviderName: "p",
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		// Spread created_at so ordering is deterministic (List orders by
		// created_at DESC).
		time.Sleep(2 * time.Millisecond)
	}

	all, err := s.List(100, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("List returned %d sessions, want 5", len(all))
	}
	// List returns metadata only — messages must not be loaded.
	if len(all[0].Messages) != 0 {
		t.Errorf("List should return metadata only, got %d messages", len(all[0].Messages))
	}

	// Pagination
	page, err := s.List(2, 0)
	if err != nil {
		t.Fatalf("List paged: %v", err)
	}
	if len(page) != 2 {
		t.Errorf("List(limit=2) returned %d, want 2", len(page))
	}
}

func TestStore_Update(t *testing.T) {
	s := newTestStore(t)
	sess := &types.Session{ID: "u1", Title: "old", ModelID: "m1", ProviderName: "p"}
	if err := s.Create(sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Title = "new"
	sess.ModelID = "m2"
	if err := s.Update(sess); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get("u1")
	if got.Title != "new" {
		t.Errorf("title after update = %q", got.Title)
	}
	if got.ModelID != "m2" {
		t.Errorf("model_id after update = %q", got.ModelID)
	}
}

func TestStore_Delete(t *testing.T) {
	s := newTestStore(t)
	s.Create(&types.Session{ID: "d1", Title: "x"})
	if err := s.Delete("d1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("d1"); err == nil {
		t.Error("Get after Delete should fail")
	}
	// Deleting a missing id should not error (idempotent).
	if err := s.Delete("d1"); err != nil {
		t.Errorf("Delete missing id should be idempotent, got: %v", err)
	}
}

func TestStore_AppendMessageAndLoad(t *testing.T) {
	s := newTestStore(t)
	s.Create(&types.Session{ID: "m1", Title: "msgs"})
	if err := s.AppendMessage("m1", types.Message{
		ID:        "msg1",
		Role:      types.RoleUser,
		Content:   "hi",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("m1", types.Message{
		ID:        "msg2",
		Role:      types.RoleAssistant,
		Content:   "hello back",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("AppendMessage 2: %v", err)
	}

	got, err := s.Get("m1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got.Messages))
	}
	if got.Messages[0].Content != "hi" {
		t.Errorf("msg[0].Content = %q", got.Messages[0].Content)
	}
	if got.Messages[1].Content != "hello back" {
		t.Errorf("msg[1].Content = %q", got.Messages[1].Content)
	}
}

func TestStore_SearchMessages(t *testing.T) {
	s := newTestStore(t)
	s.Create(&types.Session{ID: "se1", Title: "search"})
	s.AppendMessage("se1", types.Message{ID: "x", Role: types.RoleUser, Content: "how do I test sqlite", Timestamp: time.Now()})
	s.AppendMessage("se1", types.Message{ID: "y", Role: types.RoleAssistant, Content: "with go test", Timestamp: time.Now()})

	results, err := s.SearchMessages("sqlite", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) == 0 {
		t.Error("SearchMessages returned no results for a term that exists")
	}
}

func TestStore_ConfigKV(t *testing.T) {
	s := newTestStore(t)
	if _, ok := s.GetConfig("missing"); ok {
		t.Error("GetConfig on missing key should return ok=false")
	}
	if err := s.SetConfig("theme", "dark"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	got, ok := s.GetConfig("theme")
	if !ok {
		t.Fatal("GetConfig after SetConfig should return ok=true")
	}
	if got != "dark" {
		t.Errorf("GetConfig = %q, want dark", got)
	}
	// Overwrite
	s.SetConfig("theme", "light")
	got, _ = s.GetConfig("theme")
	if got != "light" {
		t.Errorf("overwrite: GetConfig = %q, want light", got)
	}
}

func TestStore_MigrateIdempotent(t *testing.T) {
	// Running migrate twice (as New would on a second open of the same file)
	// must not error — migrations use IF NOT EXISTS.
	s := newTestStore(t)
	if err := s.migrate(); err != nil {
		t.Errorf("migrate on already-migrated DB should be idempotent: %v", err)
	}
}
