package session

import (
	"testing"
	"time"

	"github.com/ponygates/icode/internal/types"
)

func TestNewStore(t *testing.T) {
	s := NewStore()
	if s == nil {
		t.Fatal("NewStore should not return nil")
	}
	if len(s.sessions) != 0 {
		t.Errorf("new store should have 0 sessions, got %d", len(s.sessions))
	}
}

func TestCreate(t *testing.T) {
	s := NewStore()
	sess := &types.Session{
		ModelID:      "test-model",
		ProviderName: "test-provider",
	}

	err := s.Create(sess)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if sess.ID == "" {
		t.Error("session ID should be auto-generated")
	}
	if sess.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
	if sess.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}
}

func TestCreateWithCustomID(t *testing.T) {
	s := NewStore()
	sess := &types.Session{
		ID:           "custom-id",
		ModelID:      "test-model",
		ProviderName: "test-provider",
	}

	err := s.Create(sess)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if sess.ID != "custom-id" {
		t.Errorf("expected ID 'custom-id', got %q", sess.ID)
	}
}

func TestGet(t *testing.T) {
	s := NewStore()
	sess := &types.Session{
		ModelID:      "test-model",
		ProviderName: "test-provider",
	}
	s.Create(sess)

	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.ID != sess.ID {
		t.Errorf("expected ID %q, got %q", sess.ID, got.ID)
	}
	if got.ModelID != "test-model" {
		t.Errorf("expected ModelID 'test-model', got %q", got.ModelID)
	}
}

func TestGetNotFound(t *testing.T) {
	s := NewStore()
	_, err := s.Get("non-existent")
	if err == nil {
		t.Fatal("expected error for non-existent session, got nil")
	}
}

func TestDelete(t *testing.T) {
	s := NewStore()
	sess := &types.Session{ModelID: "test", ProviderName: "test"}
	s.Create(sess)

	err := s.Delete(sess.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = s.Get(sess.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestUpdate(t *testing.T) {
	s := NewStore()
	sess := &types.Session{
		ModelID:      "old-model",
		ProviderName: "test",
	}
	s.Create(sess)

	// Update
	sess.ModelID = "new-model"
	err := s.Update(sess)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, _ := s.Get(sess.ID)
	if got.ModelID != "new-model" {
		t.Errorf("expected updated ModelID 'new-model', got %q", got.ModelID)
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := NewStore()
	err := s.Update(&types.Session{ID: "non-existent"})
	if err == nil {
		t.Fatal("expected error for update of non-existent session, got nil")
	}
}

func TestList(t *testing.T) {
	s := NewStore()
	sessions := []*types.Session{
		{ModelID: "model-a", ProviderName: "test"},
		{ModelID: "model-b", ProviderName: "test"},
		{ModelID: "model-c", ProviderName: "test"},
	}
	for _, sess := range sessions {
		s.Create(sess)
	}

	// All sessions
	all, err := s.List(0, 0)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(all))
	}

	// Limited
	limited, _ := s.List(1, 0)
	if len(limited) != 1 {
		t.Errorf("expected 1 session with limit=1, got %d", len(limited))
	}

	// Offset
	offset, _ := s.List(10, 10)
	if len(offset) != 0 {
		t.Errorf("expected 0 sessions with offset past end, got %d", len(offset))
	}
}

func TestAppendMessage(t *testing.T) {
	s := NewStore()
	sess := &types.Session{
		ModelID:      "test",
		ProviderName: "test",
	}
	s.Create(sess)

	msg := types.Message{
		Role:    types.RoleUser,
		Content: "hello",
	}

	err := s.AppendMessage(sess.ID, msg)
	if err != nil {
		t.Fatalf("AppendMessage failed: %v", err)
	}

	got, _ := s.Get(sess.ID)
	if len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got.Messages))
	}
	if got.Messages[0].Content != "hello" {
		t.Errorf("expected content 'hello', got %q", got.Messages[0].Content)
	}
	if got.Messages[0].Role != types.RoleUser {
		t.Errorf("expected role 'user', got %q", got.Messages[0].Role)
	}
	if got.Messages[0].Timestamp.IsZero() {
		t.Error("message Timestamp should be set")
	}
}

func TestAppendMessageMultiple(t *testing.T) {
	s := NewStore()
	sess := &types.Session{ModelID: "test", ProviderName: "test"}
	s.Create(sess)

	s.AppendMessage(sess.ID, types.Message{Role: types.RoleUser, Content: "first"})
	s.AppendMessage(sess.ID, types.Message{Role: types.RoleAssistant, Content: "response"})
	s.AppendMessage(sess.ID, types.Message{Role: types.RoleUser, Content: "second"})

	got, _ := s.Get(sess.ID)
	if len(got.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got.Messages))
	}
	if got.Messages[0].Content != "first" {
		t.Errorf("msg[0] expected 'first', got %q", got.Messages[0].Content)
	}
	if got.Messages[1].Role != types.RoleAssistant {
		t.Errorf("msg[1] expected assistant role, got %q", got.Messages[1].Role)
	}
}

func TestAppendMessageNotFound(t *testing.T) {
	s := NewStore()
	err := s.AppendMessage("non-existent", types.Message{
		Role:    types.RoleUser,
		Content: "test",
	})
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
}

func TestUpdateUpdatesTimestamp(t *testing.T) {
	s := NewStore()
	sess := &types.Session{ModelID: "test", ProviderName: "test"}
	s.Create(sess)

	originalTime := sess.UpdatedAt

	// Small delay to ensure time changes
	time.Sleep(time.Millisecond)

	sess.ModelID = "updated"
	s.Update(sess)

	if !sess.UpdatedAt.After(originalTime) {
		t.Error("UpdatedAt should be updated on Update")
	}
}

func TestCreatePreservesCreatedAt(t *testing.T) {
	s := NewStore()
	fixedTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sess := &types.Session{
		ModelID:      "test",
		ProviderName: "test",
		CreatedAt:    fixedTime,
	}
	s.Create(sess)

	if !sess.CreatedAt.Equal(fixedTime) {
		t.Errorf("CreatedAt should be preserved, got %v", sess.CreatedAt)
	}
}
