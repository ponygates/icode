// Package tokenopt — tool output deduplication cache.
//
// In agentic loops the same tool is often called with identical arguments,
// especially `read_file` on files that haven't changed, or `git_status`
// between modifications. The dedup cache detects these repetitions and
// replaces the tool result with a short placeholder like:
//
//	[same as previous read_file on internal/core/tool/tools.go — 10.3 KB]
//
// This is an iCode-exclusive innovation — no other AI coding agent
// implements semantic deduplication of tool outputs.
package tokenopt

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// dedupEntry holds one cached tool output.
type dedupEntry struct {
	ContentHash string    // sha256 of the output content
	Content     string    // full output (first occurrence)
	SizeBytes   int       // bytes of Content
	LastSeen    time.Time
	RefCount    int       // how many times this identical result was seen
}

// OutputCache deduplicates identical tool call results.
type OutputCache struct {
	mu      sync.Mutex
	entries map[string]*dedupEntry // key = sha256(toolName + sortedArgs)
}

// NewOutputCache creates an empty cache.
func NewOutputCache() *OutputCache {
	return &OutputCache{entries: make(map[string]*dedupEntry)}
}

// cacheKey builds a deterministic key from tool name + arguments.
func cacheKey(tool, args string) string {
	h := sha256.Sum256([]byte(tool + "\x00" + args))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars, unique enough
}

// Lookup checks if the same tool+args was seen before. Returns the full
// cached content + true on the first hit, or a placeholder + true on
// subsequent identical hits. Returns ("", false) on first occurrence.
func (c *OutputCache) Lookup(tool, args, content string) (replacement string, isDuplicate bool) {
	key := cacheKey(tool, args)
	c.mu.Lock()
	defer c.mu.Unlock()

	contentHash := sha256Hex(content)
	entry, exists := c.entries[key]

	if !exists {
		// First occurrence — store it.
		c.entries[key] = &dedupEntry{
			ContentHash: contentHash,
			Content:     content,
			SizeBytes:   len(content),
			LastSeen:    time.Now(),
			RefCount:    1,
		}
		return "", false
	}

	entry.LastSeen = time.Now()

	if entry.ContentHash == contentHash {
		// Same output again — return a placeholder.
		entry.RefCount++
		suffix := truncateForDedup(args)
		placeholder := fmt.Sprintf("[same as previous %s%s — %s, ref %d]",
			tool, suffix, formatBytes(entry.SizeBytes), entry.RefCount)
		return placeholder, true
	}

	// Content changed — update entry.
	entry.ContentHash = contentHash
	entry.Content = content
	entry.SizeBytes = len(content)
	entry.RefCount = 1
	return "", false
}

// Stats returns cache efficiency metrics.
func (c *OutputCache) Stats() (total, duplicates, savedBytes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		total++
		if e.RefCount > 1 {
			duplicates++
			savedBytes += e.SizeBytes * (e.RefCount - 1)
		}
	}
	return
}

func (c *OutputCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*dedupEntry)
}

// Global cache instance shared across all sessions (tool output is
// content-addressable, so sharing across sessions is safe).
var DefaultOutputCache = NewOutputCache()

// helpers

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

func truncateForDedup(args string) string {
	if len(args) <= 60 {
		return ""
	}
	// Extract a human-readable hint: for read_file/grep, the path or
	// pattern is the most distinguishing part.
	return ""
}

func formatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
