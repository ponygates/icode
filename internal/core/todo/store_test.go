package todo

// Tests for the in-memory todo Store backing the TodoWrite tool
// (internal/core/tool/todo.go) and the status bar callbacks
// (cmd/commands_callback.go, internal/server/handlers_workspace.go).

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewStoreIsEmpty(t *testing.T) {
	s := NewStore()
	if got := s.Get("nope"); got != nil {
		t.Errorf("Get on fresh store = %v, want nil", got)
	}
	if p, a, d, tot := s.Counts("nope"); p|a|d|tot != 0 {
		t.Errorf("Counts on fresh store = %d/%d/%d/%d, want 0/0/0/0", p, a, d, tot)
	}
	if got := s.Sessions(); len(got) != 0 {
		t.Errorf("Sessions on fresh store = %v, want empty", got)
	}
	if got := s.ActiveText("nope"); got != "" {
		t.Errorf("ActiveText on fresh store = %q, want empty", got)
	}
}

func TestReplaceFillsDefaults(t *testing.T) {
	s := NewStore()
	out := s.Replace("sess", []TodoItem{
		{Content: "first"}, // no ID, no status, no timestamps
		{Content: "second", Status: StatusCompleted},
	})
	if len(out) != 2 {
		t.Fatalf("Replace returned %d items, want 2", len(out))
	}
	if out[0].ID == "" || !strings.HasPrefix(out[0].ID, "t-") {
		t.Errorf("auto ID = %q, want t-prefixed", out[0].ID)
	}
	if out[1].ID == "" || out[1].ID == out[0].ID {
		t.Errorf("auto IDs must be unique per item: %q vs %q", out[0].ID, out[1].ID)
	}
	if out[0].Status != StatusPending {
		t.Errorf("empty status should default to pending, got %q", out[0].Status)
	}
	if out[1].Status != StatusCompleted {
		t.Errorf("explicit status must be kept, got %q", out[1].Status)
	}
	for i, it := range out {
		if it.CreatedAt.IsZero() || it.UpdatedAt.IsZero() {
			t.Errorf("item %d: timestamps not filled (%v / %v)", i, it.CreatedAt, it.UpdatedAt)
		}
	}

	// The stored list mirrors the returned one.
	back := s.Get("sess")
	if len(back) != 2 || back[0].Content != "first" || back[1].Content != "second" {
		t.Fatalf("Get after Replace = %v, want ordered [first second]", back)
	}
	if back[0].ID != out[0].ID {
		t.Errorf("stored ID %q differs from returned %q", back[0].ID, out[0].ID)
	}
}

func TestReplacePreservesExistingFields(t *testing.T) {
	s := NewStore()
	past := time.Now().Add(-time.Hour)
	out := s.Replace("sess", []TodoItem{{
		ID:         "stable-1",
		Content:    "keep me",
		Status:     StatusInProgress,
		ActiveForm: "Working",
		CreatedAt:  past,
	}})
	if out[0].ID != "stable-1" {
		t.Errorf("explicit ID overwritten: %q", out[0].ID)
	}
	if out[0].Status != StatusInProgress || out[0].ActiveForm != "Working" {
		t.Errorf("explicit fields altered: %+v", out[0])
	}
	if !out[0].CreatedAt.Equal(past) {
		t.Errorf("CreatedAt should be preserved, got %v want %v", out[0].CreatedAt, past)
	}
	if out[0].UpdatedAt.Before(past) {
		t.Errorf("UpdatedAt should be refreshed, got %v", out[0].UpdatedAt)
	}
}

func TestReplaceIsIdempotentSwapNotAppend(t *testing.T) {
	s := NewStore()
	s.Replace("sess", []TodoItem{{Content: "a"}, {Content: "b"}})
	s.Replace("sess", []TodoItem{{Content: "c"}})
	got := s.Get("sess")
	if len(got) != 1 || got[0].Content != "c" {
		t.Fatalf("Replace must swap the whole list, got %v", got)
	}
}

func TestReplaceEmptyAndNilClearList(t *testing.T) {
	s := NewStore()
	s.Replace("sess", []TodoItem{{Content: "x"}})
	for _, in := range [][]TodoItem{{}, nil} {
		s.Replace("sess", in)
		if got := s.Get("sess"); len(got) != 0 {
			t.Errorf("Replace(%v) then Get = %v, want empty", in, got)
		}
		if p, a, d, tot := s.Counts("sess"); p|a|d|tot != 0 {
			t.Errorf("Counts after empty replace = %d/%d/%d/%d, want zeros", p, a, d, tot)
		}
	}
}

