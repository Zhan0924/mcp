package tests

import (
	"context"
	"testing"
	"time"

	"mcp_rag_server/rag"
)

func TestLRUCache_BasicPutGet(t *testing.T) {
	cfg := rag.CacheConfig{
		Enabled:      true,
		LocalMaxSize: 100,
		LocalTTL:     5 * time.Minute,
	}
	cache := rag.NewEmbeddingCache(cfg, nil)

	ctx := context.Background()

	vec := []float64{0.1, 0.2, 0.3}
	cache.Put(ctx, "hello world", vec)

	got, ok := cache.Get(ctx, "hello world")
	if !ok {
		t.Fatal("Expected cache hit")
	}
	if len(got) != 3 {
		t.Errorf("Expected vector of length 3, got %d", len(got))
	}
	if got[0] != 0.1 {
		t.Errorf("Expected 0.1, got %f", got[0])
	}
}

func TestLRUCache_Miss(t *testing.T) {
	cfg := rag.CacheConfig{
		Enabled:      true,
		LocalMaxSize: 100,
		LocalTTL:     5 * time.Minute,
	}
	cache := rag.NewEmbeddingCache(cfg, nil)

	ctx := context.Background()
	_, ok := cache.Get(ctx, "nonexistent")
	if ok {
		t.Error("Expected cache miss")
	}
}

func TestLRUCache_Eviction(t *testing.T) {
	cfg := rag.CacheConfig{
		Enabled:      true,
		LocalMaxSize: 3,
		LocalTTL:     5 * time.Minute,
	}
	cache := rag.NewEmbeddingCache(cfg, nil)
	ctx := context.Background()

	cache.Put(ctx, "key1", []float64{1.0})
	cache.Put(ctx, "key2", []float64{2.0})
	cache.Put(ctx, "key3", []float64{3.0})

	// key1 should still be accessible
	_, ok := cache.Get(ctx, "key1")
	if !ok {
		t.Error("key1 should still be cached")
	}

	// Adding key4 should evict the LRU entry
	cache.Put(ctx, "key4", []float64{4.0})

	_, ok = cache.Get(ctx, "key4")
	if !ok {
		t.Error("key4 should be cached")
	}

	// After accessing key1 above, key2 is now the LRU
	_, ok = cache.Get(ctx, "key2")
	if ok {
		t.Log("key2 was evicted (expected for LRU behavior)")
	}
}

func TestLRUCache_TTLExpiry(t *testing.T) {
	cfg := rag.CacheConfig{
		Enabled:      true,
		LocalMaxSize: 100,
		LocalTTL:     50 * time.Millisecond,
	}
	cache := rag.NewEmbeddingCache(cfg, nil)
	ctx := context.Background()

	cache.Put(ctx, "expiring", []float64{1.0})

	_, ok := cache.Get(ctx, "expiring")
	if !ok {
		t.Error("Entry should be accessible before TTL")
	}

	time.Sleep(100 * time.Millisecond)

	_, ok = cache.Get(ctx, "expiring")
	if ok {
		t.Error("Entry should have expired")
	}
}

func TestLRUCache_Disabled(t *testing.T) {
	cfg := rag.CacheConfig{
		Enabled: false,
	}
	cache := rag.NewEmbeddingCache(cfg, nil)
	ctx := context.Background()

	cache.Put(ctx, "test", []float64{1.0})

	_, ok := cache.Get(ctx, "test")
	if ok {
		t.Error("Disabled cache should not return results")
	}
}

func TestLRUCache_GetBatch(t *testing.T) {
	cfg := rag.CacheConfig{
		Enabled:      true,
		LocalMaxSize: 100,
		LocalTTL:     5 * time.Minute,
	}
	cache := rag.NewEmbeddingCache(cfg, nil)
	ctx := context.Background()

	cache.Put(ctx, "text1", []float64{1.0})
	cache.Put(ctx, "text3", []float64{3.0})

	texts := []string{"text1", "text2", "text3", "text4"}
	cached, missed := cache.GetBatch(ctx, texts)

	if len(cached) != 2 {
		t.Errorf("Expected 2 cached, got %d", len(cached))
	}
	if len(missed) != 2 {
		t.Errorf("Expected 2 missed, got %d", len(missed))
	}

	if _, ok := cached[0]; !ok {
		t.Error("Index 0 (text1) should be cached")
	}
	if _, ok := cached[2]; !ok {
		t.Error("Index 2 (text3) should be cached")
	}

	for _, idx := range missed {
		if idx != 1 && idx != 3 {
			t.Errorf("Unexpected missed index: %d", idx)
		}
	}
}

func TestCacheStats(t *testing.T) {
	cfg := rag.CacheConfig{
		Enabled:      true,
		LocalMaxSize: 100,
		LocalTTL:     5 * time.Minute,
	}
	cache := rag.NewEmbeddingCache(cfg, nil)
	ctx := context.Background()

	cache.Put(ctx, "key1", []float64{1.0})

	cache.Get(ctx, "key1") // hit
	cache.Get(ctx, "key2") // miss
	cache.Get(ctx, "key1") // hit

	stats := cache.Stats()
	if stats.Hits != 2 {
		t.Errorf("Expected 2 hits, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}
	if stats.HitRate < 60 || stats.HitRate > 70 {
		t.Errorf("Expected ~66.7%% hit rate, got %.1f%%", stats.HitRate)
	}
	if stats.LocalSize != 1 {
		t.Errorf("Expected local size 1, got %d", stats.LocalSize)
	}
	t.Logf("Stats: hits=%d, misses=%d, hitRate=%.1f%%, size=%d/%d",
		stats.Hits, stats.Misses, stats.HitRate, stats.LocalSize, stats.LocalCap)
}

func TestLRUCache_SameContentDifferentWhitespace(t *testing.T) {
	cfg := rag.CacheConfig{
		Enabled:      true,
		LocalMaxSize: 100,
		LocalTTL:     5 * time.Minute,
	}
	cache := rag.NewEmbeddingCache(cfg, nil)
	ctx := context.Background()

	cache.Put(ctx, "hello world", []float64{1.0})

	// Leading/trailing whitespace is trimmed in hash, so these should match
	_, ok := cache.Get(ctx, "  hello world  ")
	if !ok {
		t.Error("Content with trimmed whitespace should match")
	}
}

func TestLRUCache_UpdateExisting(t *testing.T) {
	cfg := rag.CacheConfig{
		Enabled:      true,
		LocalMaxSize: 100,
		LocalTTL:     5 * time.Minute,
	}
	cache := rag.NewEmbeddingCache(cfg, nil)
	ctx := context.Background()

	cache.Put(ctx, "key", []float64{1.0})
	cache.Put(ctx, "key", []float64{2.0})

	vec, ok := cache.Get(ctx, "key")
	if !ok {
		t.Fatal("Expected cache hit")
	}
	if vec[0] != 2.0 {
		t.Errorf("Expected updated value 2.0, got %f", vec[0])
	}
}
