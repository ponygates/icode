// Package db provides SQLite-backed persistent storage for sessions, messages, and config.
// Uses a pure-Go SQLite driver (modernc.org/sqlite) to avoid CGO dependencies.
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ponygates/icode/internal/types"
)

// Store implements types.SessionStore with SQLite persistence.
type Store struct {
	db *sql.DB
}

// Config configures the database connection.
type Config struct {
	// Path to the SQLite database file. If empty, uses ~/.icode/icode.db
	Path string
}

// New creates a new SQLite-backed store.
func New(cfg Config) (*Store, error) {
	if cfg.Path == "" {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".icode")
		os.MkdirAll(dir, 0755)
		cfg.Path = filepath.Join(dir, "icode.db")
	}

	db, err := sql.Open("sqlite", cfg.Path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// For in-memory test mode, use file::memory:?cache=shared
	return store, nil
}

// Close shuts down the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ============================================================================
// Migration
// ============================================================================

func (s *Store) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			model_id TEXT NOT NULL DEFAULT '',
			provider_name TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '{}',
			total_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_cache_hits INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			tool_calls TEXT NOT NULL DEFAULT '[]',
			tool_id TEXT NOT NULL DEFAULT '',
			timestamp TEXT NOT NULL,
			token_count INTEGER NOT NULL DEFAULT 0,
			cache_hit INTEGER NOT NULL DEFAULT 0,
			model TEXT NOT NULL DEFAULT '',
			finish_reason TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, timestamp)`,
		`CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS model_cache (
			provider TEXT NOT NULL,
			model_id TEXT NOT NULL,
			model_data TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (provider, model_id)
		)`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
	}

	for i, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	return nil
}

// ============================================================================
// Session CRUD
// ============================================================================

func (s *Store) Create(sess *types.Session) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if sess.ID == "" {
		sess.ID = fmt.Sprintf("%x", time.Now().UnixNano())
	}
	sess.CreatedAt = time.Now()
	sess.UpdatedAt = time.Now()

	metaJSON, err := json.Marshal(sess.Metadata)
	if err != nil {
		return fmt.Errorf("marshal session metadata: %w", err)
	}

	_, err = s.db.Exec(`INSERT INTO sessions
		(id, title, model_id, provider_name, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.Title, sess.ModelID, sess.ProviderName, string(metaJSON), now, now,
	)
	return err
}

func (s *Store) Get(id string) (*types.Session, error) {
	row := s.db.QueryRow(`SELECT id, title, model_id, provider_name, metadata,
		total_input_tokens, total_output_tokens, total_cache_hits, created_at, updated_at
		FROM sessions WHERE id = ?`, id)

	sess := &types.Session{}
	var metaJSON string
	var createdAt, updatedAt string
	err := row.Scan(&sess.ID, &sess.Title, &sess.ModelID, &sess.ProviderName, &metaJSON,
		&sess.TotalTokens.PromptTokens, &sess.TotalTokens.CompletionTokens,
		&sess.TotalTokens.CacheHitTokens, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get session %q: %w", id, err)
	}

	sess.TotalTokens.TotalTokens = sess.TotalTokens.PromptTokens + sess.TotalTokens.CompletionTokens
	json.Unmarshal([]byte(metaJSON), &sess.Metadata)
	sess.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		log.Printf("warning: failed to parse created_at %q: %v", createdAt, err)
	}
	sess.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		log.Printf("warning: failed to parse updated_at %q: %v", updatedAt, err)
	}

	// Load messages
	messages, err := s.loadMessages(id)
	if err != nil {
		return nil, fmt.Errorf("load messages for session %q: %w", id, err)
	}
	sess.Messages = messages

	return sess, nil
}

