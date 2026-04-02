package rag

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Multi-Tenant Quota Management — P1 数据隔离与资源控制
//
//  为每个租户（user_id）提供资源配额限制，防止单个租户耗尽共享资源。
//  支持文档数量、分块数量、存储大小、每日 Embedding 配额等多维度限制。
// ──────────────────────────────────────────────────────────────────────────────

// TenantQuota 租户资源配额
type TenantQuota struct {
	MaxDocuments      int   `json:"max_documents" toml:"max_documents"`                 // 最大文档数
	MaxChunksPerDoc   int   `json:"max_chunks_per_doc" toml:"max_chunks_per_doc"`       // 单文档最大分块数
	MaxTotalChunks    int   `json:"max_total_chunks" toml:"max_total_chunks"`           // 总分块数上限
	MaxStorageMB      int64 `json:"max_storage_mb" toml:"max_storage_mb"`               // 存储上限 (MB)
	RateLimitRPM      int   `json:"rate_limit_rpm" toml:"rate_limit_rpm"`               // 请求/分钟
	EmbeddingQuotaDay int   `json:"embedding_quota_daily" toml:"embedding_quota_daily"` // 每日 Embedding 配额
}

// DefaultTenantQuota 默认租户配额（宽松，适合开发环境）
func DefaultTenantQuota() TenantQuota {
	return TenantQuota{
		MaxDocuments:      100,
		MaxChunksPerDoc:   500,
		MaxTotalChunks:    10000,
		MaxStorageMB:      1024, // 1GB
		RateLimitRPM:      60,
		EmbeddingQuotaDay: 10000,
	}
}

// TenantUsage 租户当前使用量
type TenantUsage struct {
	Documents       int    `json:"documents"`
	TotalChunks     int    `json:"total_chunks"`
	StorageMB       int64  `json:"storage_mb"`
	EmbeddingsToday int    `json:"embeddings_today"`
	LastResetDate   string `json:"last_reset_date"`
}

// TenantManager 多租户配额管理器
type TenantManager struct {
	mu           sync.RWMutex
	quotas       map[uint]*TenantQuota // 每个租户的配额（nil 使用默认）
	usage        map[uint]*TenantUsage // 实时使用量追踪
	defaultQuota TenantQuota
	enabled      bool
}

// TenantManagerConfig 配额管理配置
type TenantManagerConfig struct {
	Enabled      bool        `toml:"enabled"`
	DefaultQuota TenantQuota `toml:"default_quota"`
}

// NewTenantManager 创建租户管理器
func NewTenantManager(cfg TenantManagerConfig) *TenantManager {
	dq := cfg.DefaultQuota
	if dq.MaxDocuments == 0 {
		dq = DefaultTenantQuota()
	}
	return &TenantManager{
		quotas:       make(map[uint]*TenantQuota),
		usage:        make(map[uint]*TenantUsage),
		defaultQuota: dq,
		enabled:      cfg.Enabled,
	}
}

// SetQuota 设置指定租户的配额（覆盖默认值）
func (tm *TenantManager) SetQuota(userID uint, quota TenantQuota) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.quotas[userID] = &quota
}

// GetQuota 获取租户配额（未设置则返回默认值）
func (tm *TenantManager) GetQuota(userID uint) TenantQuota {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	if q, ok := tm.quotas[userID]; ok {
		return *q
	}
	return tm.defaultQuota
}

// GetUsage 获取租户当前使用量
func (tm *TenantManager) GetUsage(userID uint) TenantUsage {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	if u, ok := tm.usage[userID]; ok {
		return *u
	}
	return TenantUsage{}
}

// CheckDocumentQuota 检查是否允许添加新文档
func (tm *TenantManager) CheckDocumentQuota(ctx context.Context, userID uint) error {
	if !tm.enabled {
		return nil
	}

	tm.mu.RLock()
	quota := tm.getQuotaLocked(userID)
	usage := tm.getUsageLocked(userID)
	tm.mu.RUnlock()

	if usage.Documents >= quota.MaxDocuments {
		logrus.Warnf("[Tenant] User %d hit document quota: %d/%d", userID, usage.Documents, quota.MaxDocuments)
		return fmt.Errorf("document quota exceeded: %d/%d documents", usage.Documents, quota.MaxDocuments)
	}
	return nil
}