func TestGetReturnsCopy(t *testing.T) {
	s := NewStore()
	s.Replace("sess", []TodoItem{{Content: "orig", Status: StatusPending}})
	got := s.Get("sess")
	got[0].Content = "MUTATED"
	if again := s.Get("sess"); again[0].Content != "orig" {
		t.Errorf("mutating Get result leaked into store: %v", again)
	}
}

func TestGetAfterCallerReusesInputSliceIsSafe(t *testing.T) {
	// TodoWrite builds a fresh slice per call, but the store snapshots it, so
	// a caller reusing the backing array must not affect the stored list.
	s := NewStore()
	items := []TodoItem{{Content: "one"}}
	s.Replace("sess", items)
	items[0].Content = "changed by caller"
	if got := s.Get("sess"); got[0].Content != "one" {
		t.Errorf("store must hold a snapshot, got %q", got[0].Content)
	}
}

func TestCounts(t *testing.T) {
	s := NewStore()
	s.Replace("sess", []TodoItem{
		{Content: "a", Status: StatusPending},
		{Content: "b", Status: StatusInProgress},
		{Content: "c", Status: StatusCompleted},
		{Content: "d", Status: StatusCompleted},
	})
	p, a, d, tot := s.Counts("sess")
	if p != 1 || a != 1 || d != 2 || tot != 4 {
		t.Errorf("Counts = %d/%d/%d/%d, want 1/1/2/4", p, a, d, tot)
	}

	// A status outside the enum (tool layer normalizes, but the store must not
	// lose the total) still counts toward total only.
	s.Replace("odd", []TodoItem{{Content: "x", Status: Status("weird")}})
	p, a, d, tot = s.Counts("odd")
	if p != 0 || a != 0 || d != 0 || tot != 1 {
		t.Errorf("Counts with unknown status = %d/%d/%d/%d, want 0/0/0/1", p, a, d, tot)
	}
}

func TestActiveText(t *testing.T) {
	s := NewStore()
	s.Replace("sess", []TodoItem{
		{Content: "Fix auth", ActiveForm: "Fixing auth", Status: StatusInProgress},
		{Content: "later", Status: StatusPending},
	})
	if got := s.ActiveText("sess"); got != "Fixing auth" {
		t.Errorf("ActiveText should prefer ActiveForm, got %q", got)
	}

	s.Replace("noform", []TodoItem{
		{Content: "pending first", Status: StatusPending},
		{Content: "Working on it", Status: StatusInProgress},
	})
	if got := s.ActiveText("noform"); got != "Working on it" {
		t.Errorf("ActiveText should fall back to Content, got %q", got)
	}

	// Only the first in-progress item is reported (UI shows one activity).
	s.Replace("multi", []TodoItem{
		{Content: "one", ActiveForm: "first active", Status: StatusInProgress},
		{Content: "two", ActiveForm: "second active", Status: StatusInProgress},
	})
	if got := s.ActiveText("multi"); got != "first active" {
		t.Errorf("ActiveText with two actives = %q, want the first", got)
	}

	s.Replace("idle", []TodoItem{{Content: "all done", Status: StatusCompleted}})
	if got := s.ActiveText("idle"); got != "" {
		t.Errorf("no in-progress item should give empty text, got %q", got)
	}
}

func TestClear(t *testing.T) {
	s := NewStore()
	s.Replace("keep", []TodoItem{{Content: "a"}})
	s.Replace("wipe", []TodoItem{{Content: "b"}})
	s.Clear("wipe")
	if got := s.Get("wipe"); got != nil {
		t.Errorf("Get after Clear = %v, want nil", got)
	}
	s.Clear("wipe") // clearing twice must not panic
	if got := s.Get("keep"); len(got) != 1 {
		t.Errorf("Clear must be session-scoped, keep = %v", got)
	}
	if sess := s.Sessions(); len(sess) != 1 || sess[0] != "keep" {
		t.Errorf("Sessions after Clear = %v, want [keep]", sess)
	}
}

func TestSessionsSorted(t *testing.T) {
	s := NewStore()
	for _, id := range []string{"charlie", "alpha", "bravo"} {
		s.Replace(id, []TodoItem{{Content: "x"}})
	}
	got := s.Sessions()
	want := []string{"alpha", "bravo", "charlie"}
	if len(got) != len(want) {
		t.Fatalf("Sessions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Sessions = %v, want sorted %v", got, want)
		}
	}
}