func (s *Store) List(limit, offset int) ([]types.Session, error) {
	rows, err := s.db.Query(`SELECT id, title, model_id, provider_name, metadata,
		total_input_tokens, total_output_tokens, total_cache_hits, created_at, updated_at
		FROM sessions ORDER BY updated_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []types.Session
	for rows.Next() {
		var sess types.Session
		var metaJSON, createdAt, updatedAt string
		if err := rows.Scan(&sess.ID, &sess.Title, &sess.ModelID, &sess.ProviderName, &metaJSON,
			&sess.TotalTokens.PromptTokens, &sess.TotalTokens.CompletionTokens,
			&sess.TotalTokens.CacheHitTokens, &createdAt, &updatedAt); err != nil {
			return sessions, fmt.Errorf("scan session row: %w", err)
		}

		sess.TotalTokens.TotalTokens = sess.TotalTokens.PromptTokens + sess.TotalTokens.CompletionTokens
		json.Unmarshal([]byte(metaJSON), &sess.Metadata)
		sess.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			log.Printf("warning: failed to parse created_at %q: %v", createdAt, err)
		}
		sess.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			log.Printf("warning: failed to parse updated_at %q: %v", updatedAt, err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return sessions, fmt.Errorf("iterate session rows: %w", err)
	}

	return sessions, nil
}

func (s *Store) Update(sess *types.Session) error {
	now := time.Now().UTC().Format(time.RFC3339)
	metaJSON, err := json.Marshal(sess.Metadata)
	if err != nil {
		return fmt.Errorf("marshal session metadata: %w", err)
	}

	_, err = s.db.Exec(`UPDATE sessions SET
		title = ?, model_id = ?, provider_name = ?, metadata = ?,
		total_input_tokens = ?, total_output_tokens = ?, total_cache_hits = ?,
		updated_at = ?
		WHERE id = ?`,
		sess.Title, sess.ModelID, sess.ProviderName, string(metaJSON),
		sess.TotalTokens.PromptTokens, sess.TotalTokens.CompletionTokens,
		sess.TotalTokens.CacheHitTokens, now, sess.ID,
	)
	return err
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// ============================================================================
// Message operations
// ============================================================================

func (s *Store) AppendMessage(sessionID string, msg types.Message) error {
	now := msg.Timestamp.Format(time.RFC3339)
	if msg.Timestamp.IsZero() {
		now = time.Now().UTC().Format(time.RFC3339)
	}

	toolCallsJSON, err := json.Marshal(msg.ToolCalls)
	if err != nil {
		return fmt.Errorf("marshal message tool calls: %w", err)
	}

	_, err = s.db.Exec(`INSERT INTO messages
		(id, session_id, role, content, tool_calls, tool_id, timestamp,
			token_count, cache_hit, model, finish_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, sessionID, string(msg.Role), msg.Content, string(toolCallsJSON),
		msg.ToolID, now, msg.Metadata.TokenCount,
		boolToInt(msg.Metadata.CacheHit), msg.Metadata.Model, msg.Metadata.FinishReason,
	)
	return err
}

func (s *Store) loadMessages(sessionID string) ([]types.Message, error) {
	rows, err := s.db.Query(`SELECT id, role, content, tool_calls, tool_id, timestamp,
		token_count, cache_hit, model, finish_reason
		FROM messages WHERE session_id = ? ORDER BY timestamp ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []types.Message
	for rows.Next() {
		var msg types.Message
		var tcJSON, ts string
		var cacheHit int
		if err := rows.Scan(&msg.ID, &msg.Role, &msg.Content, &tcJSON, &msg.ToolID, &ts,
			&msg.Metadata.TokenCount, &cacheHit, &msg.Metadata.Model, &msg.Metadata.FinishReason); err != nil {
			return messages, fmt.Errorf("scan message row: %w", err)
		}

		msg.Metadata.CacheHit = cacheHit != 0
		msg.Timestamp, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			log.Printf("warning: failed to parse message timestamp %q: %v", ts, err)
		}
		json.Unmarshal([]byte(tcJSON), &msg.ToolCalls)
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate message rows: %w", err)
	}

	return messages, nil
}

// SearchMessages searches message content across all sessions using LIKE.
// Results are ordered by most recent first, limited to `limit` rows.
func (s *Store) SearchMessages(query string, limit int) ([]types.SearchResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.db.Query(`SELECT m.id, m.session_id, COALESCE(s.title, ''), m.role, m.content, m.timestamp
		FROM messages m
		LEFT JOIN sessions s ON m.session_id = s.id
		WHERE m.content LIKE ?
		ORDER BY m.timestamp DESC
		LIMIT ?`, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	var results []types.SearchResult
	for rows.Next() {
		var r types.SearchResult
		var ts string
		if err := rows.Scan(&r.MessageID, &r.SessionID, &r.SessionTitle,
			&r.Role, &r.Content, &ts); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		r.Timestamp, _ = time.Parse(time.RFC3339, ts)
		// Find first match position (simple search)
		lower := strings.ToLower(r.Content)
		qLower := strings.ToLower(query)
		r.MatchPos = strings.Index(lower, qLower)
		results = append(results, r)
	}
	return results, rows.Err()
}

// ============================================================================
// Token statistics
// ============================================================================

// TotalTokens returns aggregate token usage across all sessions.
func (s *Store) TotalTokens() (types.TokenUsage, error) {
	row := s.db.QueryRow(`SELECT
		COALESCE(SUM(total_input_tokens), 0),
		COALESCE(SUM(total_output_tokens), 0),
		COALESCE(SUM(total_cache_hits), 0)
		FROM sessions`)

	var usage types.TokenUsage
	err := row.Scan(&usage.PromptTokens, &usage.CompletionTokens, &usage.CacheHitTokens)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage, err
}

// ============================================================================
// Config storage
// ============================================================================

func (s *Store) GetConfig(key string) (string, bool) {
	row := s.db.QueryRow(`SELECT value FROM config WHERE key = ?`, key)
	var value string
	if err := row.Scan(&value); err != nil {
		return "", false
	}
	return value, true
}

func (s *Store) SetConfig(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO config (key, value, updated_at)
		VALUES (?, ?, ?)`, key, value, now)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
