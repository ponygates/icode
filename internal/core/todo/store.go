// Package todo provides the in-memory TodoStore backing the TodoWrite tool.
//
// Design mirrors Claude Code's TodoWrite: each session owns an ordered list
// of items with status (pending/in_progress/completed). The tool takes the
// FULL current list on each call (idempotent replace) rather than diffing,
// which keeps the model's mental model simple.
package todo

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Status enumerates the allowed values for TodoItem.Status.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
)

// TodoItem is one entry in the todo list.
type TodoItem struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Status     Status    `json:"status"`
	ActiveForm string    `json:"activeForm,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Store is a concurrent map from session ID to that session's todo list.
// Kept in memory only — a todo list is a within-session scratchpad and does
// not need to survive process restarts (matching Claude Code's behaviour).
type Store struct {
	mu     sync.RWMutex
	lists  map[string][]TodoItem
	subCbs []func(sessionID string, items []TodoItem)
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{lists: make(map[string][]TodoItem)}
}

// Replace atomically swaps the list for a session. This is what TodoWrite
// invokes when the model produces a new snapshot.
func (s *Store) Replace(sessionID string, items []TodoItem) []TodoItem {
	now := time.Now()
	for i := range items {
		if items[i].ID == "" {
			items[i].ID = fmt.Sprintf("t-%d-%d", now.UnixNano(), i)
		}
		if items[i].CreatedAt.IsZero() {
			items[i].CreatedAt = now
		}
		items[i].UpdatedAt = now
		if items[i].Status == "" {
			items[i].Status = StatusPending
		}
	}

	s.mu.Lock()
	s.lists[sessionID] = append([]TodoItem(nil), items...)
	cbs := append([]func(string, []TodoItem){}, s.subCbs...)
	s.mu.Unlock()

	for _, cb := range cbs {
		cb(sessionID, items)
	}
	return items
}

// Get returns a copy of the current list for a session.
func (s *Store) Get(sessionID string) []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.lists[sessionID]
	if len(src) == 0 {
		return nil
	}
	out := make([]TodoItem, len(src))
	copy(out, src)
	return out
}

// Counts returns (pending, in_progress, completed, total) for status pills.
func (s *Store) Counts(sessionID string) (pending, active, done, total int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range s.lists[sessionID] {
		total++
		switch it.Status {
		case StatusPending:
			pending++
		case StatusInProgress:
			active++
		case StatusCompleted:
			done++
		}
	}
	return
}

// Clear wipes a session's list (used by /clear).
func (s *Store) Clear(sessionID string) {
	s.mu.Lock()
	delete(s.lists, sessionID)
	s.mu.Unlock()
}

// Sessions returns the set of session IDs that have any todos, sorted for
// deterministic listing in admin/dev UIs.
func (s *Store) Sessions() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.lists))
	for k := range s.lists {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Subscribe registers a callback fired whenever a session's list changes.
// The callback runs synchronously after Replace so downstream UIs (TUI status
// bar, desktop SSE broadcast) receive the update without polling.
func (s *Store) Subscribe(cb func(sessionID string, items []TodoItem)) {
	s.mu.Lock()
	s.subCbs = append(s.subCbs, cb)
	s.mu.Unlock()
}

// Default is the process-wide store used by the TodoWrite tool. Keeping this
// as a singleton avoids threading a store handle through every tool factory
// while still letting tests use isolated stores if they want.
var Default = NewStore()
