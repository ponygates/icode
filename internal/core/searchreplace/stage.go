// Package searchreplace implements a staging area for SEARCH/REPLACE edit blocks.
//
// Workflow:
//  1. LLM calls search_replace tool → edits are staged, not applied
//  2. User runs /review to inspect pending changes
//  3. User runs /apply  to commit all staged edits
//  4. User runs /reject to discard staged edits
package searchreplace

import (
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
}

// StagingArea holds pending edits per session.
type StagingArea struct {
	mu    sync.Mutex
	edits []StagedEdit
}

var defaultStage = &StagingArea{}

// StageAdd adds a proposed edit, validates it, and returns the index.
func StageAdd(filePath, search, replace string) (int, bool, string) {
	return defaultStage.Add(filePath, search, replace)
}

// StageList returns all staged edits (copy).
func StageList() []StagedEdit {
	return defaultStage.List()
}

// StageCount returns the number of staged edits.
func StageCount() int {
	return defaultStage.Count()
}

// StageClear removes all staged edits.
func StageClear() {
	defaultStage.Clear()
}

// StageApplyValid applies all valid staged edits and returns results.
func StageApplyValid() []string {
	return defaultStage.ApplyValid()
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
		c := strings.Count(string(content), search)
		if c == 0 {
			ed.Reason = fmt.Sprintf("search text not found in %s", filePath)
			ed.Valid = false
		} else {
			ed.Valid = true
			ed.Reason = fmt.Sprintf("%d occurrence(s) will be replaced", c)
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
		text = strings.ReplaceAll(text, ed.Search, ed.Replace)
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
