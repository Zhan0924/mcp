package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Search Result Cache — P2 Performance Optimization
//
//  缓存检索结果，减少重复查询的向量计算和网络往返开销。
//  使用简单的 LRU + TTL 策略，避免引入额外依赖。
// ──────────────────────────────────────────────────────────────────────────────

// SearchCacheConfig configures the search result cache.
type SearchCacheConfig struct {
	Enabled    bool          `toml:"enabled"`
	MaxEntries int           `toml:"max_entries"` // max cache entries
	TTL        time.Duration `toml:"ttl"`         // entry expiration time
}

// DefaultSearchCacheConfig returns sensible defaults.
func DefaultSearchCacheConfig() SearchCacheConfig {
	return SearchCacheConfig{
		Enabled:    true,
		MaxEntries: 500,
		TTL:        5 * time.Minute,
	}
}

// searchCacheEntry stores a cached search result with expiry.
type searchCacheEntry struct {
	results   []RetrievalResult
	createdAt time.Time
}

func (e *searchCacheEntry) expired(ttl time.Duration) bool {
	return time.Since(e.createdAt) > ttl
}

// SearchCache caches retrieval results by query+params hash.
type SearchCache struct {
	mu      sync.RWMutex
	entries map[string]*searchCacheEntry
	order   []string // insertion order for LRU eviction
	config  SearchCacheConfig
	hits    int64
	misses  int64
}

// NewSearchCache creates a new search result cache.
func NewSearchCache(cfg SearchCacheConfig) *SearchCache {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 500
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 5 * time.Minute
	}
	return &SearchCache{
		entries: make(map[string]*searchCacheEntry, cfg.MaxEntries),
		order:   make([]string, 0, cfg.MaxEntries),
		config:  cfg,
	}
}

// SearchCacheKey generates a deterministic cache key from query parameters.
func SearchCacheKey(query string, userID uint, topK int, fileIDs []string) string {
	raw := fmt.Sprintf("q=%s&uid=%d&k=%d&fids=%v", query, userID, topK, fileIDs)
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:16]) // 128-bit key
}

// Get retrieves cached results. Returns nil, false on miss.
func (c *SearchCache) Get(key string) ([]RetrievalResult, bool) {
	if !c.config.Enabled {
		return nil, false
	}

	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok || entry.expired(c.config.TTL) {
		c.mu.Lock()
		c.misses++
		// Clean expired entry if exists
		if ok && entry.expired(c.config.TTL) {
			delete(c.entries, key)
			c.removeFromOrder(key)
		}
		c.mu.Unlock()
		return nil, false
	}

	c.mu.Lock()
	c.hits++
	c.mu.Unlock()
	return entry.results, true
}

// Set stores search results in cache.
func (c *SearchCache) Set(key string, results []RetrievalResult) {
	if !c.config.Enabled || len(results) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict oldest if at capacity
	for len(c.entries) >= c.config.MaxEntries && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}

	// Deep copy results to prevent mutation
	copied := make([]RetrievalResult, len(results))
	copy(copied, results)

	c.entries[key] = &searchCacheEntry{
		results:   copied,
		createdAt: time.Now(),
	}
	c.order = append(c.order, key)
}

// Invalidate removes a specific cache entry (e.g., after document update).
func (c *SearchCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	c.removeFromOrder(key)
}

// InvalidateByUser removes all cache entries for a user (e.g., after index/delete).
func (c *SearchCache) InvalidateByUser(userID uint) {
	prefix := fmt.Sprintf("uid=%d", userID)
	_ = prefix // For future: if keys encode user_id, filter by prefix
	// Simple approach: clear all (safe, slightly aggressive)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*searchCacheEntry, c.config.MaxEntries)
	c.order = c.order[:0]
}

// Stats returns cache hit/miss statistics.
func (c *SearchCache) Stats() SearchCacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	total := c.hits + c.misses
	hitRate := float64(0)
	if total > 0 {
		hitRate = float64(c.hits) / float64(total) * 100
	}
	return SearchCacheStats{
		Hits:    c.hits,
		Misses:  c.misses,
		Size:    len(c.entries),
		MaxSize: c.config.MaxEntries,
		HitRate: hitRate,
	}
}

// SearchCacheStats holds cache statistics.
type SearchCacheStats struct {
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
	Size    int     `json:"size"`
	MaxSize int     `json:"max_size"`
	HitRate float64 `json:"hit_rate_percent"`
}

func (c *SearchCache) removeFromOrder(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// ── Global Search Cache ──────────────────────────────────────────────────────

var (
	globalSearchCache     *SearchCache
	globalSearchCacheOnce sync.Once
)

// InitGlobalSearchCache initializes the global search cache singleton.
func InitGlobalSearchCache(cfg SearchCacheConfig) *SearchCache {
	globalSearchCacheOnce.Do(func() {
		globalSearchCache = NewSearchCache(cfg)
	})
	return globalSearchCache
}

// GetGlobalSearchCache returns the global search cache (may be nil).
func GetGlobalSearchCache() *SearchCache {
	return globalSearchCache
}
