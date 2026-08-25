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

	"github.com/ponygates/icode/internal/scheduler"
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
		`CREATE TABLE IF NOT EXISTS session_stats (
			session_id TEXT PRIMARY KEY,
			tokens_saved INTEGER NOT NULL DEFAULT 0,
			cache_hit_tokens INTEGER NOT NULL DEFAULT 0,
			estimated_cost REAL NOT NULL DEFAULT 0,
			estimated_saved_cost REAL NOT NULL DEFAULT 0,
			cache_hit_rate REAL NOT NULL DEFAULT 0,
			day TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_stats_day ON session_stats(day)`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			session_ids TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS automations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			prompt TEXT NOT NULL DEFAULT '',
			schedule TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			last_run TEXT NOT NULL DEFAULT '',
			next_run TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS automation_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT '',
			output TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS agent_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			from_session TEXT NOT NULL,
			to_session TEXT NOT NULL,
			body TEXT NOT NULL,
			created_at TEXT NOT NULL,
			read_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_messages_to ON agent_messages(to_session, read_at)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_runs_task ON automation_runs(task_id, started_at)`,
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
	// CRITICAL: With SetMaxOpenConns(1), we cannot call loadMessages()
	// while the rows cursor is open — the cursor holds the only connection
	// and loadMessages() would block forever, deadlocking the entire store.
	// Fix: collect all sessions first, close the cursor, THEN load messages.
	rows, err := s.db.Query(`SELECT id, title, model_id, provider_name, metadata,
		total_input_tokens, total_output_tokens, total_cache_hits, created_at, updated_at
		FROM sessions ORDER BY updated_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var sessions []types.Session
	for rows.Next() {
		var sess types.Session
		var metaJSON, createdAt, updatedAt string
		if err := rows.Scan(&sess.ID, &sess.Title, &sess.ModelID, &sess.ProviderName, &metaJSON,
			&sess.TotalTokens.PromptTokens, &sess.TotalTokens.CompletionTokens,
			&sess.TotalTokens.CacheHitTokens, &createdAt, &updatedAt); err != nil {
			rows.Close()
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
	rows.Close() // Release the connection BEFORE loading messages!

	if err := rows.Err(); err != nil {
		return sessions, fmt.Errorf("iterate session rows: %w", err)
	}

	// NOTE: Intentionally do NOT load message bodies here. Returning full
	// histories for every session (up to `limit`) made the sessions list API
	// load potentially very large message payloads on startup and on every
	// list refresh — a genuine freeze with many/long sessions (the UI would
	// block waiting for the response, then have to parse/serialize it). The
	// desktop lazily loads a session's messages via GET /api/sessions/{id}
	// when that session is actually opened.
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

// UpdateMessage updates a single message's role/content in place. It is used
// by the desktop "shell" helper to persist a tool message after its result
// arrives, so the same edit shows up in the CLI and simpleui.
func (s *Store) UpdateMessage(sessionID string, msg types.Message) error {
	now := msg.Timestamp.Format(time.RFC3339)
	if msg.Timestamp.IsZero() {
		now = time.Now().UTC().Format(time.RFC3339)
	}

	toolCallsJSON, err := json.Marshal(msg.ToolCalls)
	if err != nil {
		return fmt.Errorf("marshal message tool calls: %w", err)
	}

	res, err := s.db.Exec(`UPDATE messages SET role=?, content=?, tool_calls=?, tool_id=?, timestamp=?,
		token_count=?, cache_hit=?, model=?, finish_reason=? WHERE id=? AND session_id=?`,
		string(msg.Role), msg.Content, string(toolCallsJSON), msg.ToolID, now,
		msg.Metadata.TokenCount, boolToInt(msg.Metadata.CacheHit), msg.Metadata.Model,
		msg.Metadata.FinishReason, msg.ID, sessionID)
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("message %q not found in session %q", msg.ID, sessionID)
	}
	return nil
}

// DeleteMessage removes a single message from a session (used by desktop
// regenerate to drop the stale assistant reply before re-sending).
func (s *Store) DeleteMessage(sessionID, msgID string) error {
	if _, err := s.db.Exec(`DELETE FROM messages WHERE id=? AND session_id=?`, msgID, sessionID); err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	return nil
}

// ClearMessages wipes every message of a session but keeps the session shell,
// so the cleared conversation is shared identically across all three UIs.
func (s *Store) ClearMessages(sessionID string) error {
	if _, err := s.db.Exec(`DELETE FROM messages WHERE session_id=?`, sessionID); err != nil {
		return fmt.Errorf("clear messages: %w", err)
	}
	return nil
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

// SessionStatSnapshot is a flattened, persistable view of one session's
// token-optimization stats. Keeping it as a plain struct (rather than the
// raw tokenopt.Stats JSON) lets the store SUM columns directly for the
// global dashboard without parsing JSON per row.
type SessionStatSnapshot struct {
	SessionID          string
	TokensSaved        int
	CacheHitTokens     int
	EstimatedCost      float64
	EstimatedSavedCost float64
	CacheHitRate       float64
}

// UpsertSessionStats persists (or replaces) one session's token stats so the
// global dashboard survives restarts and aggregates across sessions.
func (s *Store) UpsertSessionStats(snap SessionStatSnapshot) error {
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	_, err := s.db.Exec(`INSERT INTO session_stats
		(session_id, tokens_saved, cache_hit_tokens, estimated_cost, estimated_saved_cost, cache_hit_rate, day, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			tokens_saved = excluded.tokens_saved,
			cache_hit_tokens = excluded.cache_hit_tokens,
			estimated_cost = excluded.estimated_cost,
			estimated_saved_cost = excluded.estimated_saved_cost,
			cache_hit_rate = excluded.cache_hit_rate,
			day = excluded.day,
			updated_at = excluded.updated_at`,
		snap.SessionID, snap.TokensSaved, snap.CacheHitTokens,
		snap.EstimatedCost, snap.EstimatedSavedCost, snap.CacheHitRate, day, now.Format(time.RFC3339),
	)
	return err
}

// DayAgg is one day's slice of the token-savings trend.
type DayAgg struct {
	Day         string  `json:"day"`
	TokensSaved int     `json:"tokens_saved"`
	SavedCost   float64 `json:"saved_cost"`
}

// GlobalAgg is the cross-session aggregate shown on the global dashboard tab.
type GlobalAgg struct {
	Sessions         int      `json:"sessions"`
	TotalTokensSaved int      `json:"total_tokens_saved"`
	TotalCacheHits   int      `json:"total_cache_hit_tokens"`
	TotalCost        float64  `json:"total_cost"`
	TotalSavedCost   float64  `json:"total_saved_cost"`
	AvgCacheHitRate  float64  `json:"avg_cache_hit_rate"`
	Trend            []DayAgg `json:"trend"`
}

// GlobalStats aggregates all persisted session stats into a single summary
// plus a per-day savings trend for the dashboard chart.
func (s *Store) GlobalStats() (GlobalAgg, error) {
	var agg GlobalAgg
	row := s.db.QueryRow(`SELECT
		COUNT(*),
		COALESCE(SUM(tokens_saved), 0),
		COALESCE(SUM(cache_hit_tokens), 0),
		COALESCE(SUM(estimated_cost), 0),
		COALESCE(SUM(estimated_saved_cost), 0),
		COALESCE(AVG(cache_hit_rate), 0)
		FROM session_stats`)
	if err := row.Scan(&agg.Sessions, &agg.TotalTokensSaved, &agg.TotalCacheHits,
		&agg.TotalCost, &agg.TotalSavedCost, &agg.AvgCacheHitRate); err != nil {
		return agg, err
	}

	rows, err := s.db.Query(`SELECT day,
		COALESCE(SUM(tokens_saved), 0),
		COALESCE(SUM(estimated_saved_cost), 0)
		FROM session_stats
		GROUP BY day ORDER BY day ASC`)
	if err != nil {
		return agg, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DayAgg
		if err := rows.Scan(&d.Day, &d.TokensSaved, &d.SavedCost); err != nil {
			return agg, err
		}
		agg.Trend = append(agg.Trend, d)
	}
	return agg, rows.Err()
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

// ============================================================================
// Workspaces
// ============================================================================

// Workspace groups related sessions under a named, path-scoped container —
// the desktop analogue of an IDE project window. session_ids is a JSON array
// so the (ordered) membership survives restarts.
type Workspace struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	SessionIDs []string  `json:"session_ids"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *Store) CreateWorkspace(w Workspace) error {
	now := time.Now().UTC()
	if w.ID == "" {
		w.ID = fmt.Sprintf("ws_%x", now.UnixNano())
	}
	if w.SessionIDs == nil {
		w.SessionIDs = []string{}
	}
	ids, err := json.Marshal(w.SessionIDs)
	if err != nil {
		return fmt.Errorf("marshal session_ids: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO workspaces (id, name, path, session_ids, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Path, string(ids), now.Format(time.RFC3339), now.Format(time.RFC3339))
	return err
}

func (s *Store) ListWorkspaces() ([]Workspace, error) {
	rows, err := s.db.Query(`SELECT id, name, path, session_ids, created_at, updated_at
		FROM workspaces ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var w Workspace
		var idsJSON, createdAt, updatedAt string
		if err := rows.Scan(&w.ID, &w.Name, &w.Path, &idsJSON, &createdAt, &updatedAt); err != nil {
			return out, err
		}
		_ = json.Unmarshal([]byte(idsJSON), &w.SessionIDs)
		w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		w.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) GetWorkspace(id string) (*Workspace, error) {
	row := s.db.QueryRow(`SELECT id, name, path, session_ids, created_at, updated_at
		FROM workspaces WHERE id = ?`, id)
	var w Workspace
	var idsJSON, createdAt, updatedAt string
	if err := row.Scan(&w.ID, &w.Name, &w.Path, &idsJSON, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(idsJSON), &w.SessionIDs)
	w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	w.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &w, nil
}

func (s *Store) UpdateWorkspace(w Workspace) error {
	ids, err := json.Marshal(w.SessionIDs)
	if err != nil {
		return fmt.Errorf("marshal session_ids: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`UPDATE workspaces SET name = ?, path = ?, session_ids = ?, updated_at = ?
		WHERE id = ?`, w.Name, w.Path, string(ids), now, w.ID)
	return err
}

func (s *Store) DeleteWorkspace(id string) error {
	_, err := s.db.Exec(`DELETE FROM workspaces WHERE id = ?`, id)
	return err
}

// SetWorkspaceSessions replaces the ordered session membership of a workspace.
func (s *Store) SetWorkspaceSessions(id string, sessionIDs []string) error {
	ids, err := json.Marshal(sessionIDs)
	if err != nil {
		return fmt.Errorf("marshal session_ids: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`UPDATE workspaces SET session_ids = ?, updated_at = ? WHERE id = ?`,
		string(ids), now, id)
	return err
}

// AddSessionToWorkspace appends a single session to a workspace's membership
// without clobbering existing sessions. Used when a new session is created
// under the active workspace (deep session↔workspace binding). Dedupes so
// repeated calls are idempotent.
func (s *Store) AddSessionToWorkspace(id string, sessionID string) error {
	ws, err := s.GetWorkspace(id)
	if err != nil {
		return err
	}
	for _, sid := range ws.SessionIDs {
		if sid == sessionID {
			return nil // already a member — idempotent
		}
	}
	ws.SessionIDs = append(ws.SessionIDs, sessionID)
	ids, err := json.Marshal(ws.SessionIDs)
	if err != nil {
		return fmt.Errorf("marshal session_ids: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`UPDATE workspaces SET session_ids = ?, updated_at = ? WHERE id = ?`,
		string(ids), now, id)
	return err
}

// ============================================================================
// Automations (scheduled tasks) — WorkBuddy-style
// ============================================================================

// LoadAutomations returns all persisted automation tasks.
func (s *Store) LoadAutomations() ([]scheduler.Task, error) {
	rows, err := s.db.Query(`SELECT id, name, prompt, schedule, enabled, last_run, next_run, created_at, updated_at FROM automations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scheduler.Task
	for rows.Next() {
		var t scheduler.Task
		var enabled int
		var lastRun, nextRun, createdAt, updatedAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.Prompt, &t.Schedule, &enabled,
			&lastRun, &nextRun, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		t.Enabled = enabled != 0
		t.LastRun, _ = time.Parse(time.RFC3339, lastRun)
		t.NextRun, _ = time.Parse(time.RFC3339, nextRun)
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveAutomation inserts or replaces an automation task.
func (s *Store) SaveAutomation(t scheduler.Task) error {
	enabled := 0
	if t.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO automations
		(id, name, prompt, schedule, enabled, last_run, next_run, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Prompt, t.Schedule, enabled,
		rf3339(t.LastRun), rf3339(t.NextRun), rf3339(t.CreatedAt), rf3339(t.UpdatedAt))
	return err
}

// DeleteAutomation removes a task and its run history.
func (s *Store) DeleteAutomation(id string) error {
	if _, err := s.db.Exec(`DELETE FROM automation_runs WHERE task_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM automations WHERE id = ?`, id)
	return err
}

// AppendAutomationRun records one execution result.
func (s *Store) AppendAutomationRun(r scheduler.RunRecord) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO automation_runs
		(id, task_id, started_at, finished_at, status, output, error)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TaskID, rf3339(r.StartedAt), rf3339(r.FinishedAt),
		r.Status, r.Output, r.Error)
	return err
}

// ListAutomationRuns returns the most recent runs for a task.
func (s *Store) ListAutomationRuns(taskID string, limit int) ([]scheduler.RunRecord, error) {
	rows, err := s.db.Query(`SELECT id, task_id, started_at, finished_at, status, output, error
		FROM automation_runs WHERE task_id = ? ORDER BY started_at DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scheduler.RunRecord
	for rows.Next() {
		var r scheduler.RunRecord
		var startedAt, finishedAt string
		if err := rows.Scan(&r.ID, &r.TaskID, &startedAt, &finishedAt,
			&r.Status, &r.Output, &r.Error); err != nil {
			return nil, err
		}
		r.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
		r.FinishedAt, _ = time.Parse(time.RFC3339, finishedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

// rf3339 renders a time as RFC3339 (empty for zero time), matching the rest
// of the store's serialization.
func rf3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ============================================================================
// Cross-session agent messaging (Claude Code SendMessage parity)
// ============================================================================

// SendAgentMessage stores one message addressed to another session.
func (s *Store) SendAgentMessage(fromID, toID, body string) error {
	if strings.TrimSpace(fromID) == "" || strings.TrimSpace(toID) == "" {
		return fmt.Errorf("agent message needs from and to")
	}
	_, err := s.db.Exec(`INSERT INTO agent_messages (from_session, to_session, body, created_at)
		VALUES (?, ?, ?, ?)`, fromID, toID, body, rf3339(time.Now()))
	return err
}

// AgentInbox returns messages addressed to sessionID, newest first. When
// unreadOnly is set only un-read ones are returned (and marked read).
func (s *Store) AgentInbox(sessionID string, limit int, unreadOnly bool) ([]types.AgentMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT id, from_session, to_session, body, created_at, read_at
		FROM agent_messages WHERE to_session = ?`
	if unreadOnly {
		q += ` AND read_at = ''`
	}
	q += ` ORDER BY id DESC LIMIT ?`
	rows, err := s.db.Query(q, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.AgentMessage
	for rows.Next() {
		var m types.AgentMessage
		var created, readAt string
		if err := rows.Scan(&m.ID, &m.FromID, &m.ToID, &m.Body, &created, &readAt); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, created)
		if readAt != "" {
			m.ReadAt, _ = time.Parse(time.RFC3339, readAt)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkAgentMessagesRead stamps all of a session's unread messages as read.
func (s *Store) MarkAgentMessagesRead(sessionID string) error {
	_, err := s.db.Exec(`UPDATE agent_messages SET read_at = ?
		WHERE to_session = ? AND read_at = ''`, rf3339(time.Now()), sessionID)
	return err
}
