// Package searchreplace implements a staging area for SEARCH/REPLACE edit blocks.
//
// Workflow:
//  1. LLM calls search_replace tool → edits are staged, not applied
//  2. User runs /review to inspect pending changes (shows unified diff)
//  3. User runs /apply  to commit all staged edits (with git checkpoint rollback)
//  4. User runs /reject to discard staged edits
package searchreplace

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// StagedEdit holds one proposed SEARCH/REPLACE edit.
type StagedEdit struct {
	FilePath string
	Search   string
	Replace  string
	Valid    bool   // true if search text was found in the file
	Reason   string // validation message
	Diff     string // unified diff preview (empty if invalid)
}

// StagingArea holds pending edits per session.
type StagingArea struct {
	mu    sync.Mutex
	edits []StagedEdit
}

// stageRegistry keeps exactly one staging area per session. There is NO
// shared fallback bucket: the old package-level defaultStage silently merged
// every empty-sessionID caller into one area, which cross-contaminated staged
// edits between desktop tabs sharing a process. Callers that legitimately
// have no conversation session must use ProcessSessionID() (deterministic per
// process, still isolated from every real session).
//
// Areas are created lazily and never pruned — a session finishes a handful
// of staged-edit rounds, so the footprint is trivial.
var (
	stageMu     sync.Mutex
	stageBySess = map[string]*StagingArea{}
)

// processStageID is the deterministic per-process staging key. pid makes it
// stable for the lifetime of one process (restarting gives a fresh, still
// deterministic value) and unique across processes.
var processStageID = fmt.Sprintf("process:%d", os.Getpid())

// ProcessSessionID returns the staging key for callers with no conversation
// session (single-shot CLI exec, UI slash commands before a session exists).
func ProcessSessionID() string { return processStageID }

// ErrNoSession is returned by StageForSession for an empty session ID —
// callers must pass a real session ID or ProcessSessionID().
var ErrNoSession = errors.New("searchreplace: empty session ID (use a real session or ProcessSessionID)")

// StageForSession returns the staging area owned by the given session ID.
// An empty session ID is an explicit error (ErrNoSession), never a shared
// bucket.
func StageForSession(sessionID string) (*StagingArea, error) {
	if sessionID == "" {
		return nil, ErrNoSession
	}
	stageMu.Lock()
	defer stageMu.Unlock()
	if s, ok := stageBySess[sessionID]; ok {
		return s, nil
	}
	s := &StagingArea{}
	stageBySess[sessionID] = s
	return s, nil
}

// StageForSessionOrProcess is the convenience for callers that may
// legitimately lack a session ID: they get this process's deterministic
// private area instead of an error — still never shared with any session.
func StageForSessionOrProcess(sessionID string) *StagingArea {
	if sessionID == "" {
		sessionID = processStageID
	}
	s, _ := StageForSession(sessionID)
	return s
}

// Add validates and stages a proposed edit.
func (s *StagingArea) Add(filePath, search, replace string) (int, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ed := StagedEdit{
		FilePath: filePath,
		Search:   search,
		Replace:  replace,
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		ed.Reason = fmt.Sprintf("cannot read %s: %v", filePath, err)
		ed.Valid = false
	} else {
		text := string(content)
		c := strings.Count(text, search)
		if c == 0 {
			ed.Reason = fmt.Sprintf("search text not found in %s", filePath)
			ed.Valid = false
		} else {
			var newText string
			if c == 1 {
				newText = strings.ReplaceAll(text, search, replace)
			} else {
				newText = strings.Replace(text, search, replace, 1)
			}
			ed.Valid = true
			ed.Reason = fmt.Sprintf("%d occurrence(s) found, replacing first only", c)
			ed.Diff = unifiedDiff(filePath, text, newText, search, replace)
		}
	}

	idx := len(s.edits)
	s.edits = append(s.edits, ed)
	return idx, ed.Valid, ed.Reason
}

func (s *StagingArea) List() []StagedEdit {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StagedEdit, len(s.edits))
	copy(out, s.edits)
	return out
}

