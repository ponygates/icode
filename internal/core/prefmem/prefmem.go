// Package prefmem implements a lightweight preference memory layer.
//
// Inspired by the "memory" chapter of the Claude Code engineering book: a
// coding agent should remember USER PREFERENCES (how the user likes to work,
// what tools they favour, what to avoid) across turns — while never storing
// code itself. Memory here is intentionally narrow:
//
//   - it only keeps short preference statements, not code or payloads;
//   - repeating a preference refreshes it (recency = relevance);
//   - stale entries age out after a TTL so the injected block stays small.
package prefmem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Entry is one remembered preference.
type Entry struct {
	// Text is the normalized preference statement, e.g. "用简体中文回答" or
	// "优先用 Go 写后台服务".
	Text string
	// FirstAt is when the preference was first learned.
	FirstAt time.Time
	// SeenAt is the last time the preference was (re)stated. Drives age-out.
	SeenAt time.Time
	// Seen counts how often the user has stated this preference. Repetition
	// indicates a strong preference (book: "repetition is the strongest
	// signal of intent").
	Seen int
}

// Options tune the memory store. Zero value gives the defaults.
type Options struct {
	// TTL is how long a preference stays alive without being restated.
	// Default 30 days.
	TTL time.Duration
	// MaxEntries caps how many preferences are kept/injected. Default 12.
	MaxEntries int
}

func (o *Options) apply() {
	if o.TTL <= 0 {
		o.TTL = 30 * 24 * time.Hour
	}
	if o.MaxEntries <= 0 {
		o.MaxEntries = 12
	}
}

// Store is a concurrency-safe preference memory. All methods are safe for
// concurrent use. It is deliberately dependency-free: no disk, no db, no LLM.
// Persistence is the caller's job (see Store.Snapshot / Store.Restore).
type Store struct {
	mu      sync.Mutex
	entries map[string]*Entry // keyed by normalized text
	opts    Options
}

// New returns an empty Store with the given options (defaults applied).
func New(opts Options) *Store {
	opts.apply()
	return &Store{entries: make(map[string]*Entry), opts: opts}
}

// normalize collapses whitespace and trims punctuation so the same statement
// written slightly differently maps to the same memory key.
func normalize(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// preferenceMarkers are explicit linguistic signals that the user is stating
// a durable preference, not describing the current task. Only statements
// containing one of these are candidates for memory — everything else (task
// instructions, code, file paths) is deliberately ignored, matching the
// book's "remember preferences, never code" rule.
var preferenceMarkers = []struct {
	marker string
	tip    string // if non-empty, keep only text after this marker
}{
	{"以后都用", ""},
	{"以后都", ""},
	{"以后请", ""},
	{"请总是", ""},
	{"请一直", ""},
	{"我总是", ""},
	{"我习惯", ""},
	{"我偏好", ""},
	{"我更喜欢", ""},
	{"我喜欢用", ""},
	{"别用", ""},
	{"不要用", ""},
	{"优先用", ""},
	{"always use", ""},
	{"always ", ""},
	{"prefer ", ""},
	{"never use ", ""},
	{"i like to use ", ""},
	{"please always ", ""},
	{"i always ", ""},
}

// maxPrefLen caps how long a remembered statement may be. Anything longer is
// probably a task instruction in disguise, not a preference.
const maxPrefLen = 120

// Extract scans user text for explicit preference statements and returns
// normalized, deduplicated candidates. It is intentionally cheap and
// deterministic (no LLM): each result is a short phrase suitable for
// Store.Remember.
func Extract(text string) []string {
	lower := strings.ToLower(text)
	seen := make(map[string]bool)
	var out []string
	for _, m := range preferenceMarkers {
		idx := strings.Index(lower, m.marker)
		if idx < 0 {
			continue
		}
		start := idx + len(m.marker)
		if m.tip != "" {
			if tip := strings.Index(lower[start:], m.tip); tip >= 0 {
				start += tip + len(m.tip)
			}
		}
		// Take from the marker to the end of the sentence (sentence breakers).
		rest := text[start:]
		end := len(rest)
		for _, brk := range []string{"。", ". ", "\n", ";", "，"} {
			if i := strings.Index(rest, brk); i >= 0 && i < end {
				end = i
			}
		}
		phrase := normalize(rest[:end])
		if phrase == "" || len([]rune(phrase)) > maxPrefLen {
			continue
		}
		if seen[phrase] {
			continue
		}
		seen[phrase] = true
		out = append(out, phrase)
	}
	return out
}

// Remember records a stated preference, refreshing its SeenAt/Seen counters.
// If the entry is stale it is treated as brand new again. Returns the stored
// entry (nil if the input is empty).
func (s *Store) Remember(text string) *Entry {
	text = normalize(text)
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	e, ok := s.entries[text]
	if ok {
		e.SeenAt = now
		e.Seen++
		return e
	}
	// Age out the oldest stale entry if we are at capacity.
	if len(s.entries) >= s.opts.MaxEntries {
		var oldest *Entry
		for _, v := range s.entries {
			if oldest == nil || v.SeenAt.Before(oldest.SeenAt) {
				oldest = v
			}
		}
		if oldest != nil {
			delete(s.entries, oldest.Text)
		}
	}
	e = &Entry{Text: text, FirstAt: now, SeenAt: now, Seen: 1}
	s.entries[text] = e
	return e
}

// Forget removes a preference by exact normalized text. Returns true if it
// existed.
func (s *Store) Forget(text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.entries[normalize(text)]
	if ok {
		delete(s.entries, normalize(text))
	}
	return ok
}

// Purge removes all stored preferences. Returns the number removed.
func (s *Store) Purge() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.entries)
	s.entries = make(map[string]*Entry)
	return n
}

