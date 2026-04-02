/*
┌──────────────────────────────────────────────────────────────────────────────┐
│ upload_store.go — 文件上传暂存服务                                            │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  核心思想: 绕过 MCP JSON 协议的大小限制，为大文件提供独立的 HTTP 上传通道。     │
│  上传后返回 upload_id，MCP 工具通过 upload_id 引用文件内容，无需在 JSON 中     │
│  传输完整文件。                                                               │
│                                                                              │
│  存储策略:                                                                    │
│    - < 5MB  → Redis String (SET key data EX ttl)，零磁盘依赖                 │
│    - ≥ 5MB  → 本地磁盘文件 + Redis 存元信息，避免 Redis 内存压力              │
│                                                                              │
│  自动清理: 后台 goroutine 定期扫描过期文件，TTL 默认 1 小时                    │
│                                                                              │
│  导出类型:                                                                    │
│    UploadStore     — 暂存服务主体                                             │
│    UploadMeta      — 上传文件元信息                                           │
│    UploadConfig    — 配置参数                                                 │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	redisCli "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

const (
	// redisThreshold 小于此大小存 Redis，否则存磁盘
	redisThreshold = 5 * 1024 * 1024 // 5MB
	// uploadKeyPrefix Redis key 前缀
	uploadKeyPrefix = "rag:upload:"
	// uploadMetaSuffix 元信息后缀
	uploadMetaSuffix = ":meta"
	// uploadDataSuffix 数据后缀 (小文件)
	uploadDataSuffix = ":data"
)

// UploadConfig 上传服务配置
type UploadConfig struct {
	Enabled            bool          `toml:"enabled"`
	MaxUploadSize      int64         `toml:"max_upload_size"`      // 最大上传大小，默认 100MB
	DiskPath           string        `toml:"disk_path"`            // 大文件暂存目录
	TTL                time.Duration `toml:"ttl"`                  // 过期时间，默认 1 小时
	AutoAsyncThreshold int           `toml:"auto_async_threshold"` // 自动异步阈值，默认 100KB
	CleanupInterval    time.Duration // 清理间隔（内部使用）
}

// DefaultUploadConfig 返回默认上传配置
func DefaultUploadConfig() UploadConfig {
	return UploadConfig{
		Enabled:            false,
		MaxUploadSize:      100 * 1024 * 1024, // 100MB
		DiskPath:           "/tmp/rag-uploads",
		TTL:                1 * time.Hour,
		AutoAsyncThreshold: 100 * 1024, // 100KB
		CleanupInterval:    10 * time.Minute,
	}
}

// UploadMeta 上传文件元信息
type UploadMeta struct {
	UploadID   string    `json:"upload_id"`
	FileName   string    `json:"file_name"`
	Format     string    `json:"format"`
	Size       int64     `json:"size"`
	OnDisk     bool      `json:"on_disk"` // true=磁盘存储, false=Redis存储
	DiskPath   string    `json:"disk_path,omitempty"`
	UploadedAt time.Time `json:"uploaded_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// UploadStore 文件上传暂存服务
type UploadStore struct {
	redis  redisCli.UniversalClient
	config UploadConfig
	mu     sync.RWMutex
	cancel context.CancelFunc
}

// NewUploadStore 创建上传暂存服务
func NewUploadStore(redis redisCli.UniversalClient, config UploadConfig) *UploadStore {
	if config.MaxUploadSize <= 0 {
		config.MaxUploadSize = 100 * 1024 * 1024
	}
	if config.TTL <= 0 {
		config.TTL = 1 * time.Hour
	}
	if config.DiskPath == "" {
		config.DiskPath = "/tmp/rag-uploads"
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = 10 * time.Minute
	}

	// 确保磁盘目录存在
	if err := os.MkdirAll(config.DiskPath, 0755); err != nil {
		logrus.Warnf("[UploadStore] Failed to create disk path %s: %v", config.DiskPath, err)
	}

	return &UploadStore{
		redis:  redis,
		config: config,
	}
}

// StartCleaner 启动后台清理 goroutine
func (s *UploadStore) StartCleaner(ctx context.Context) {
	cleanCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	go func() {
		ticker := time.NewTicker(s.config.CleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-cleanCtx.Done():
				logrus.Info("[UploadStore] Cleaner stopped")
				return
			case <-ticker.C:
				s.cleanup(cleanCtx)
			}
		}
	}()

	logrus.Infof("[UploadStore] Started (disk=%s, ttl=%s, max=%dMB)",
		s.config.DiskPath, s.config.TTL, s.config.MaxUploadSize/(1024*1024))
}

// Stop 停止清理 goroutine
func (s *UploadStore) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// GenerateID 生成唯一的 upload ID
func (s *UploadStore) GenerateID() string {
	return "upl_" + uuid.New().String()[:12]
}

// Save 保存上传文件
func (s *UploadStore) Save(ctx context.Context, uploadID string, data []byte, meta UploadMeta) error {
	meta.UploadID = uploadID
	meta.Size = int64(len(data))
	meta.UploadedAt = time.Now()
	meta.ExpiresAt = meta.UploadedAt.Add(s.config.TTL)

	if int64(len(data)) < redisThreshold {
		// 小文件: 存 Redis
		meta.OnDisk = false
		pipe := s.redis.Pipeline()
		dataKey := uploadKeyPrefix + uploadID + uploadDataSuffix
		pipe.Set(ctx, dataKey, data, s.config.TTL)

		metaJSON, _ := json.Marshal(meta)
		metaKey := uploadKeyPrefix + uploadID + uploadMetaSuffix
		pipe.Set(ctx, metaKey, metaJSON, s.config.TTL)

		_, err := pipe.Exec(ctx)
		if err != nil {
			return fmt.Errorf("save to redis: %w", err)
		}
	} else {
		// 大文件: 存磁盘 + Redis 元信息
		diskPath := filepath.Join(s.config.DiskPath, uploadID)
		if err := os.WriteFile(diskPath, data, 0644); err != nil {
			return fmt.Errorf("save to disk: %w", err)
		}

		meta.OnDisk = true
		meta.DiskPath = diskPath

		metaJSON, _ := json.Marshal(meta)
		metaKey := uploadKeyPrefix + uploadID + uploadMetaSuffix
		if err := s.redis.Set(ctx, metaKey, metaJSON, s.config.TTL).Err(); err != nil {
			// 回滚磁盘文件
			os.Remove(diskPath)
			return fmt.Errorf("save meta to redis: %w", err)
		}
	}

	logrus.Infof("[UploadStore] Saved upload %s: size=%d, on_disk=%v, file=%s",
		uploadID, len(data), meta.OnDisk, meta.FileName)
	return nil
}

// Load 加载上传文件内容
func (s *UploadStore) Load(ctx context.Context, uploadID string) ([]byte, UploadMeta, error) {
	// 加载元信息
	metaKey := uploadKeyPrefix + uploadID + uploadMetaSuffix
	metaJSON, err := s.redis.Get(ctx, metaKey).Bytes()
	if err != nil {
		return nil, UploadMeta{}, fmt.Errorf("upload not found or expired: %s", uploadID)
	}

	var meta UploadMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, UploadMeta{}, fmt.Errorf("invalid upload metadata: %w", err)
	}

	var data []byte
	if meta.OnDisk {
		// 从磁盘读取
		data, err = os.ReadFile(meta.DiskPath)
		if err != nil {
			return nil, meta, fmt.Errorf("read from disk: %w", err)
		}
	} else {
		// 从 Redis 读取
		dataKey := uploadKeyPrefix + uploadID + uploadDataSuffix
		data, err = s.redis.Get(ctx, dataKey).Bytes()
		if err != nil {
			return nil, meta, fmt.Errorf("read from redis: %w", err)
		}
	}

	return data, meta, nil
}

// Delete 删除上传文件
func (s *UploadStore) Delete(ctx context.Context, uploadID string) error {
	metaKey := uploadKeyPrefix + uploadID + uploadMetaSuffix
	metaJSON, err := s.redis.Get(ctx, metaKey).Bytes()

	// 删除 Redis keys
	dataKey := uploadKeyPrefix + uploadID + uploadDataSuffix
	s.redis.Del(ctx, metaKey, dataKey)

	// 如果有磁盘文件也删除
	if err == nil {
		var meta UploadMeta
		if json.Unmarshal(metaJSON, &meta) == nil && meta.OnDisk && meta.DiskPath != "" {
			os.Remove(meta.DiskPath)
		}
	}

	return nil
}

// cleanup 清理过期的上传文件
func (s *UploadStore) cleanup(ctx context.Context) {
	// 扫描磁盘目录中的过期文件
	entries, err := os.ReadDir(s.config.DiskPath)
	if err != nil {
		return
	}

	cleaned := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "upl_") {
			continue
		}

		// 检查 Redis 中元信息是否还存在（TTL 过期则自动删除）
		metaKey := uploadKeyPrefix + name + uploadMetaSuffix
		exists, _ := s.redis.Exists(ctx, metaKey).Result()
		if exists == 0 {
			// 元信息已过期，删除磁盘文件
			diskPath := filepath.Join(s.config.DiskPath, name)
			if err := os.Remove(diskPath); err == nil {
				cleaned++
			}
		}
	}

	if cleaned > 0 {
		logrus.Infof("[UploadStore] Cleaned %d expired disk files", cleaned)
	}
}

// GetConfig 返回配置（只读）
func (s *UploadStore) GetConfig() UploadConfig {
	return s.config
}

// DetectFormatByFileName 根据文件名检测文档格式
func DetectFormatByFileName(fileName string) DocumentFormat {
	ext := strings.ToLower(filepath.Ext(fileName))
	switch ext {
	case ".pdf":
		return FormatPDF
	case ".docx":
		return FormatDOCX
	case ".md", ".markdown":
		return FormatMarkdown
	case ".html", ".htm":
		return FormatHTML
	default:
		return FormatPlainText
	}
}