// CheckEmbeddingQuota 检查是否允许生成 Embedding
func (tm *TenantManager) CheckEmbeddingQuota(ctx context.Context, userID uint, count int) error {
	if !tm.enabled {
		return nil
	}

	tm.mu.RLock()
	quota := tm.getQuotaLocked(userID)
	usage := tm.getUsageLocked(userID)
	tm.mu.RUnlock()

	// 检查每日 Embedding 配额，并在日期变更时自动重置
	today := time.Now().Format("2006-01-02")
	if usage.LastResetDate != today {
		tm.mu.Lock()
		u := tm.ensureUsageLocked(userID)
		u.EmbeddingsToday = 0
		u.LastResetDate = today
		tm.mu.Unlock()
	}

	if usage.EmbeddingsToday+count > quota.EmbeddingQuotaDay {
		return fmt.Errorf("daily embedding quota exceeded: %d+%d > %d",
			usage.EmbeddingsToday, count, quota.EmbeddingQuotaDay)
	}
	return nil
}

// RecordDocumentAdded 记录文档添加
func (tm *TenantManager) RecordDocumentAdded(userID uint, chunks int) {
	if !tm.enabled {
		return
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	u := tm.ensureUsageLocked(userID)
	u.Documents++
	u.TotalChunks += chunks
}

// RecordDocumentDeleted 记录文档删除
func (tm *TenantManager) RecordDocumentDeleted(userID uint, chunks int) {
	if !tm.enabled {
		return
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	u := tm.ensureUsageLocked(userID)
	u.Documents--
	u.TotalChunks -= chunks
	if u.Documents < 0 {
		u.Documents = 0
	}
	if u.TotalChunks < 0 {
		u.TotalChunks = 0
	}
}

// RecordEmbeddings 记录 Embedding 使用量
func (tm *TenantManager) RecordEmbeddings(userID uint, count int) {
	if !tm.enabled {
		return
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	u := tm.ensureUsageLocked(userID)
	u.EmbeddingsToday += count
}

func (tm *TenantManager) getQuotaLocked(userID uint) TenantQuota {
	if q, ok := tm.quotas[userID]; ok {
		return *q
	}
	return tm.defaultQuota
}

func (tm *TenantManager) getUsageLocked(userID uint) TenantUsage {
	if u, ok := tm.usage[userID]; ok {
		return *u
	}
	return TenantUsage{}
}

func (tm *TenantManager) ensureUsageLocked(userID uint) *TenantUsage {
	if u, ok := tm.usage[userID]; ok {
		return u
	}
	u := &TenantUsage{LastResetDate: time.Now().Format("2006-01-02")}
	tm.usage[userID] = u
	return u
}

// ── Global TenantManager ────────────────────────────────────────────────────

var (
	globalTenantManager     *TenantManager
	globalTenantManagerOnce sync.Once
)

// InitGlobalTenantManager 初始化全局租户管理器
func InitGlobalTenantManager(cfg TenantManagerConfig) *TenantManager {
	globalTenantManagerOnce.Do(func() {
		globalTenantManager = NewTenantManager(cfg)
		if cfg.Enabled {
			logrus.Infof("[TenantManager] Enabled (maxDocs=%d, maxChunks=%d, embQuota=%d/day)",
				cfg.DefaultQuota.MaxDocuments, cfg.DefaultQuota.MaxTotalChunks, cfg.DefaultQuota.EmbeddingQuotaDay)
		}
	})
	return globalTenantManager
}

// GetGlobalTenantManager 获取全局租户管理器（可能为 nil）
func GetGlobalTenantManager() *TenantManager {
	return globalTenantManager
}