func (s *StagingArea) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.edits)
}

func (s *StagingArea) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edits = nil
}

// ApplyValid applies all valid edits. Skips invalid ones.
func (s *StagingArea) ApplyValid() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var results []string
	var remaining []StagedEdit

	for _, ed := range s.edits {
		if !ed.Valid {
			results = append(results, fmt.Sprintf("SKIPPED %s: %s", ed.FilePath, ed.Reason))
			continue
		}
		content, err := os.ReadFile(ed.FilePath)
		if err != nil {
			results = append(results, fmt.Sprintf("FAILED %s: %v", ed.FilePath, err))
			remaining = append(remaining, ed)
			continue
		}
		text := string(content)
		c := strings.Count(text, ed.Search)
		if c == 0 {
			results = append(results, fmt.Sprintf("FAILED %s: search text no longer found (stale)", ed.FilePath))
			remaining = append(remaining, ed)
			continue
		}
		text = strings.Replace(text, ed.Search, ed.Replace, 1)
		if err := os.WriteFile(ed.FilePath, []byte(text), 0644); err != nil {
			results = append(results, fmt.Sprintf("FAILED %s: write error: %v", ed.FilePath, err))
			remaining = append(remaining, ed)
			continue
		}
		results = append(results, fmt.Sprintf("APPLIED %s: %s", ed.FilePath, ed.Reason))
	}

	s.edits = remaining
	return results
}

// unifiedDiff generates a compact context diff between old and new text,
// focused on the SEARCH/REPLACE region. Only lines around the changed area
// are shown, making it easy for the user to review.
func unifiedDiff(path, oldText, newText, search, replace string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	// Find the first line where old and new diverge
	var startLine int
	for startLine < len(oldLines) && startLine < len(newLines) {
		if oldLines[startLine] != newLines[startLine] {
			break
		}
		startLine++
	}
	if startLine >= len(oldLines) && startLine >= len(newLines) {
		return "(no changes)"
	}

	// Find the last line where old and new diverge
	endOld := len(oldLines) - 1
	endNew := len(newLines) - 1
	for endOld >= startLine && endNew >= startLine {
		if oldLines[endOld] != newLines[endNew] {
			break
		}
		endOld--
		endNew--
	}

	// Build focused diff with 2 lines of context
	ctxStart := startLine - 2
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEndOld := endOld + 2
	if ctxEndOld >= len(oldLines) {
		ctxEndOld = len(oldLines) - 1
	}
	ctxEndNew := endNew + 2
	if ctxEndNew >= len(newLines) {
		ctxEndNew = len(newLines) - 1
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- %s\n+++ %s\n", path, path))
	b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
		ctxStart+1, ctxEndOld-ctxStart+1,
		ctxStart+1, ctxEndNew-ctxStart+1))

	lineOld := ctxStart
	lineNew := ctxStart
	for lineOld <= ctxEndOld || lineNew <= ctxEndNew {
		oldLine := ""
		newLine := ""
		if lineOld <= ctxEndOld {
			oldLine = oldLines[lineOld]
		}
		if lineNew <= ctxEndNew {
			newLine = newLines[lineNew]
		}
		switch {
		case oldLine == newLine && oldLine != "":
			b.WriteString(fmt.Sprintf(" %s\n", oldLine))
			lineOld++
			lineNew++
		case lineOld <= ctxEndOld && (lineNew > ctxEndNew || oldLine != newLine):
			// Check if this line is part of the SEARCH block
			if lineOld <= endOld {
				b.WriteString(fmt.Sprintf("-%s\n", oldLine))
				lineOld++
			}
			if lineNew <= endNew {
				b.WriteString(fmt.Sprintf("+%s\n", newLine))
				lineNew++
			}
		default:
			if lineNew <= ctxEndNew {
				b.WriteString(fmt.Sprintf("+%s\n", newLine))
				lineNew++
			}
			if lineOld <= ctxEndOld {
				b.WriteString(fmt.Sprintf("-%s\n", oldLine))
				lineOld++
			}
		}
	}

	return b.String()
}
