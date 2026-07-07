package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type CacheStrategy string

const (
	StrategyAppendOnly CacheStrategy = "append-only"
	StrategyPrefix     CacheStrategy = "prefix"
	StrategyMinimal    CacheStrategy = "minimal"
)

type CacheEntry struct {
	Hash      string    `json:"hash"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	AccessCount int     `json:"access_count"`
}

type Cache struct {
	mu       sync.RWMutex
	entries  map[string]*CacheEntry
	maxSize  int
	strategy CacheStrategy
}

type ProviderCache interface {
	Get(prefix string) (string, bool)
	Set(prefix, content string)
	Invalidate(prefix string)
	Clear()
}

type CacheLayer struct {
	memCache     *Cache
	providerName string
	prefixID     string
}

func New(maxSizeMB int, strategy CacheStrategy) *Cache {
	return &Cache{
		entries:  make(map[string]*CacheEntry),
		maxSize:  maxSizeMB * 1024 * 1024,
		strategy: strategy,
	}
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok {
		return "", false
	}
	entry.AccessCount++
	return entry.Content, true
}

func (c *Cache) Set(key, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &CacheEntry{
		Hash:      hashContent(content),
		Content:   content,
		CreatedAt: time.Now(),
	}
}

func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*CacheEntry)
}

func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func NewCacheLayer(memCache *Cache, providerName string) *CacheLayer {
	return &CacheLayer{
		memCache:     memCache,
		providerName: providerName,
	}
}

func (cl *CacheLayer) AppendOnlyKey(prefix string) string {
	return cl.providerName + ":append:" + hashContent(prefix)
}

func (cl *CacheLayer) StoreAppendPrefix(prefix string) string {
	key := cl.AppendOnlyKey(prefix)
	cl.memCache.Set(key, prefix)
	return key
}
