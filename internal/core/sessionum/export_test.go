package sessionum

import (
	"encoding/json"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func TestExportImport_RoundTrip(t *testing.T) {
	store := mkStore(t)
	src := &types.Session{ID: "s1", Title: "原会话", ModelID: "m1", ProviderName: "p1"}
	if err := store.Create(src); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.AppendMessage(src.ID, types.Message{ID: "m1", Role: "user", Content: "你好"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.AppendMessage(src.ID, types.Message{ID: "m2", Role: "assistant", Content: "hi"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := store.Get(src.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	data, err := Export(got)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("export is not valid JSON")
	}

	imported, n, err := Import(store, data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported = %d, want 2", n)
	}
	if imported.ID == "" || imported.ID == src.ID {
		t.Fatalf("imported id = %q, want fresh", imported.ID)
	}
	msgs, _ := store.Get(imported.ID)
	if len(msgs.Messages) != 2 {
		t.Fatalf("imported messages = %d, want 2", len(msgs.Messages))
	}
	if msgs.Messages[0].Content != "你好" || msgs.Messages[1].Content != "hi" {
		t.Fatalf("imported order/content wrong: %+v", msgs.Messages)
	}
	if msgs.Messages[0].ID == "m1" || msgs.Messages[1].ID == "m2" {
		t.Fatalf("imported message ids must be regenerated")
	}
}

func TestImport_RejectsBadJSON(t *testing.T) {
	_, _, err := Import(mkStore(t), []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

func TestImport_DefaultTitle(t *testing.T) {
	imported, _, err := Import(mkStore(t), []byte(`{"messages":[]}`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported.Title != "Imported session" {
		t.Fatalf("title = %q", imported.Title)
	}
}
