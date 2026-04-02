package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	redisCli "github.com/redis/go-redis/v9"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Redis Session Manager — P2 Session 外部化
//
//  实现 mcp-go 的 SessionIdManager 接口，将 Session ID 的生成/验证/终止
//  全部委托给 Redis，从而支持多实例部署（无需 sticky sessions）。
//
//  mcp-go SessionIdManager 接口:
//    Generate() string
//    Validate(sessionID string) (isTerminated bool, err error)
//    Terminate(sessionID string) (isNotAllowed bool, err error)
//
//  Redis 存储结构:
//    Key: mcp:session:{sessionID}
//    Value: "active" | "terminated"
//    TTL: 24h（可配置）
//
//  使用方式:
//    import "github.com/mark3labs/mcp-go/server"
//    sessionMgr := middleware.NewRedisSessionManager(redisClient, cfg)
//    httpServer := server.NewStreamableHTTPServer(mcpServer,
//        server.WithSessionIdManager(sessionMgr),
//    )
// ──────────────────────────────────────────────────────────────────────────────

// RedisSessionConfig Redis Session 配置
type RedisSessionConfig struct {
	KeyPrefix string        `toml:"key_prefix"` // 默认 "mcp:session"
	TTL       time.Duration `toml:"ttl"`        // Session 过期时间，默认 24h
	IDLength  int           `toml:"id_length"`  // Session ID 长度（字节），默认 16
}

// DefaultRedisSessionConfig 默认 Redis Session 配置
func DefaultRedisSessionConfig() RedisSessionConfig {
	return RedisSessionConfig{
		KeyPrefix: "mcp:session",
		TTL:       24 * time.Hour,
		IDLength:  16,
	}
}

// RedisSessionManager 实现 mcp-go server.SessionIdManager 接口
// 将 Session 状态存储在 Redis 中，支持多实例部署
type RedisSessionManager struct {
	redis  redisCli.UniversalClient
	config RedisSessionConfig
}

// NewRedisSessionManager 创建 Redis Session Manager
func NewRedisSessionManager(redis redisCli.UniversalClient, cfg RedisSessionConfig) *RedisSessionManager {
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "mcp:session"
	}
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	if cfg.IDLength <= 0 {
		cfg.IDLength = 16
	}
	log.Printf("[Session] Redis session manager enabled (prefix=%s, ttl=%v)", cfg.KeyPrefix, cfg.TTL)
	return &RedisSessionManager{redis: redis, config: cfg}
}

// sessionKey 构造 Redis key
func (m *RedisSessionManager) sessionKey(sessionID string) string {
	return fmt.Sprintf("%s:%s", m.config.KeyPrefix, sessionID)
}

// Generate 生成新 Session ID 并存入 Redis
// 实现 mcp-go server.SessionIdManager 接口
func (m *RedisSessionManager) Generate() string {
	id := generateSecureID(m.config.IDLength)
	key := m.sessionKey(id)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.redis.Set(ctx, key, "active", m.config.TTL).Err()
	if err != nil {
		log.Printf("[Session] Warning: failed to save session %s to Redis: %v", id, err)
		// 即使 Redis 写入失败，也返回 ID（降级为内存模式）
	}

	return id
}

// Validate 验证 Session ID 是否有效
// 返回 isTerminated=true 表示已终止，err!=nil 表示无效
// 实现 mcp-go server.SessionIdManager 接口
func (m *RedisSessionManager) Validate(sessionID string) (bool, error) {
	key := m.sessionKey(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	val, err := m.redis.Get(ctx, key).Result()
	if err != nil {
		if err == redisCli.Nil {
			return false, fmt.Errorf("session not found: %s", sessionID)
		}
		// Redis 错误时，降级允许（避免 Redis 故障导致全部请求失败）
		log.Printf("[Session] Warning: Redis lookup failed for %s: %v, allowing", sessionID, err)
		return false, nil
	}

	if val == "terminated" {
		return true, nil
	}

	// 刷新 TTL（延长活跃 session 的生命周期）
	m.redis.Expire(ctx, key, m.config.TTL)

	return false, nil
}

// Terminate 标记 Session 为已终止
// 实现 mcp-go server.SessionIdManager 接口
func (m *RedisSessionManager) Terminate(sessionID string) (bool, error) {
	key := m.sessionKey(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 设为 terminated，保留 1h 以便后续查询
	err := m.redis.Set(ctx, key, "terminated", 1*time.Hour).Err()
	if err != nil {
		log.Printf("[Session] Warning: failed to terminate session %s: %v", sessionID, err)
		return false, err
	}

	return false, nil
}

// ActiveSessionCount 返回当前活跃 Session 数量（用于监控指标）
func (m *RedisSessionManager) ActiveSessionCount(ctx context.Context) (int64, error) {
	pattern := fmt.Sprintf("%s:*", m.config.KeyPrefix)
	var count int64

	iter := m.redis.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		val, err := m.redis.Get(ctx, key).Result()
		if err == nil && val == "active" {
			count++
		}
	}
	if err := iter.Err(); err != nil {
		return count, err
	}
	return count, nil
}

// generateSecureID 生成加密安全的随机 ID
func generateSecureID(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		// 极端情况：rand 失败，fallback 到时间戳
		return fmt.Sprintf("sess_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