func TestSubscribeFiresOnReplace(t *testing.T) {
	s := NewStore()
	var calls []string
	s.Subscribe(func(sessionID string, items []TodoItem) {
		calls = append(calls, sessionID+":"+strings.Join(namesOf(items), ","))
	})
	s.Replace("s1", []TodoItem{{Content: "a"}, {Content: "b"}})
	if len(calls) != 1 || calls[0] != "s1:a,b" {
		t.Fatalf("callback calls = %q, want [s1:a,b]", calls)
	}
	// Callbacks registered before the Replace do not fire for earlier ones,
	// and a late registrant sees only subsequent changes.
	var second string
	s.Subscribe(func(_ string, items []TodoItem) { second = items[0].Content })
	s.Replace("s1", []TodoItem{{Content: "c"}})
	if second != "c" {
		t.Errorf("second subscriber got %q, want c", second)
	}
	if len(calls) != 2 {
		t.Errorf("first subscriber call count = %d, want 2", len(calls))
	}
}

// A subscription callback running synchronously after Replace must not
// deadlock when it reads the store (this is exactly what the TUI/SSE
// consumers do).
func TestSubscribeCallbackCanReadStore(t *testing.T) {
	s := NewStore()
	done := make(chan struct{}, 1)
	s.Subscribe(func(sessionID string, _ []TodoItem) {
		p, _, _, tot := s.Counts(sessionID)
		if p != 1 || tot != 1 {
			t.Errorf("Counts inside callback = %d/…/%d, want pending item", p, tot)
		}
		_ = s.Get(sessionID)
		_ = s.ActiveText(sessionID)
		done <- struct{}{}
	})
	s.Replace("s", []TodoItem{{Content: "x", Status: StatusPending}})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("callback deadlocked (Replace must fire subscribers after unlocking)")
	}
}

func TestDefaultSingletonRoundTrip(t *testing.T) {
	if Default == nil {
		t.Fatal("Default store must be initialised at package level")
	}
	const sess = "test-default-roundtrip"
	defer Default.Clear(sess)
	Default.Replace(sess, []TodoItem{{Content: "ping", Status: StatusInProgress, ActiveForm: "Pinging"}})
	if got := Default.ActiveText(sess); got != "Pinging" {
		t.Errorf("ActiveText via Default = %q", got)
	}
	if got := Default.Get(sess); len(got) != 1 || got[0].Content != "ping" {
		t.Errorf("Get via Default = %v", got)
	}
}

// Hammer one store from many goroutines across shared and private sessions.
// CGO is off on this machine so `go test -race` is unavailable; this test at
// least catches deadlocks (via the timeout below), panics and logical
// corruption (the final state must be readable and consistent).
func TestConcurrentAccess(t *testing.T) {
	s := NewStore()
	const workers = 8
	const iterations = 100

	var wg sync.WaitGroup
	deadline := time.After(20 * time.Second)
	finished := make(chan struct{})

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			own := "own-" + string(rune('a'+w))
			for i := 0; i < iterations; i++ {
				switch i % 4 {
				case 0:
					s.Replace("hot", []TodoItem{{Content: "shared", Status: StatusInProgress, ActiveForm: "Working"}})
				case 1:
					_ = s.Get("hot")
					_, _, _, _ = s.Counts("hot")
					_ = s.ActiveText("hot")
				case 2:
					s.Replace(own, []TodoItem{{Content: "mine"}})
					if got := s.Get(own); len(got) != 1 || got[0].Content != "mine" {
						t.Errorf("own-session read torn: %v", got)
						return
					}
				case 3:
					_ = s.Sessions()
					s.Subscribe(func(string, []TodoItem) {})
					s.Clear("stale-" + own)
				}
			}
		}(w)
	}

	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-deadline:
		t.Fatal("concurrent access deadlocked or hung")
	}

	s.Replace("final", []TodoItem{{Content: "written last"}})
	if got := s.Get("final"); len(got) != 1 || got[0].Content != "written last" {
		t.Errorf("store unusable after concurrency storm: %v", got)
	}
	// Every own-session worker should still have its last list.
	for w := 0; w < workers; w++ {
		own := "own-" + string(rune('a'+w))
		if got := s.Get(own); len(got) != 1 {
			t.Errorf("session %q lost, Get = %v", own, got)
		}
	}
}

func namesOf(items []TodoItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Content
	}
	return out
}
