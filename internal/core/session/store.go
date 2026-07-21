// Package session provides in-memory session management.
// Phase 2 upgrade: SQLite-backed storage via internal/db package.
package session

import (
	"fmt"
	"strings"
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

// SearchMessages performs a simple case-insensitive content search across all sessions.
func (s *Store) SearchMessages(query string, limit int) ([]types.SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	qLower := strings.ToLower(query)
	var results []types.SearchResult

	for _, sess := range s.sessions {
		for _, msg := range sess.Messages {
			lower := strings.ToLower(msg.Content)
			if pos := strings.Index(lower, qLower); pos >= 0 {
				title := ""
				if sess != nil {
					title = sess.Title
				}
				results = append(results, types.SearchResult{
					SessionID:    sess.ID,
					SessionTitle: title,
					MessageID:   msg.ID,
					Role:        msg.Role,
					Content:     msg.Content,
					Timestamp:   msg.Timestamp,
					MatchPos:    pos,
				})
			}
		}
	}

	// Sort by timestamp descending (most recent first)
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Timestamp.After(results[i].Timestamp) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}
