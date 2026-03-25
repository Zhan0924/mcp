/*
┌──────────────────────────────────────────────────────────────────────────────┐
│                          cache.go 结构总览                                    │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  配置                                                                        │
│    CacheConfig            — 缓存开关、容量、TTL、Redis 配置、去重开关         │
│    DefaultCacheConfig()   — 默认配置                                         │
│                                                                              │
│  二级缓存                                                                    │
│    EmbeddingCache         — L1 (本地 LRU) + L2 (Redis) 两级缓存              │
│      NewEmbeddingCache()  — 构造实例                                         │
│      Get()                — 查询：L1 命中 → 返回; L1 未中 → 查 L2 → 回填 L1  │
│      Put()                — 写入：同时写 L1 和 L2                             │
│      GetBatch()           — 批量查询：返回 {命中索引→向量} + 未命中索引列表    │
│      PutBatch()           — 批量写入                                         │
│      Stats()              — 返回缓存统计快照                                  │
│    CacheStats             — 命中/未中计数 + 命中率 + 本地缓存占用量           │
│                                                                              │
│  LRU 缓存 (内部实现)                                                         │
│    lruEntry               — 链表节点：key + value + 过期时间                  │
│    lruCache               — 双向链表 + HashMap，O(1) Get/Put/淘汰             │
│      Get()                — 查找 + TTL 过期检查 + 移到链表头部                 │
│      Put()                — 写入/更新 + 容量满时淘汰链表尾部（最久未访问）    │
│      Len()                — 当前条目数                                        │
│      Clear()              — 清空所有条目                                      │
│                                                                              │
│  全局实例                                                                    │
│    InitGlobalCache()      — 初始化全局缓存单例                               │
│    GetGlobalCache()       — 获取全局缓存                                     │
│    CachedEmbedStrings()   — 带缓存的 Embedding 包级别函数                    │
│                                                                              │
│  内部工具                                                                    │
│    contentHash()          — SHA256(TrimSpace(text))，作为缓存 key             │
│                                                                              │
│  设计要点                                                                    │
│    ┌────────────────────────────────────────────────────────────────────┐    │
│    │  请求 → L1 本地 LRU (进程内, 纳秒级)                               │    │
│    │           ↓ miss                                                   │    │
│    │         L2 Redis (跨实例共享, 毫秒级)                              │    │
│    │           ↓ miss                                                   │    │
│    │         调用 Embedding API → 回写 L1 + L2                          │    │
│    └────────────────────────────────────────────────────────────────────┘    │
│    - LRU 淘汰策略：容量满时移除链表尾部（最久未使用的条目）                   │
│    - TTL：Get 时惰性检查过期，过期条目视为 miss 并移除                        │
│    - 缓存 key 用 SHA256 哈希，避免原文作 key 导致内存膨胀和碰撞               │
│    - GetBatch 返回未命中索引，调用方只需对 miss 部分调用 API，减少开销         │
│    - 线程安全：LRU 用 sync.Mutex; hits/misses 用 sync.RWMutex                │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	redisCli "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// CacheConfig 缓存配置
type CacheConfig struct {
	Enabled       bool          `toml:"enabled"`
	LocalMaxSize  int           `toml:"local_max_size"`
	LocalTTL      time.Duration `toml:"local_ttl"`
	RedisEnabled  bool          `toml:"redis_enabled"`
	RedisTTL      time.Duration `toml:"redis_ttl"`
	RedisPrefix   string        `toml:"redis_prefix"`
	DeduplicateOn bool          `toml:"deduplicate_on"` // 索引时对相同内容跳过 embedding，依赖缓存判断是否已有向量
}

// DefaultCacheConfig 默认缓存配置
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		Enabled:       true,
		LocalMaxSize:  10000,
		LocalTTL:      30 * time.Minute,
		RedisEnabled:  false,
		RedisTTL:      24 * time.Hour,
		RedisPrefix:   "emb_cache:",
		DeduplicateOn: true,
	}
}

// EmbeddingCache 二级 Embedding 缓存
// L1: 本地 LRU (进程内, 纳秒级延迟, 受 LocalMaxSize 容量限制)
// L2: Redis (跨实例共享, 毫秒级延迟, 可选启用)
type EmbeddingCache struct {
	config CacheConfig
	local  *lruCache
	redis  redisCli.UniversalClient

	hits   int64 // 命中计数（L1 或 L2 命中均算）
	misses int64 // 未命中计数
	mu     sync.RWMutex
}

// NewEmbeddingCache 创建缓存实例
func NewEmbeddingCache(config CacheConfig, redisClient redisCli.UniversalClient) *EmbeddingCache {
	cache := &EmbeddingCache{
		config: config,
		local:  newLRUCache(config.LocalMaxSize, config.LocalTTL),
	}
	if config.RedisEnabled && redisClient != nil {
		cache.redis = redisClient
	}
	return cache
}

// Get 查询缓存，按 L1 → L2 顺序查找
// L2 命中时自动回填 L1，后续相同请求可在进程内直接命中，避免重复 Redis 网络开销
func (c *EmbeddingCache) Get(ctx context.Context, text string) ([]float64, bool) {
	if !c.config.Enabled {
		return nil, false
	}

	key := contentHash(text)

	// L1: 本地 LRU（纳秒级）
	if vec, ok := c.local.Get(key); ok {
		c.mu.Lock()
		c.hits++
		c.mu.Unlock()
		return vec, true
	}

	// L2: Redis（毫秒级），命中后回填 L1
	if c.redis != nil {
		redisKey := c.config.RedisPrefix + key
		data, err := c.redis.Get(ctx, redisKey).Bytes()
		if err == nil {
			var vec []float64
			if err := json.Unmarshal(data, &vec); err == nil {
				c.local.Put(key, vec) // 回填 L1
				c.mu.Lock()
				c.hits++
				c.mu.Unlock()
				return vec, true
			}
		}
	}

	c.mu.Lock()
	c.misses++
	c.mu.Unlock()
	return nil, false
}

// Put 写入缓存 (同时写 L1 + L2)
func (c *EmbeddingCache) Put(ctx context.Context, text string, vec []float64) {
	if !c.config.Enabled {
		return
	}

	key := contentHash(text)

	c.local.Put(key, vec)

	if c.redis != nil {
		data, err := json.Marshal(vec)
		if err != nil {
			return
		}
		redisKey := c.config.RedisPrefix + key
		if err := c.redis.Set(ctx, redisKey, data, c.config.RedisTTL).Err(); err != nil {
			logrus.Debugf("[EmbeddingCache] Redis put failed: %v", err)
		}
	}
}

// GetBatch 批量查询：返回 {原始索引 → 向量} 和未命中索引列表
// 调用方只需对 missed 列表中的文本调用 Embedding API，减少 API 调用量
func (c *EmbeddingCache) GetBatch(ctx context.Context, texts []string) (map[int][]float64, []int) {
	if !c.config.Enabled {
		missed := make([]int, len(texts))
		for i := range texts {
			missed[i] = i
		}
		return nil, missed
	}

	cached := make(map[int][]float64)
	var missed []int

	for i, text := range texts {
		if vec, ok := c.Get(ctx, text); ok {
			cached[i] = vec
		} else {
			missed = append(missed, i)
		}
	}

	return cached, missed
}

// PutBatch 批量写入缓存
func (c *EmbeddingCache) PutBatch(ctx context.Context, texts []string, vectors [][]float64) {
	if !c.config.Enabled || len(texts) != len(vectors) {
		return
	}
	for i, text := range texts {
		c.Put(ctx, text, vectors[i])
	}
}

// Stats 缓存统计
func (c *EmbeddingCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	total := c.hits + c.misses
	var hitRate float64
	if total > 0 {
		hitRate = float64(c.hits) / float64(total) * 100
	}
	return CacheStats{
		Hits:      c.hits,
		Misses:    c.misses,
		HitRate:   hitRate,
		LocalSize: c.local.Len(),
		LocalCap:  c.config.LocalMaxSize,
	}
}

// CacheStats 缓存统计信息
type CacheStats struct {
	Hits      int64   `json:"hits"`
	Misses    int64   `json:"misses"`
	HitRate   float64 `json:"hit_rate_percent"`
	LocalSize int     `json:"local_size"`
	LocalCap  int     `json:"local_capacity"`
}

// contentHash 使用 SHA256 哈希文本生成缓存 key
// TrimSpace 保证首尾空白差异不会导致相同内容产生不同 key
// SHA256 而非 MD5: 抗碰撞性更强，在大规模索引场景下降低哈希冲突概率
func contentHash(text string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(h[:])
}

// --- LRU Cache (本地内存) ---
// 经典 LRU 实现：双向链表 (eviction order) + HashMap (O(1) 查找)
// 链表头部 = 最近使用; 链表尾部 = 最久未使用 (淘汰候选)

type lruEntry struct {
	key       string
	value     []float64
	expiresAt time.Time // TTL 过期时间，零值表示永不过期
}

type lruCache struct {
	maxSize  int
	ttl      time.Duration
	items    map[string]*list.Element // key → 链表节点，O(1) 定位
	eviction *list.List               // 淘汰顺序链表，Front=最近使用, Back=最久未使用
	mu       sync.Mutex
	stopCh   chan struct{} // 后台清理协程停止信号
}

func newLRUCache(maxSize int, ttl time.Duration) *lruCache {
	if maxSize <= 0 {
		maxSize = 10000
	}
	c := &lruCache{
		maxSize:  maxSize,
		ttl:      ttl,
		items:    make(map[string]*list.Element),
		eviction: list.New(),
		stopCh:   make(chan struct{}),
	}
	// 启动后台过期清理协程：每 5 分钟扫描一轮，清除过期但未被访问的条目
	// 补充惰性过期的不足——长期不被 Get 的过期条目也能被释放
	if ttl > 0 {
		go c.backgroundCleanup()
	}
	return c
}

// Get 查找条目，命中后移到链表头部（标记为"最近使用"）
// TTL 惰性过期：仅在 Get 时检查，过期条目立即移除，视为 miss
// 好处：无需后台清理协程，简化并发控制
func (c *lruCache) Get(key string) ([]float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*lruEntry)
	if c.ttl > 0 && time.Now().After(entry.expiresAt) {
		c.eviction.Remove(elem)
		delete(c.items, key)
		return nil, false
	}

	c.eviction.MoveToFront(elem)
	return entry.value, true
}

// Put 写入条目，已存在则更新值和 TTL；不存在则插入，容量满时淘汰链表尾部
func (c *lruCache) Put(key string, value []float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// key 已存在：更新值、刷新 TTL、移到头部
	if elem, ok := c.items[key]; ok {
		c.eviction.MoveToFront(elem)
		entry := elem.Value.(*lruEntry)
		entry.value = value
		if c.ttl > 0 {
			entry.expiresAt = time.Now().Add(c.ttl)
		}
		return
	}

	// 容量已满：从链表尾部淘汰最久未使用的条目
	for c.eviction.Len() >= c.maxSize {
		oldest := c.eviction.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(*lruEntry)
		delete(c.items, entry.key)
		c.eviction.Remove(oldest)
	}

	entry := &lruEntry{key: key, value: value}
	if c.ttl > 0 {
		entry.expiresAt = time.Now().Add(c.ttl)
	}
	elem := c.eviction.PushFront(entry)
	c.items[key] = elem
}

func (c *lruCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *lruCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element)
	c.eviction.Init()
}

// Stop 停止后台清理协程
func (c *lruCache) Stop() {
	select {
	case <-c.stopCh:
		// 已经停止
	default:
		close(c.stopCh)
	}
}

// backgroundCleanup 后台过期清理协程
// 每 5 分钟从链表尾部开始扫描（尾部是最久未访问的条目，最可能已过期），
// 一次最多清理 1000 个过期条目，避免长时间持锁阻塞热路径
func (c *lruCache) backgroundCleanup() {
	cleanupInterval := 5 * time.Minute
	if c.ttl < cleanupInterval {
		cleanupInterval = c.ttl // TTL 较短时加快清理频率
	}

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.cleanExpired(1000)
		}
	}
}

// cleanExpired 从链表尾部开始批量清除过期条目
// maxClean 限制单次清理数量，防止长时间持锁
func (c *lruCache) cleanExpired(maxClean int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	cleaned := 0

	// 从尾部（最久未使用）开始扫描
	for elem := c.eviction.Back(); elem != nil && cleaned < maxClean; {
		entry := elem.Value.(*lruEntry)
		prev := elem.Prev() // 先保存前驱，因为 Remove 会断开链接

		if c.ttl > 0 && now.After(entry.expiresAt) {
			delete(c.items, entry.key)
			c.eviction.Remove(elem)
			cleaned++
		}

		elem = prev
	}

	if cleaned > 0 {
		logrus.Debugf("[LRUCache] Background cleanup: removed %d expired entries, remaining %d", cleaned, len(c.items))
	}
}

// --- 全局缓存实例 ---

var (
	globalCache   *EmbeddingCache
	globalCacheMu sync.RWMutex
)

// InitGlobalCache 初始化全局缓存
func InitGlobalCache(config CacheConfig, redisClient redisCli.UniversalClient) *EmbeddingCache {
	globalCacheMu.Lock()
	defer globalCacheMu.Unlock()

	// 停止旧 cache 的后台清理协程，防止 goroutine 泄漏（问题 6）
	if globalCache != nil {
		globalCache.local.Stop()
	}

	globalCache = NewEmbeddingCache(config, redisClient)
	logrus.Infof("[EmbeddingCache] Global cache initialized (local_max=%d, ttl=%v, redis=%v, dedup=%v)",
		config.LocalMaxSize, config.LocalTTL, config.RedisEnabled, config.DeduplicateOn)
	return globalCache
}

// GetGlobalCache 获取全局缓存
func GetGlobalCache() *EmbeddingCache {
	globalCacheMu.RLock()
	defer globalCacheMu.RUnlock()
	return globalCache
}

// CachedEmbedStrings 带缓存的 Embedding 包级别函数
// 流程：GetBatch 查缓存 → 仅对 miss 部分调用 API → 将新结果写入缓存 → 合并返回
// 这样 N 个文本中有 M 个缓存命中，则只需调用 API 处理 N-M 个，显著降低延迟和费用
func CachedEmbedStrings(ctx context.Context, texts []string) ([][]float64, error) {
	cache := GetGlobalCache()
	if cache == nil || !cache.config.Enabled {
		return EmbedStrings(ctx, texts)
	}

	cached, missedIdx := cache.GetBatch(ctx, texts)
	if len(missedIdx) == 0 {
		// 全部命中，无需调用 API
		result := make([][]float64, len(texts))
		for i, vec := range cached {
			result[i] = vec
		}
		return result, nil
	}

	// 仅对未命中的文本调用 Embedding API
	missedTexts := make([]string, len(missedIdx))
	for i, idx := range missedIdx {
		missedTexts[i] = texts[idx]
	}

	newVectors, err := EmbedStrings(ctx, missedTexts)
	if err != nil {
		return nil, err
	}

	// 将新向量写入缓存并合并到结果集
	for i, idx := range missedIdx {
		if i < len(newVectors) {
			cached[idx] = newVectors[i]
			cache.Put(ctx, texts[idx], newVectors[i])
		}
	}

	result := make([][]float64, len(texts))
	for i := range texts {
		if vec, ok := cached[i]; ok {
			result[i] = vec
		} else {
			return nil, fmt.Errorf("missing embedding for index %d", i)
		}
	}

	return result, nil
}
