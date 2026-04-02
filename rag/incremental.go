package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	redisCli "github.com/redis/go-redis/v9"
)

// ──────────────────────────────────────────────────────────────────────────────
//  增量索引 — P3 功能增强
//
//  通过内容哈希检测文档是否变更，仅对变更的文档重新索引，
//  大幅减少重复索引的 Embedding API 调用和 Redis 写入量。
//
//  存储结构：
//    Redis Hash: doc:version:{user_id}
//      field = file_id, value = JSON{hash, version, chunks, updated_at}
//
//  工作流程：
//    1. 计算文档内容 SHA-256 哈希
//    2. 与 Redis 中存储的哈希对比
//    3. 哈希相同 → 跳过（返回 "unchanged"）
//    4. 哈希不同 → 删除旧 chunks → 重新索引 → 更新版本记录
// ──────────────────────────────────────────────────────────────────────────────

// DocumentVersion 文档版本记录
type DocumentVersion struct {
	FileID    string    `json:"file_id"`
	Hash      string    `json:"hash"`       // 内容 SHA-256 哈希
	Version   int       `json:"version"`    // 递增版本号
	Chunks    int       `json:"chunks"`     // 当前版本的 chunk 数量
	UpdatedAt time.Time `json:"updated_at"` // 最后更新时间
}

// IncrementalIndexer 增量索引器
type IncrementalIndexer struct {
	redis     redisCli.UniversalClient
	keyPrefix string // 默认 "doc:version"
}

// NewIncrementalIndexer 创建增量索引器
func NewIncrementalIndexer(redis redisCli.UniversalClient) *IncrementalIndexer {
	return &IncrementalIndexer{
		redis:     redis,
		keyPrefix: "doc:version",
	}
}

// IncrementalResult 增量索引结果
type IncrementalResult struct {
	Action   string `json:"action"` // "indexed", "unchanged", "updated"
	FileID   string `json:"file_id"`
	Hash     string `json:"hash"`
	Version  int    `json:"version"`
	Chunks   int    `json:"chunks"`
	Duration string `json:"duration,omitempty"`
}

// ContentHash 计算文档内容的 SHA-256 哈希
func ContentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// versionKey 构造 Redis key
func (idx *IncrementalIndexer) versionKey(userID int64) string {
	return fmt.Sprintf("%s:%d", idx.keyPrefix, userID)
}

// GetVersion 获取文档版本信息
func (idx *IncrementalIndexer) GetVersion(ctx context.Context, userID int64, fileID string) (*DocumentVersion, error) {
	key := idx.versionKey(userID)
	data, err := idx.redis.HGet(ctx, key, fileID).Result()
	if err != nil {
		if err == redisCli.Nil {
			return nil, nil // 不存在
		}
		return nil, fmt.Errorf("get version: %w", err)
	}

	var ver DocumentVersion
	if err := jsonUnmarshal([]byte(data), &ver); err != nil {
		return nil, fmt.Errorf("unmarshal version: %w", err)
	}
	return &ver, nil
}

// SetVersion 保存文档版本信息
func (idx *IncrementalIndexer) SetVersion(ctx context.Context, userID int64, ver DocumentVersion) error {
	key := idx.versionKey(userID)
	data, err := jsonMarshal(ver)
	if err != nil {
		return fmt.Errorf("marshal version: %w", err)
	}
	return idx.redis.HSet(ctx, key, ver.FileID, string(data)).Err()
}

// DeleteVersion 删除文档版本记录
func (idx *IncrementalIndexer) DeleteVersion(ctx context.Context, userID int64, fileID string) error {
	key := idx.versionKey(userID)
	return idx.redis.HDel(ctx, key, fileID).Err()
}

// NeedsReindex 判断文档是否需要重新索引
// 返回: needsReindex, currentVersion, error
func (idx *IncrementalIndexer) NeedsReindex(ctx context.Context, userID int64, fileID string, contentHash string) (bool, *DocumentVersion, error) {
	ver, err := idx.GetVersion(ctx, userID, fileID)
	if err != nil {
		return true, nil, err // 出错时默认需要重新索引
	}
	if ver == nil {
		return true, nil, nil // 首次索引
	}
	if ver.Hash != contentHash {
		return true, ver, nil // 内容已变更
	}
	return false, ver, nil // 未变更
}

// ListVersions 列出用户的所有文档版本
func (idx *IncrementalIndexer) ListVersions(ctx context.Context, userID int64) ([]DocumentVersion, error) {
	key := idx.versionKey(userID)
	data, err := idx.redis.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}

	versions := make([]DocumentVersion, 0, len(data))
	for _, v := range data {
		var ver DocumentVersion
		if err := jsonUnmarshal([]byte(v), &ver); err != nil {
			continue
		}
		versions = append(versions, ver)
	}
	return versions, nil
}

// IndexIfChanged 增量索引：仅在文档内容变更时重新索引
// 这是面向工具层的高级 API，封装了完整的增量索引工作流。
func (idx *IncrementalIndexer) IndexIfChanged(
	ctx context.Context,
	retriever *MultiFileRetriever,
	userID int64,
	fileID string,
	fileName string,
	content string,
	format DocumentFormat,
) (*IncrementalResult, error) {
	start := time.Now()
	hash := ContentHash(content)

	// 1. 检查是否需要重新索引
	needsReindex, existingVer, err := idx.NeedsReindex(ctx, userID, fileID, hash)
	if err != nil {
		log.Printf("[IncrementalIndex] Warning: version check failed for %s: %v, proceeding with full index", fileID, err)
	}

	if !needsReindex && existingVer != nil {
		return &IncrementalResult{
			Action:  "unchanged",
			FileID:  fileID,
			Hash:    hash,
			Version: existingVer.Version,
			Chunks:  existingVer.Chunks,
		}, nil
	}

	// 2. 执行索引（IndexDocument 签名: ctx, fileID, fileName, content）
	indexResult, err := retriever.IndexDocument(ctx, fileID, fileName, content)
	if err != nil {
		return nil, err
	}
	chunkCount := indexResult.TotalChunks

	// 3. 计算新版本号
	newVersion := 1
	action := "indexed"
	if existingVer != nil {
		newVersion = existingVer.Version + 1
		action = "updated"
	}

	// 4. 保存版本信息
	ver := DocumentVersion{
		FileID:    fileID,
		Hash:      hash,
		Version:   newVersion,
		Chunks:    chunkCount,
		UpdatedAt: time.Now(),
	}
	if err := idx.SetVersion(ctx, userID, ver); err != nil {
		log.Printf("[IncrementalIndex] Warning: failed to save version for %s: %v", fileID, err)
	}

	return &IncrementalResult{
		Action:   action,
		FileID:   fileID,
		Hash:     hash,
		Version:  newVersion,
		Chunks:   chunkCount,
		Duration: time.Since(start).Round(time.Millisecond).String(),
	}, nil
}

// ── JSON 工具 ────────────────────────────────────────────────────────────────

func jsonMarshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
