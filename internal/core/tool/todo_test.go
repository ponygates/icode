package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/core/todo"
)

// TestTodoWriteReplaceCounts ensures the tool round-trips a list snapshot
// into the store and returns a summary the model can read next turn.
func TestTodoWriteReplaceCounts(t *testing.T) {
	store := todo.NewStore()
	tool := NewTodoWriteTool(store)
	ctx := WithSessionID(context.Background(), "sess-1")

	args := `{"todos":[
		{"content":"Plan refactor","status":"completed"},
		{"content":"Refactor auth","status":"in_progress","activeForm":"Refactoring auth"},
		{"content":"Update docs","status":"pending"},
		{"content":"Add tests","status":"pending"}
	]}`

	res, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("unsuccessful: %+v", res)
	}
	if !strings.Contains(res.Content, "1 in progress") {
		t.Fatalf("summary lacks in-progress count: %q", res.Content)
	}
	if !strings.Contains(res.Content, "Refactoring auth") {
		t.Fatalf("activeForm not rendered: %q", res.Content)
	}

	pending, active, done, total := store.Counts("sess-1")
	if pending != 2 || active != 1 || done != 1 || total != 4 {
		t.Fatalf("counts wrong: p=%d a=%d d=%d t=%d", pending, active, done, total)
	}
}

// TestTodoWriteIsolation verifies that separate sessions never share state.
func TestTodoWriteIsolation(t *testing.T) {
	store := todo.NewStore()
	tool := NewTodoWriteTool(store)

	ctxA := WithSessionID(context.Background(), "A")
	ctxB := WithSessionID(context.Background(), "B")

	tool.Execute(ctxA, `{"todos":[{"content":"A-only","status":"pending"}]}`)
	tool.Execute(ctxB, `{"todos":[{"content":"B-only","status":"in_progress"}]}`)

	if _, active, _, _ := store.Counts("A"); active != 0 {
		t.Fatal("session A should have zero in_progress items")
	}
	if _, active, _, _ := store.Counts("B"); active != 1 {
		t.Fatal("session B should have one in_progress item")
	}
}

// TestTodoWriteReplaceIsIdempotentReplace confirms that a second call with a
// smaller list removes the missing items (rather than diff-merging).
func TestTodoWriteReplaceIsIdempotentReplace(t *testing.T) {
	store := todo.NewStore()
	tool := NewTodoWriteTool(store)
	ctx := WithSessionID(context.Background(), "s")

	tool.Execute(ctx, `{"todos":[
		{"content":"one","status":"pending"},
		{"content":"two","status":"pending"}
	]}`)
	tool.Execute(ctx, `{"todos":[
		{"content":"one","status":"completed"}
	]}`)

	got := store.Get("s")
	if len(got) != 1 {
		t.Fatalf("expected 1 item after replace, got %d", len(got))
	}
	if got[0].Status != todo.StatusCompleted {
		t.Fatalf("status not updated: %+v", got[0])
	}
}
