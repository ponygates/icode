// Package session provides in-memory session management.
// Phase 2 upgrade: SQLite-backed storage via internal/db package.
package session

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ponygates/icode/internal/types"
)

// Store implements types.SessionStore with in-memory storage.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*types.Session
}

// NewStore creates an in-memory session store.
func NewStore() *Store {
	return &Store{
		sessions: make(map[string]*types.Session),
	}
}

func (s *Store) Create(sess *types.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess.ID == "" {
		sess.ID = uuid.New().String()
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	sess.UpdatedAt = time.Now()

	s.sessions[sess.ID] = sess
	return nil
}

func (s *Store) Get(id string) (*types.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return sess, nil
}

func (s *Store) List(limit, offset int) ([]types.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]types.Session, 0, len(s.sessions))
	i := 0
	for _, sess := range s.sessions {
		if i < offset {
			i++
			continue
		}
		result = append(result, *sess)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *Store) Update(sess *types.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sess.ID]; !ok {
		return fmt.Errorf("session %q not found", sess.ID)
	}
	sess.UpdatedAt = time.Now()
	s.sessions[sess.ID] = sess
	return nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, id)
	return nil
}

func (s *Store) AppendMessage(sessionID string, msg types.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %q not found", sessionID)
	}

	msg.Timestamp = time.Now()
	sess.Messages = append(sess.Messages, msg)
	sess.UpdatedAt = time.Now()
	return nil
}