// List returns a copy of the live (non-stale) entries ordered by recency,
// newest first. Age-out happens here: anything not restated within TTL is
// dropped (and thus no longer injected).
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-s.opts.TTL)
	live := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		if e.SeenAt.Before(cutoff) {
			continue
		}
		live = append(live, e)
	}
	if len(live) == 0 {
		return nil
	}
	// Sort by SeenAt descending (most recent first).
	for i := 1; i < len(live); i++ {
		for j := i; j > 0 && live[j].SeenAt.After(live[j-1].SeenAt); j-- {
			live[j], live[j-1] = live[j-1], live[j]
		}
	}
	out := make([]Entry, 0, len(live))
	for _, e := range live {
		out = append(out, *e)
	}
	return out
}

// Render produces a compact system-prompt block for the remembered
// preferences. Returns "" when nothing is remembered (so callers can skip
// injection entirely and keep the prompt cache-stable).
func (s *Store) Render() string {
	list := s.List()
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nUSER PREFERENCES (remembered — respect them unless the user says otherwise):\n")
	for _, e := range list {
		b.WriteString("- ")
		b.WriteString(e.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// Snapshot returns the current live entries for persistence.
func (s *Store) Snapshot() []Entry {
	return s.List()
}

// Restore re-imports previously snapshotted entries. Duplicates are merged by
// keeping the more recent SeenAt. Old entries whose SeenAt is beyond the TTL
// are dropped.
func (s *Store) Restore(entries []Entry) {
	if len(entries) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-s.opts.TTL)
	for _, e := range entries {
		if e.SeenAt.Before(cutoff) {
			continue
		}
		if e.Text == "" {
			continue
		}
		e.Text = normalize(e.Text)
		if e.Text == "" {
			continue
		}
		cur, ok := s.entries[e.Text]
		if ok && cur.SeenAt.After(e.SeenAt) {
			continue
		}
		if !ok && len(s.entries) >= s.opts.MaxEntries {
			var oldest *Entry
			for _, v := range s.entries {
				if oldest == nil || v.SeenAt.Before(oldest.SeenAt) {
					oldest = v
				}
			}
			if oldest != nil {
				delete(s.entries, oldest.Text)
			}
		}
		s.entries[e.Text] = &Entry{
			Text:    e.Text,
			FirstAt: e.FirstAt,
			SeenAt:  e.SeenAt,
			Seen:    e.Seen,
		}
		if e.Seen <= 0 {
			s.entries[e.Text].Seen = 1
		}
	}
}

// DefaultPath returns the default preference file location (~/.icode/prefs.json).
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".icode", "prefs.json")
}

// LoadFile loads preferences from a JSON file produced by SaveFile into a new
// Store. Missing/corrupt files yield an empty store rather than an error so
// first-run and partial writes are never fatal.
func LoadFile(path string) *Store {
	s := New(Options{})
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return s
	}
	s.Restore(entries)
	return s
}

// SaveFile persists the store's live entries to path (atomically: write to a
// temp file then rename). Errors are returned so callers can log them, but a
// failed persistence never affects the conversation.
func (s *Store) SaveFile(path string) error {
	entries := s.Snapshot()
	if len(entries) == 0 {
		// Nothing to remember — remove any stale file so it can't leak into
		// a future run with a very short TTL.
		_ = os.Remove(path)
		return nil
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
