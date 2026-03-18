/*
┌──────────────────────────────────────────────────────────────────────────────┐
│ migration.go — Redis 索引 Schema 版本化与蓝绿迁移                              │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标:                                                                       │
│  - 记录索引 Schema 元信息，支持版本升级                                      │
│  - 通过“蓝绿重建 + 别名切换”实现零停机迁移                                   │
│                                                                              │
│ 结构:                                                                       │
│  - 常量: SchemaVersion                                                      │
│  - 类型: MigrationConfig / IndexSchema / SchemaField / Migrator             │
│  - 构造: DefaultMigrationConfig / NewMigrator                               │
│  - 关键方法:                                                                │
│      GetCurrentSchema()  读取当前 schema 元信息                             │
│      BuildDesiredSchema() 根据配置生成期望 schema                            │
│      NeedsMigration()    判断是否需要迁移                                   │
│      MigrateIndex()       蓝绿迁移流程（新建索引→复制→切别名）               │
│      MigrateAllOnStartup() 启动时批量检查并迁移                              │
│      SaveSchemaForConfig() 索引创建时写入 schema 元数据                      │
└──────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	redisCli "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

const SchemaVersion = 2

// MigrationConfig 迁移配置
type MigrationConfig struct {
	Enabled              bool   `toml:"enabled"`
	AutoMigrateOnStartup bool   `toml:"auto_migrate_on_startup"`
	MetaPrefix           string `toml:"meta_prefix"`
	BatchSize            int    `toml:"batch_size"`
}

// DefaultMigrationConfig 默认迁移配置
func DefaultMigrationConfig() MigrationConfig {
	return MigrationConfig{
		Enabled:              true,
		AutoMigrateOnStartup: true,
		MetaPrefix:           "rag:schema:",
		BatchSize:            1000,
	}
}

// IndexSchema 索引 Schema 元信息
type IndexSchema struct {
	Version    int            `json:"version"`
	Algorithm  IndexAlgorithm `json:"algorithm"`
	Dimension  int            `json:"dimension"`
	HNSWParams *HNSWParams    `json:"hnsw_params,omitempty"`
	Fields     []SchemaField  `json:"fields"`
	CreatedAt  time.Time      `json:"created_at"`
}

// SchemaField 索引字段定义
type SchemaField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Migrator 索引 Schema 迁移引擎
type Migrator struct {
	store      VectorStore
	redis      redisCli.UniversalClient
	retCfg     *RetrieverConfig
	cfg        MigrationConfig
	metaPrefix string
	batchSize  int
}

// NewMigrator 创建迁移器
func NewMigrator(store VectorStore, redis redisCli.UniversalClient, retCfg *RetrieverConfig, cfg MigrationConfig) *Migrator {
	metaPrefix := cfg.MetaPrefix
	if metaPrefix == "" {
		metaPrefix = "rag:schema:"
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}
	return &Migrator{
		store:      store,
		redis:      redis,
		retCfg:     retCfg,
		cfg:        cfg,
		metaPrefix: metaPrefix,
		batchSize:  batchSize,
	}
}

// GetCurrentSchema 读取索引的当前 schema 元信息
func (m *Migrator) GetCurrentSchema(ctx context.Context, indexName string) (*IndexSchema, error) {
	key := m.metaPrefix + indexName
	data, err := m.redis.Get(ctx, key).Bytes()
	if err != nil {
		if err == redisCli.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get schema metadata for %s: %w", indexName, err)
	}
	var schema IndexSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("unmarshal schema for %s: %w", indexName, err)
	}
	return &schema, nil
}

// BuildDesiredSchema 根据当前配置生成期望的 schema
func (m *Migrator) BuildDesiredSchema() *IndexSchema {
	algo := IndexAlgorithm(m.retCfg.IndexAlgorithm)
	if algo == "" {
		algo = IndexAlgorithmFLAT
	}

	schema := &IndexSchema{
		Version:   SchemaVersion,
		Algorithm: algo,
		Dimension: m.retCfg.Dimension,
		Fields: []SchemaField{
			{Name: "content", Type: "TEXT"},
			{Name: "file_id", Type: "TAG"},
			{Name: "file_name", Type: "TEXT"},
			{Name: "chunk_id", Type: "TAG"},
			{Name: "chunk_index", Type: "NUMERIC"},
			{Name: m.retCfg.VectorFieldName, Type: "VECTOR"},
		},
		CreatedAt: time.Now(),
	}

	if algo == IndexAlgorithmHNSW {
		schema.HNSWParams = m.retCfg.HNSWParams
		if schema.HNSWParams == nil {
			schema.HNSWParams = DefaultHNSWParams()
		}
	}

	return schema
}

// NeedsMigration 检查是否需要迁移
func (m *Migrator) NeedsMigration(current, desired *IndexSchema) bool {
	if current == nil {
		return false
	}
	if current.Version != desired.Version {
		return true
	}
	if current.Algorithm != desired.Algorithm {
		return true
	}
	if current.Dimension != desired.Dimension && desired.Dimension > 0 && current.Dimension > 0 {
		return true
	}
	return false
}

// SaveSchema 保存 schema 元信息
func (m *Migrator) SaveSchema(ctx context.Context, indexName string, schema *IndexSchema) error {
	key := m.metaPrefix + indexName
	data, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}
	return m.redis.Set(ctx, key, data, 0).Err()
}

// MigrateIndex 对单个用户索引执行蓝绿迁移
func (m *Migrator) MigrateIndex(ctx context.Context, indexName, prefix string) error {
	current, err := m.GetCurrentSchema(ctx, indexName)
	if err != nil {
		return err
	}

	desired := m.BuildDesiredSchema()

	if !m.NeedsMigration(current, desired) {
		return nil
	}

	logrus.Infof("[Migrator] Migration needed for %s: v%d -> v%d (algo: %s -> %s)",
		indexName, current.Version, desired.Version, current.Algorithm, desired.Algorithm)

	// 版本化命名：新索引名带 _vN 后缀，旧索引名将作为 alias 指向新索引
	newIndexName := fmt.Sprintf("%s_v%d", indexName, desired.Version)
	newPrefix := fmt.Sprintf("%s_v%d:", strings.TrimSuffix(prefix, ":"), desired.Version)

	vectorField := m.retCfg.VectorFieldName
	if vectorField == "" {
		vectorField = "vector"
	}

	// 1. 创建新索引
	newConfig := IndexConfig{
		IndexName:       newIndexName,
		Prefix:          newPrefix,
		VectorFieldName: vectorField,
		Dimension:       desired.Dimension,
		Algorithm:       desired.Algorithm,
		HNSWParams:      desired.HNSWParams,
	}
	if err := m.store.EnsureIndex(ctx, newConfig); err != nil {
		return fmt.Errorf("create new index %s: %w", newIndexName, err)
	}
	logrus.Infof("[Migrator] Created new index: %s", newIndexName)

	// 2. SCAN + COPY 数据（保持旧索引在线，避免停机）
	copied, err := m.copyData(ctx, prefix, newPrefix)
	if err != nil {
		return fmt.Errorf("copy data from %s to %s: %w", prefix, newPrefix, err)
	}
	logrus.Infof("[Migrator] Copied %d keys from %s to %s", copied, prefix, newPrefix)

	// 3. 删除旧索引定义（DD 保留旧数据，避免误删）
	if err := m.dropIndex(ctx, indexName); err != nil {
		logrus.Warnf("[Migrator] Failed to drop old index %s: %v", indexName, err)
	}

	// 4. 创建别名：原始索引名保持不变，对上层完全透明
	if err := m.createAlias(ctx, indexName, newIndexName); err != nil {
		logrus.Warnf("[Migrator] Alias creation failed (non-critical): %v", err)
	}

	// 5. 更新 schema 元信息
	if err := m.SaveSchema(ctx, indexName, desired); err != nil {
		return fmt.Errorf("save schema: %w", err)
	}

	logrus.Infof("[Migrator] Migration complete for %s (v%d -> v%d, %d keys copied)",
		indexName, current.Version, desired.Version, copied)
	return nil
}

// copyData 使用 SCAN + Pipeline 批量复制 Hash 数据到新前缀
func (m *Migrator) copyData(ctx context.Context, oldPrefix, newPrefix string) (int64, error) {
	var cursor uint64
	var totalCopied int64

	for {
		keys, nextCursor, err := m.redis.Scan(ctx, cursor, oldPrefix+"*", int64(m.batchSize)).Result()
		if err != nil {
			return totalCopied, fmt.Errorf("scan: %w", err)
		}

		if len(keys) > 0 {
			// Pipeline 批量 COPY：减少 RTT，提高迁移速度
			pipe := m.redis.Pipeline()
			for _, key := range keys {
				suffix := strings.TrimPrefix(key, oldPrefix)
				newKey := newPrefix + suffix
				pipe.Copy(ctx, key, newKey, 0, true)
			}
			cmds, err := pipe.Exec(ctx)
			if err != nil {
				logrus.Warnf("[Migrator] Pipeline copy batch error: %v", err)
			}
			for _, cmd := range cmds {
				if cmd.Err() == nil {
					totalCopied++
				}
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return totalCopied, nil
}

// dropIndex 删除 RediSearch 索引定义（不删除底层数据）
func (m *Migrator) dropIndex(ctx context.Context, indexName string) error {
	// DD = Don't Delete, 仅删索引定义保留数据
	_, err := m.redis.Do(ctx, "FT.DROPINDEX", indexName, "DD").Result()
	if err != nil && !strings.Contains(err.Error(), "Unknown index name") {
		return err
	}
	return nil
}

// createAlias 创建或更新索引别名
func (m *Migrator) createAlias(ctx context.Context, alias, targetIndex string) error {
	// 先尝试更新
	_, err := m.redis.Do(ctx, "FT.ALIASUPDATE", alias, targetIndex).Result()
	if err == nil {
		return nil
	}
	// 更新失败则创建
	_, err = m.redis.Do(ctx, "FT.ALIASADD", alias, targetIndex).Result()
	return err
}

// MigrateAllOnStartup 启动时检查所有已知索引
func (m *Migrator) MigrateAllOnStartup(ctx context.Context) error {
	pattern := m.metaPrefix + "*"
	var cursor uint64
	var checked, migrated int

	for {
		keys, nextCursor, err := m.redis.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("scan schema keys: %w", err)
		}

		for _, key := range keys {
			indexName := strings.TrimPrefix(key, m.metaPrefix)
			if strings.Contains(indexName, "_v") {
				continue
			}

			checked++
			current, err := m.GetCurrentSchema(ctx, indexName)
			if err != nil {
				logrus.Warnf("[Migrator] Failed to read schema for %s: %v", indexName, err)
				continue
			}

			desired := m.BuildDesiredSchema()
			if m.NeedsMigration(current, desired) {
				prefix := strings.Replace(indexName, ":idx", ":", 1)
				if err := m.MigrateIndex(ctx, indexName, prefix); err != nil {
					logrus.Errorf("[Migrator] Migration failed for %s: %v", indexName, err)
				} else {
					migrated++
				}
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	logrus.Infof("[Migrator] Startup check complete: %d indexes checked, %d migrated (schema version: %d)",
		checked, migrated, SchemaVersion)
	return nil
}

// SaveSchemaForConfig 在创建索引时保存 schema 元信息（供 VectorStore 调用）
func SaveSchemaForConfig(ctx context.Context, redis redisCli.UniversalClient, metaPrefix string, config IndexConfig) error {
	if metaPrefix == "" {
		metaPrefix = "rag:schema:"
	}

	schema := &IndexSchema{
		Version:    SchemaVersion,
		Algorithm:  config.Algorithm,
		Dimension:  config.Dimension,
		HNSWParams: config.HNSWParams,
		Fields: []SchemaField{
			{Name: "content", Type: "TEXT"},
			{Name: "file_id", Type: "TAG"},
			{Name: "file_name", Type: "TEXT"},
			{Name: "chunk_id", Type: "TAG"},
			{Name: "chunk_index", Type: "NUMERIC"},
			{Name: config.VectorFieldName, Type: "VECTOR"},
		},
		CreatedAt: time.Now(),
	}

	data, err := json.Marshal(schema)
	if err != nil {
		return err
	}

	key := metaPrefix + config.IndexName
	return redis.Set(ctx, key, data, 0).Err()
}
