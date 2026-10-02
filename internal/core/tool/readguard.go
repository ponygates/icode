// Read-before-edit guard (Claude Code / Codex parity).
//
// The model must not edit a file it has never seen — blind old_string guesses
// are the #1 source of hallucinated edit failures (and, worse, wrong-file
// edits). This file tracks, PER CONVERSATION, which files the model has
// actually looked at this session (read_file / glob / ls results) or created
// (write_file / successful edits). edit / write_file / search_replace consult
// the tracker and refuse untracked targets with an actionable "read it first"
// error.
//
// Keying follows internal/core/searchreplace/stage.go: state lives in a
// registry keyed by session ID (never a global bucket), so desktop multi-tab
// conversations cannot see or poison each other's tracker. Callers without a
// session ID fall back to a deterministic per-process key.

package tool

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// readGuardState is one conversation's set of normalized file paths the
// model has seen.
type readGuardState struct {
	mu   sync.Mutex
	seen map[string]bool
}

var (
	readGuardMu   sync.Mutex
	readGuardSess = map[string]*readGuardState{}
	readGuardProc = "process:" + strconv.Itoa(os.Getpid())
)

// readGuardKey maps a (possibly empty) session ID to its tracker bucket.
// Empty IDs (headless one-shot tool runs, UI fallbacks) get the process-wide
// deterministic bucket — the same treatment searchreplace gives staged edits.
func readGuardKey(sessionID string) string {
	if sessionID == "" {
		return readGuardProc
	}
	return sessionID
}

func readGuardFor(sessionID string) *readGuardState {
	key := readGuardKey(sessionID)
	readGuardMu.Lock()
	defer readGuardMu.Unlock()
	if s, ok := readGuardSess[key]; ok {
		return s
	}
	s := &readGuardState{seen: make(map[string]bool)}
	readGuardSess[key] = s
	return s
}

// normalizeEditPath makes "a/b.go", "./a\\b.go" and the absolute path of the
// same file collide in the tracker (the model mixes separators freely and
// read/edit calls may use different spellings).
func normalizeEditPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	return strings.ToLower(filepath.Clean(abs))
}

// markFileRead records that the model has seen this file's content (or at
// least knows it exists — glob/ls results count per the guard design).
func markFileRead(sessionID, path string) {
	readGuardFor(sessionID).mark(path)
}

func markFileReads(sessionID string, paths []string) {
	s := readGuardFor(sessionID)
	for _, p := range paths {
		s.mark(p)
	}
}

func (s *readGuardState) mark(path string) {
	if path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[normalizeEditPath(path)] = true
}

func (s *readGuardState) has(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[normalizeEditPath(path)]
}

// readGuardVerdict is the guard's answer for one edit attempt.
type readGuardVerdict int

const (
	readGuardOK     readGuardVerdict = iota // tracked (or file unseen but harmless)
	readGuardEscape                         // untracked, but every old/new pair matches uniquely+exactly
	readGuardBlock                          // untracked and unverifiable — refuse
)

// checkEditGuard decides whether an edit/write on path may proceed without a
// prior read. oldStrings are the verbatim needles the edit will search for
// (empty slice = whole-file write).
//
// The escape hatch (unique+exact match) is deliberately NOT a config flag:
// WHY — an old_string that occurs exactly once and byte-for-byte in the file
// is self-verifying (the replacement can only land where the model meant it),
// and refusing it would deadlock headless/CI/--yolo runs that have no
// interactive read step to unblock them. Everything else stays blocked, so
// the guard still catches the fuzzy/guessed edits that cause wrong-file
// damage.
func checkEditGuard(sessionID, path, text string, oldStrings []string) readGuardVerdict {
	if readGuardFor(sessionID).has(path) {
		return readGuardOK
	}
	// Brand-new file (write_file creating it): nothing to have read.
	if _, err := os.Stat(path); os.IsNotExist(err) && text == "" {
		return readGuardOK
	}
	// Whole-file write: there is no needle to self-verify, so no escape.
	if len(oldStrings) == 0 {
		return readGuardBlock
	}
	for _, o := range oldStrings {
		if o == "" || strings.Count(text, o) != 1 {
			return readGuardBlock
		}
	}
	return readGuardEscape
}

// readGuardBlockMessage is the actionable refusal the model sees.
func readGuardBlockMessage(path string) string {
	return "refused: file \"" + path + "\" has not been read in this session. " +
		"Call read_file on it first to see the current content, then retry the edit " +
		"(an old_string that matches uniquely and verbatim in the file is always allowed)."
}

// readGuardEscapeWarning is appended to the tool result when the escape
// hatch let an untracked edit through.
const readGuardEscapeWarning = "\n⚠ [read-before-edit] 该文件本轮会话未先 read_file，因 old_string 与文件内容唯一且精确匹配才放行；建议先读取再改，避免盲改。"
