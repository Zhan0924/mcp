/*
================================================================================

	文件: vectorstore.go
	模块: rag
	职责: 向量数据库抽象层 —— 封装向量索引管理、写入、搜索、混合检索、删除等核心操作

	┌─────────────────────────────────────────────────────────────────────────────┐
	│                         整体架构                                           │
	│                                                                             │
	│  调用层 (tools/rag_tools.go)                                                │
	│       │                                                                     │
	│       ▼                                                                     │
	│  ┌──────────────┐    接口抽象层                                             │
	│  │ VectorStore  │◄── 面向接口编程，解耦具体实现                              │
	│  │  (interface) │    可替换为 Milvus / Pinecone / Qdrant / Weaviate 等      │
	│  └──────┬───────┘                                                           │
	│         │ 实现                                                              │
	│         ▼                                                                   │
	│  ┌──────────────────┐                                                       │
	│  │ RedisVectorStore │◄── 基于 Redis Stack (RediSearch + RedisJSON) 实现     │
	│  │   (struct)       │    使用 FT.CREATE / FT.SEARCH / Pipeline 等命令       │
	│  └──────────────────┘                                                       │
	└─────────────────────────────────────────────────────────────────────────────┘

	═══════════════════════════════════════════════════════════════════════════════
	接口 (Interfaces):
	═══════════════════════════════════════════════════════════════════════════════
	  VectorStore           — 向量数据库统一抽象接口
	    ├── EnsureIndex()   — 幂等创建索引（FLAT / HNSW 算法）
	    ├── UpsertVectors() — Pipeline 批量写入向量数据
	    ├── SearchVectors() — KNN 向量近邻搜索（FT.SEARCH）
	    ├── HybridSearch()  — 混合搜索（向量 + BM25 全文 → RRF 融合）
	    ├── DeleteByFileID()— 按文件 ID 批量删除
	    └── Close()         — 关闭连接

	═══════════════════════════════════════════════════════════════════════════════
	类型 (Types):
	═══════════════════════════════════════════════════════════════════════════════
	  IndexConfig           — 索引创建配置（名称、前缀、维度、算法、HNSW 参数）
	  IndexAlgorithm        — 索引算法枚举（FLAT / HNSW）
	  HNSWParams            — HNSW 算法超参数（M, EF_CONSTRUCTION, EF_RUNTIME）
	  VectorEntry           — 待写入的向量条目（Key + Fields）
	  VectorQuery           — 向量搜索请求参数
	  HybridQuery           — 混合搜索请求参数（组合 VectorQuery + 全文条件）
	  VectorSearchResult    — 搜索结果（Key + Fields + Score）
	  RedisVectorStore      — VectorStore 的 Redis 实现

	═══════════════════════════════════════════════════════════════════════════════
	常量 (Constants):
	═══════════════════════════════════════════════════════════════════════════════
	  IndexAlgorithmFLAT    — "FLAT" 暴力搜索算法
	  IndexAlgorithmHNSW    — "HNSW" 近似最近邻算法

	═══════════════════════════════════════════════════════════════════════════════
	函数 / 方法 (Functions / Methods):
	═══════════════════════════════════════════════════════════════════════════════
	  DefaultHNSWParams()                — 返回 HNSW 默认超参数
	  NewRedisVectorStore(client)        — 构造 RedisVectorStore 实例

	  (RedisVectorStore) EnsureIndex()   — 幂等创建 Redis 向量索引
	  (RedisVectorStore) UpsertVectors() — Pipeline 批量写入
	  (RedisVectorStore) SearchVectors() — KNN 向量搜索
	  (RedisVectorStore) HybridSearch()  — 混合检索（向量 + 全文 + RRF）
	  (RedisVectorStore) DeleteByFileID()— 按 file_id 标签删除文档向量
	  (RedisVectorStore) Close()         — 关闭（当前为空操作）

	  mergeByRRF()                       — RRF (Reciprocal Rank Fusion) 排名融合
	  parseRawSearchResult()             — 解析 FT.SEARCH 返回值（RESP2 / RESP3）
	  parseRawRESP3()                    — 解析 RESP3 格式的搜索结果
	  extractKeysFromSearch()            — 从搜索结果中提取文档 Key 列表
	  extractKeysFromRESP3()             — 从 RESP3 搜索结果中提取 Key 列表
	  escapeRedisQuery()                 — 转义 Redis 查询中的特殊字符
	  Float32SliceToBytes()              — float32 切片转 Little-Endian 字节序列

================================================================================
*/
package rag

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	redisCli "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// VectorStore 向量数据库抽象接口
//
// 【设计原则 — 依赖倒置 (DIP)】
// 上层业务（tools/rag_tools.go）仅依赖此接口，不感知底层是 Redis、Milvus 还是 Pinecone。
// 切换向量数据库只需实现此接口并注入即可，无需修改任何上层代码。
//
// 当前唯一实现: RedisVectorStore（基于 Redis Stack / RediSearch 模块）。
// 未来可扩展:
//   - MilvusVectorStore   → 大规模高性能场景（百亿级向量）
//   - PineconeVectorStore → 全托管 SaaS 方案
//   - QdrantVectorStore   → Rust 实现，低延迟场景
type VectorStore interface {
	// EnsureIndex 确保索引存在，不存在则按配置创建
	EnsureIndex(ctx context.Context, config IndexConfig) error

	// UpsertVectors 批量写入向量数据
	UpsertVectors(ctx context.Context, entries []VectorEntry) (int, error)

	// SearchVectors KNN 向量搜索
	SearchVectors(ctx context.Context, query VectorQuery) ([]VectorSearchResult, error)

	// HybridSearch 混合搜索 (向量 + 全文)
	HybridSearch(ctx context.Context, query HybridQuery) ([]VectorSearchResult, error)

	// DeleteByFileID 按 file_id 删除文档向量
	DeleteByFileID(ctx context.Context, indexName, prefix, fileID string) (int64, error)

	// GetDocumentChunks 按 file_id 获取文档的所有分块内容，按块序号排序
	GetDocumentChunks(ctx context.Context, indexName, prefix, fileID string) ([]string, error)

	// ListDocuments 列出指定索引中所有唯一的文档元信息
	// 通过 FT.AGGREGATE 对 file_id 做去重聚合，返回每个文件的 ID、名称和 chunk 数量
	ListDocuments(ctx context.Context, indexName string) ([]DocumentMeta, error)

	// Close 关闭连接
	Close() error
}

// DocumentMeta 文档元信息（用于 resources/list）
type DocumentMeta struct {
	FileID     string `json:"file_id"`
	FileName   string `json:"file_name"`
	ChunkCount int    `json:"chunk_count"`
}

// IndexConfig 索引创建配置
type IndexConfig struct {
	IndexName       string         // 索引名称（Redis FT.CREATE 的索引标识）
	Prefix          string         // Hash Key 前缀，用于限定索引的作用范围
	VectorFieldName string         // 向量字段名（默认 "vector"）
	Dimension       int            // 向量维度（必须与 Embedding 模型输出维度一致）
	Algorithm       IndexAlgorithm // 索引算法：FLAT 或 HNSW
	HNSWParams      *HNSWParams    // HNSW 算法专用参数（仅 Algorithm=HNSW 时生效）
}

// IndexAlgorithm 索引算法枚举类型
type IndexAlgorithm string

const (
	// IndexAlgorithmFLAT 暴力搜索（Brute-Force）
	// 【算法特性】精确搜索，O(N) 时间复杂度，无近似误差
	// 【适用场景】数据量较小（< 10 万条）或对召回率要求 100% 的场景
	// 【优点】零构建开销、零额外内存开销、结果完全精确
	// 【缺点】查询延迟随数据量线性增长，不适合大规模检索
	IndexAlgorithmFLAT IndexAlgorithm = "FLAT"

	// IndexAlgorithmHNSW 层级可导航小世界图（Hierarchical Navigable Small World）
	// 【算法特性】近似最近邻（ANN），O(log N) 时间复杂度
	// 【适用场景】数据量较大（> 10 万条）且可接受微小召回损失的场景
	// 【优点】查询速度快、可通过参数调节精度-速度权衡
	// 【缺点】需要额外内存存储图结构，构建索引耗时较长
	// 【论文】Malkov & Yashunin, "Efficient and Robust Approximate Nearest Neighbor using
	//         Hierarchical Navigable Small World Graphs", 2018
	IndexAlgorithmHNSW IndexAlgorithm = "HNSW"
)

// HNSWParams HNSW 算法超参数
//
// 【三个核心参数的权衡关系】
//
//	┌────────────────┬──────────────────────┬──────────────────────────────────┐
//	│ 参数            │ 作用                  │ 权衡                              │
//	├────────────────┼──────────────────────┼──────────────────────────────────┤
//	│ M              │ 每层节点最大出边数     │ ↑ 提高召回率 & 内存开销           │
//	│ EFConstruction │ 构建时搜索队列宽度     │ ↑ 提高索引质量 & 构建时间         │
//	│ EFRuntime      │ 查询时搜索队列宽度     │ ↑ 提高查询精度 & 查询延迟         │
//	└────────────────┴──────────────────────┴──────────────────────────────────┘
//
// 典型配置参考:
//   - 小规模 (<100K): M=16, EFC=200, EFR=10   （默认值，平衡型）
//   - 中规模 (100K-1M): M=32, EFC=400, EFR=50 （高召回）
//   - 大规模 (>1M): M=48, EFC=500, EFR=100    （高精度高召回）
type HNSWParams struct {
	M              int // 每层最大连接数（出边数），默认 16。越大召回率越高但内存占用也越大
	EFConstruction int // 构建索引时的搜索宽度，默认 200。影响索引质量，仅在构建阶段生效
	EFRuntime      int // 检索时的搜索宽度，默认 10。越大结果越精确但延迟越高，可动态调整
}

// DefaultHNSWParams 返回 HNSW 默认参数，适用于中小规模数据集
func DefaultHNSWParams() *HNSWParams {
	return &HNSWParams{
		M:              16,
		EFConstruction: 200,
		EFRuntime:      10,
	}
}

// VectorEntry 待写入的向量条目
type VectorEntry struct {
	Key    string                 // Redis Hash Key（通常格式: {prefix}{file_id}:{chunk_index}）
	Fields map[string]interface{} // Hash 字段集（content, vector, file_id, chunk_id 等）
}

// VectorQuery 向量搜索请求参数
type VectorQuery struct {
	IndexName       string   // 目标索引名称
	Vector          []byte   // 查询向量（Little-Endian float32 字节序列，由 Float32SliceToBytes 生成）
	TopK            int      // 返回最相似的 K 个结果
	VectorFieldName string   // 向量字段名（默认 "vector"）
	ReturnFields    []string // 需要返回的字段列表
	SearchDialect   int      // Redis 搜索方言版本（需 >= 2 才支持向量搜索语法）
	FilterQuery     string   // 前置过滤条件（RediSearch 查询语法，如 "@file_id:{xxx}"）
}

// HybridQuery 混合搜索请求参数
//
// 【设计思路】组合向量语义搜索与 BM25 全文关键词搜索，通过 RRF 融合排序。
// 这样可以同时利用语义相似性（向量）和精确关键词匹配（BM25）的优势。
type HybridQuery struct {
	VectorQuery            // 嵌入向量搜索参数
	TextQuery     string   // 全文搜索关键词（为空时退化为纯向量搜索）
	TextFields    []string // 全文搜索目标字段
	VectorWeight  float64  // 向量搜索权重（默认 0.7）
	KeywordWeight float64  // 关键词搜索权重（默认 0.3）
}

// VectorSearchResult 向量搜索结果（底层通用结构）
type VectorSearchResult struct {
	Key    string            // 文档 Key
	Fields map[string]string // 文档字段键值对
	Score  float64           // 相似度 / 融合得分（越高越相关）
}

// ═══════════════════════════════════════════════════════════════════════════════
// Redis 实现层
// ═══════════════════════════════════════════════════════════════════════════════

// RedisVectorStore 基于 Redis Stack (RediSearch 模块) 的向量存储实现
//
// 依赖 Redis Stack >= 7.2，需启用 RediSearch 和 RedisJSON 模块。
// 使用 UniversalClient 接口，同时兼容 Standalone / Sentinel / Cluster 部署模式。
type RedisVectorStore struct {
	client redisCli.UniversalClient
}

// NewRedisVectorStore 创建 Redis 向量存储实例
func NewRedisVectorStore(client redisCli.UniversalClient) *RedisVectorStore {
	return &RedisVectorStore{client: client}
}

// EnsureIndex 确保指定索引存在，若不存在则根据配置创建
//
// 【幂等性设计】
// 1. 先通过 FT.INFO 探测索引是否已存在 → 存在则直接返回（避免重复创建）
// 2. 若创建时遇到 "Index already exists" 错误（并发创建场景），同样视为成功
// 这保证了无论调用多少次，最终效果与调用一次相同 —— 符合幂等性原则。
//
// 【索引 Schema 设计】
// - content (TEXT, WEIGHT=1.0): 文本内容，用于 BM25 全文搜索
// - file_id (TAG):             文件标识，用于精确过滤和按文件删除
// - file_name (TEXT, NOINDEX): 文件名，仅存储不参与检索
// - chunk_id (TAG):            块标识，用于精确定位
// - chunk_index (NUMERIC):     块序号，用于排序和范围查询
// - vector (VECTOR):           向量字段，支持 KNN 搜索
//
// 【FLAT vs HNSW 算法选择】
// 根据 config.Algorithm 决定:
//   - FLAT:  参数少（TYPE, DIM, DISTANCE_METRIC），适合小数据量精确搜索
//   - HNSW:  额外需要 M 和 EF_CONSTRUCTION 参数，适合大规模近似搜索
func (s *RedisVectorStore) EnsureIndex(ctx context.Context, config IndexConfig) error {
	// 幂等检查：先探测索引是否已存在
	_, err := s.client.Do(ctx, "FT.INFO", config.IndexName).Result()
	if err == nil {
		logrus.Infof("[VectorStore] Index %s already exists", config.IndexName)
		return nil
	}

	logrus.Infof("[VectorStore] Creating index %s (algorithm=%s, dim=%d)",
		config.IndexName, config.Algorithm, config.Dimension)

	vectorField := config.VectorFieldName
	if vectorField == "" {
		vectorField = "vector"
	}

	algorithm := config.Algorithm
	if algorithm == "" {
		algorithm = IndexAlgorithmFLAT
	}

	// 构建 FT.CREATE 命令参数
	// ON HASH: 索引 Redis Hash 类型的数据
	// PREFIX 1 {prefix}: 只索引以指定前缀开头的 Key
	createArgs := []interface{}{
		"FT.CREATE", config.IndexName,
		"ON", "HASH",
		"PREFIX", "1", config.Prefix,
		"SCHEMA",
		"content", "TEXT", "WEIGHT", "1.0",
		"file_id", "TAG",
		"file_name", "TEXT", "NOINDEX",
		"chunk_id", "TAG",
		"chunk_index", "NUMERIC",
		"parent_chunk_id", "TAG",
	}

	switch algorithm {
	case IndexAlgorithmHNSW:
		// 【HNSW 索引参数说明】
		// "HNSW", "10" → 算法类型为 HNSW，后跟 10 个配置参数（5 对 key-value）
		// TYPE FLOAT32       → 向量元素类型为 32 位浮点数
		// DIM                → 向量维度（必须与 Embedding 模型输出一致）
		// DISTANCE_METRIC    → 距离度量：COSINE（余弦相似度，最常用于文本语义）
		// M                  → 图中每层节点最大连接数
		// EF_CONSTRUCTION    → 构建阶段搜索队列宽度（越大索引质量越高但构建越慢）
		params := config.HNSWParams
		if params == nil {
			params = DefaultHNSWParams()
		}
		createArgs = append(createArgs,
			vectorField, "VECTOR", "HNSW", "10",
			"TYPE", "FLOAT32",
			"DIM", fmt.Sprintf("%d", config.Dimension),
			"DISTANCE_METRIC", "COSINE",
			"M", fmt.Sprintf("%d", params.M),
			"EF_CONSTRUCTION", fmt.Sprintf("%d", params.EFConstruction),
		)
	default:
		// 【FLAT 索引参数说明】
		// "FLAT", "6" → 算法类型为 FLAT (暴力搜索)，后跟 6 个配置参数（3 对 key-value）
		// 暴力搜索会遍历所有向量计算距离，O(N) 复杂度，但结果 100% 精确
		createArgs = append(createArgs,
			vectorField, "VECTOR", "FLAT", "6",
			"TYPE", "FLOAT32",
			"DIM", fmt.Sprintf("%d", config.Dimension),
			"DISTANCE_METRIC", "COSINE",
		)
	}

	_, err = s.client.Do(ctx, createArgs...).Result()
	if err != nil {
		// 并发安全：多实例同时创建索引时，后到的请求会收到此错误
		if strings.Contains(err.Error(), "Index already exists") {
			logrus.Infof("[VectorStore] Index %s was created concurrently, ok", config.IndexName)
			return nil
		}
		return NewRAGError(ErrCodeIndexCreateFailed, config.IndexName, err)
	}

	logrus.Infof("[VectorStore] Successfully created index %s", config.IndexName)

	// 【Schema 元数据持久化 — 迁移支持】
	// 将当前索引的 Schema 版本信息保存到 Redis，供后续迁移 (migration.go) 使用。
	// 当 Schema 发生变更时，迁移模块可对比版本差异并自动执行升级。
	// 此操作失败不影响索引创建，仅记录警告日志。
	if err := SaveSchemaForConfig(ctx, s.client, "", config); err != nil {
		logrus.Warnf("[VectorStore] Failed to save schema metadata for %s: %v", config.IndexName, err)
	}

	return nil
}

// UpsertVectors 批量写入向量数据（Upsert = Update + Insert）
//
// 【Pipeline 批量写入原理】
// Redis Pipeline 将多条命令打包为一次网络请求发送，服务端批量执行后一次性返回所有结果。
// 相比逐条发送:
//   - 减少网络 RTT（Round-Trip Time），N 条命令从 N 次 RTT 降为 1 次
//   - 减少系统调用次数（send/recv），降低 CPU 开销
//   - 在高延迟网络下效果更显著（如跨机房部署）
//
// 【分批策略】
// 每批最多 500 条，避免单次 Pipeline 过大导致:
//   - Redis 服务端内存峰值过高
//   - 网络传输包过大被中间代理截断
//   - 单批失败影响范围过大
func (s *RedisVectorStore) UpsertVectors(ctx context.Context, entries []VectorEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	const pipelineBatchSize = 500
	indexed := 0

	for start := 0; start < len(entries); start += pipelineBatchSize {
		end := start + pipelineBatchSize
		if end > len(entries) {
			end = len(entries)
		}

		// 每批创建独立的 Pipeline，批内命令原子性执行
		pipe := s.client.Pipeline()
		for _, e := range entries[start:end] {
			// HSet 实现 Upsert 语义：Key 存在则更新字段，不存在则创建
			pipe.HSet(ctx, e.Key, e.Fields)
		}

		cmds, err := pipe.Exec(ctx)
		if err != nil {
			// Pipeline 部分失败不中断整体流程，逐条统计成功数
			logrus.Errorf("[VectorStore] Pipeline exec error (batch %d-%d): %v", start, end, err)
		}

		for _, cmd := range cmds {
			if cmd.Err() == nil {
				indexed++
			}
		}
	}

	return indexed, nil
}

// SearchVectors 执行 KNN 向量近邻搜索
//
// 【FT.SEARCH KNN 查询语法】
// 格式: FT.SEARCH {index} "{filter}=>[KNN {K} @{field} $vec AS distance]"
//
//	PARAMS 2 vec {blob} RETURN {n} {fields...} SORTBY distance ASC DIALECT 2
//
// 【Dialect 2 说明】
// Redis Search 的方言版本：
//   - Dialect 1: 旧版语法，不支持向量搜索
//   - Dialect 2: 支持 KNN 向量查询语法（"=>[KNN ...]"）和 VECTOR 类型
//   - Dialect 3: 支持更多高级功能（如 Aggregation Pipeline 中的向量操作）
//
// 本系统要求至少 Dialect 2。
//
// 【COSINE 距离解读】
// distance 值越小表示越相似（0 = 完全相同，2 = 完全相反）。
// SORTBY distance ASC 保证结果按相似度从高到低排列。
func (s *RedisVectorStore) SearchVectors(ctx context.Context, query VectorQuery) ([]VectorSearchResult, error) {
	vectorField := query.VectorFieldName
	if vectorField == "" {
		vectorField = "vector"
	}

	filterQuery := query.FilterQuery
	if filterQuery == "" {
		filterQuery = "*" // "*" 表示不做前置过滤，搜索全部文档
	}

	dialect := query.SearchDialect
	if dialect == 0 {
		dialect = 2 // 默认使用 Dialect 2 以支持向量搜索语法
	}

	// 构建 FT.SEARCH 命令
	// 查询表达式: "{filter}=>[KNN {TopK} @{vectorField} $vec AS distance]"
	// PARAMS 2 vec {blob}: 传入查询向量作为参数（$vec 引用）
	searchArgs := []interface{}{
		"FT.SEARCH", query.IndexName,
		fmt.Sprintf("%s=>[KNN %d @%s $vec AS distance]", filterQuery, query.TopK, vectorField),
		"PARAMS", "2", "vec", query.Vector,
	}
	searchArgs = append(searchArgs, "RETURN", fmt.Sprintf("%d", len(query.ReturnFields)))
	for _, field := range query.ReturnFields {
		searchArgs = append(searchArgs, field)
	}
	searchArgs = append(searchArgs, "SORTBY", "distance", "ASC")
	searchArgs = append(searchArgs, "DIALECT", fmt.Sprintf("%d", dialect))

	result, err := s.client.Do(ctx, searchArgs...).Result()
	if err != nil {
		// 索引不存在时返回空结果而非报错（防御性设计）
		if strings.Contains(err.Error(), "Unknown index name") || strings.Contains(err.Error(), "No such index") {
			return []VectorSearchResult{}, nil
		}
		return nil, NewRAGError(ErrCodeSearchFailed, query.IndexName, err)
	}

	return parseRawSearchResult(result)
}

// HybridSearch 执行混合检索（向量语义搜索 + BM25 全文关键词搜索 + RRF 融合）
//
// 【混合检索流水线 (Pipeline)】
//
//	┌──────────────┐     ┌──────────────┐
//	│ 向量语义搜索  │     │ BM25 全文搜索 │
//	│ (KNN TopK)   │     │ (TopK × 3)   │   ← 全文检索扩大候选范围以提高融合效果
//	└──────┬───────┘     └──────┬───────┘
//	       │                    │
//	       ▼                    ▼
//	  vectorResults        textResults
//	       │                    │
//	       └────────┬───────────┘
//	                ▼
//	         RRF 融合排序
//	        (mergeByRRF)
//	                │
//	                ▼
//	          最终 TopK 结果
//
// 【为什么要混合检索？】
// - 纯向量搜索: 擅长语义理解（"如何部署容器" ≈ "Docker 容器化方案"），但可能遗漏精确关键词匹配
// - 纯全文搜索: 擅长精确关键词匹配，但无法理解同义词和语义近似
// - 混合检索:   两者互补，显著提升检索质量（业界实践表明混合通常比单一方式好 5-15%）
func (s *RedisVectorStore) HybridSearch(ctx context.Context, query HybridQuery) ([]VectorSearchResult, error) {
	vectorField := query.VectorFieldName
	if vectorField == "" {
		vectorField = "vector"
	}

	dialect := query.SearchDialect
	if dialect == 0 {
		dialect = 2
	}

	// 第 1 步：向量搜索（语义维度）
	vectorResults, err := s.SearchVectors(ctx, query.VectorQuery)
	if err != nil {
		return nil, err
	}

	// 若无全文查询条件，退化为纯向量搜索
	if query.TextQuery == "" {
		return vectorResults, nil
	}

	// 第 2 步：BM25 全文搜索（关键词维度）
	// 对用户输入进行转义，防止 Redis 查询语法注入
	escapedText := escapeRedisQuery(query.TextQuery)
	textSearchQuery := fmt.Sprintf("@content:(%s)", escapedText)

	// 组合过滤条件（如果有）
	if query.FilterQuery != "" && query.FilterQuery != "*" {
		textSearchQuery = fmt.Sprintf("(%s) (%s)", query.FilterQuery, textSearchQuery)
	}

	// 全文搜索取 TopK × 3 个候选，扩大候选池以提高融合后的召回质量
	expandedTopK := query.TopK * 3
	textArgs := []interface{}{
		"FT.SEARCH", query.IndexName,
		textSearchQuery,
	}
	textArgs = append(textArgs, "RETURN", fmt.Sprintf("%d", len(query.ReturnFields)))
	for _, field := range query.ReturnFields {
		textArgs = append(textArgs, field)
	}
	textArgs = append(textArgs, "LIMIT", "0", fmt.Sprintf("%d", expandedTopK))
	textArgs = append(textArgs, "DIALECT", fmt.Sprintf("%d", dialect))

	textResult, err := s.client.Do(ctx, textArgs...).Result()
	var textResults []VectorSearchResult
	if err == nil {
		textResults, _ = parseRawSearchResult(textResult)
	} else {
		// 全文搜索失败时优雅降级为纯向量搜索
		logrus.Warnf("[VectorStore] Text search failed, falling back to vector-only: %v", err)
		return vectorResults, nil
	}

	// 第 3 步：使用 RRF (Reciprocal Rank Fusion) 融合两路搜索结果
	return mergeByRRF(vectorResults, textResults, query.VectorWeight, query.KeywordWeight, query.TopK), nil
}

// DeleteByFileID 按 file_id 删除文档的所有向量块
//
// 【实现策略: 先查后删】
// 1. 通过 FT.SEARCH @file_id:{fileID} 查找该文件的所有 Hash Key
// 2. 使用 Pipeline 批量 DEL 删除所有匹配的 Key
//
// 【为什么不用 FT.DROPINDEX？】
// FT.DROPINDEX 会删除整个索引，而我们只需删除特定文件的向量。
// 使用 TAG 类型的 file_id 字段做精确匹配，再逐条 DEL 实现细粒度删除。
//
// 【Pipeline DEL 的原子性】
// 虽然 Pipeline 本身不是原子操作（可能部分成功），但对于删除场景:
// - 部分删除失败不会造成数据不一致（重试即可）
// - 比逐条 DEL 大幅减少网络往返次数
func (s *RedisVectorStore) DeleteByFileID(ctx context.Context, indexName, prefix, fileID string) (int64, error) {
	// 转义 TAG 值中的特殊字符（如 . / 等）
	escapedID := escapeTagValue(fileID)
	searchQuery := fmt.Sprintf("@file_id:{%s}", escapedID)

	var totalDeleted int64
	const batchSize = 1000

	// 分页循环删除：每次查 batchSize 条 → Pipeline DEL → 直到无剩余
	// 解决旧版硬编码 10000 上限的问题，支持超大文档
	for {
		result, err := s.client.Do(ctx, "FT.SEARCH", indexName, searchQuery,
			"RETURN", "0", "LIMIT", "0", fmt.Sprintf("%d", batchSize)).Result()
		if err != nil {
			if strings.Contains(err.Error(), "Unknown index name") {
				return totalDeleted, nil
			}
			return totalDeleted, NewRAGError(ErrCodeSearchFailed, "delete scan for "+fileID, err)
		}

		keys := extractKeysFromSearch(result)
		if len(keys) == 0 {
			break // 无更多匹配项，删除完成
		}

		// Pipeline 批量删除
		pipe := s.client.Pipeline()
		for _, key := range keys {
			pipe.Del(ctx, key)
		}
		cmds, err := pipe.Exec(ctx)
		if err != nil {
			logrus.Warnf("[VectorStore] Partial delete error: %v", err)
		}

		for _, cmd := range cmds {
			if cmd.Err() == nil {
				totalDeleted++
			}
		}

		// 如果这批数量小于 batchSize，说明已是最后一批
		if len(keys) < batchSize {
			break
		}
	}

	return totalDeleted, nil
}

// GetDocumentChunks 按 file_id 获取文档的所有分块内容，按 chunk_index 升序排列
func (s *RedisVectorStore) GetDocumentChunks(ctx context.Context, indexName, prefix, fileID string) ([]string, error) {
	escapedID := escapeTagValue(fileID)
	searchQuery := fmt.Sprintf("@file_id:{%s}", escapedID)

	// SORTBY chunk_index ASC 保证分块顺序与原文一致
	// LIMIT 0 10000 假设一个文件最多 10000 块
	result, err := s.client.Do(ctx, "FT.SEARCH", indexName, searchQuery,
		"RETURN", "1", "content", "SORTBY", "chunk_index", "ASC", "LIMIT", "0", "10000").Result()
	if err != nil {
		if strings.Contains(err.Error(), "Unknown index name") {
			return nil, nil
		}
		return nil, NewRAGError(ErrCodeSearchFailed, "get chunks for "+fileID, err)
	}

	searchResults, err := parseRawSearchResult(result)
	if err != nil {
		return nil, err
	}

	chunks := make([]string, 0, len(searchResults))
	for _, res := range searchResults {
		if content, ok := res.Fields["content"]; ok {
			chunks = append(chunks, content)
		}
	}
	return chunks, nil
}

// Close 关闭向量存储连接
// 当前 Redis 连接生命周期由外部管理，此处为空操作
func (s *RedisVectorStore) Close() error {
	return nil
}

// ListDocuments 列出指定索引中所有唯一的文档元信息
// 使用 FT.AGGREGATE GROUPBY 对 file_id + file_name 去重聚合，统计每个文件的 chunk 数量
func (s *RedisVectorStore) ListDocuments(ctx context.Context, indexName string) ([]DocumentMeta, error) {
	result, err := s.client.Do(ctx,
		"FT.AGGREGATE", indexName, "*",
		"GROUPBY", "2", "@file_id", "@file_name",
		"REDUCE", "COUNT", "0", "AS", "chunk_count",
	).Result()
	if err != nil {
		if strings.Contains(err.Error(), "Unknown index name") || strings.Contains(err.Error(), "No such index") {
			return []DocumentMeta{}, nil
		}
		return nil, NewRAGError(ErrCodeSearchFailed, "list documents for "+indexName, err)
	}

	return parseAggregateResult(result)
}

// parseAggregateResult 解析 FT.AGGREGATE 返回值（RESP2 / RESP3 兼容）
func parseAggregateResult(result interface{}) ([]DocumentMeta, error) {
	// RESP3 format
	if mapResult, ok := result.(map[interface{}]interface{}); ok {
		return parseAggregateRESP3(mapResult)
	}

	// RESP2 format: [total, [field1, val1, field2, val2, ...], ...]
	arr, ok := result.([]interface{})
	if !ok || len(arr) < 2 {
		return []DocumentMeta{}, nil
	}

	var docs []DocumentMeta
	for i := 1; i < len(arr); i++ {
		row, ok := arr[i].([]interface{})
		if !ok {
			continue
		}
		doc := DocumentMeta{}
		for j := 0; j < len(row)-1; j += 2 {
			key, _ := row[j].(string)
			val := fmt.Sprintf("%v", row[j+1])
			switch key {
			case "file_id":
				doc.FileID = val
			case "file_name":
				doc.FileName = val
			case "chunk_count":
				fmt.Sscanf(val, "%d", &doc.ChunkCount)
			}
		}
		if doc.FileID != "" {
			docs = append(docs, doc)
		}
	}
	return docs, nil
}

// parseAggregateRESP3 解析 RESP3 格式的 FT.AGGREGATE 结果
func parseAggregateRESP3(mapResult map[interface{}]interface{}) ([]DocumentMeta, error) {
	resultsData, ok := mapResult["results"]
	if !ok {
		return []DocumentMeta{}, nil
	}
	resultsArray, ok := resultsData.([]interface{})
	if !ok {
		return []DocumentMeta{}, nil
	}

	var docs []DocumentMeta
	for _, item := range resultsArray {
		docMap, ok := item.(map[interface{}]interface{})
		if !ok {
			continue
		}
		doc := DocumentMeta{}
		if attrs, ok := docMap["extra_attributes"]; ok {
			if attrsMap, ok := attrs.(map[interface{}]interface{}); ok {
				for k, v := range attrsMap {
					key, _ := k.(string)
					val := fmt.Sprintf("%v", v)
					switch key {
					case "file_id":
						doc.FileID = val
					case "file_name":
						doc.FileName = val
					case "chunk_count":
						fmt.Sscanf(val, "%d", &doc.ChunkCount)
					}
				}
			}
		}
		if doc.FileID != "" {
			docs = append(docs, doc)
		}
	}
	return docs, nil
}

// ═══════════════════════════════════════════════════════════════════════════════
// 内部辅助函数
// ═══════════════════════════════════════════════════════════════════════════════

// extractKeysFromSearch 从 FT.SEARCH 返回值中提取文档 Key 列表
//
// 【RESP2 vs RESP3 兼容】
// Redis 6.x (RESP2): 返回 []interface{}{total, key1, fields1, key2, fields2, ...}
// Redis 7.x (RESP3): 返回 map[interface{}]interface{}{"total_results": N, "results": [...]}
// 此函数自动检测格式并分发到对应的解析逻辑。
func extractKeysFromSearch(result interface{}) []string {
	arr, ok := result.([]interface{})
	if !ok || len(arr) < 1 {
		if mapResult, ok := result.(map[interface{}]interface{}); ok {
			return extractKeysFromRESP3(mapResult)
		}
		return nil
	}

	// RESP2 格式: [total, key1, fields1, key2, fields2, ...]
	// 因为 RETURN 0 时没有 fields，所以步长为 2（key 在奇数位置）
	var keys []string
	for i := 1; i < len(arr); i += 2 {
		if key, ok := arr[i].(string); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// extractKeysFromRESP3 从 RESP3 格式的搜索结果中提取 Key 列表
func extractKeysFromRESP3(mapResult map[interface{}]interface{}) []string {
	resultsData, ok := mapResult["results"]
	if !ok {
		return nil
	}
	resultsArray, ok := resultsData.([]interface{})
	if !ok {
		return nil
	}

	var keys []string
	for _, item := range resultsArray {
		docMap, ok := item.(map[interface{}]interface{})
		if !ok {
			continue
		}
		if id, ok := docMap["id"]; ok {
			if idStr, ok := id.(string); ok {
				keys = append(keys, idStr)
			}
		}
	}
	return keys
}

// mergeByRRF 使用 Reciprocal Rank Fusion (倒数排名融合) 合并两路搜索结果
//
// 【RRF 算法原理】
//
//	对文档 d 在第 i 路搜索结果中排名为 rank_i，其融合分数为:
//
//	  score(d) = Σ weight_i × 1 / (k + rank_i)
//
//	其中:
//	  - k = 60 是 RRF 标准常数（由 Cormack, Clarke & Butt 在 2009 年论文中提出）
//	    k 的作用是平滑排名差异，防止排名靠前的文档获得过大的分数优势
//	    k=60 在多数信息检索基准测试中表现稳定
//	  - weight_i 是第 i 路搜索的权重（向量搜索默认 0.7，关键词搜索默认 0.3）
//
// 【为什么选择 RRF 而非其他融合方法？】
//   - 与 CombSUM / CombMNZ 不同，RRF 不要求各路搜索的分数在同一尺度
//   - 向量距离（0~2）和 BM25 分数（0~∞）天然不可比，RRF 仅依赖排名，完美规避了此问题
//   - 实现简单，无需 min-max 归一化等预处理步骤
//
// 【排序复杂度】使用 sort.Slice (基于内省排序)，时间复杂度 O(N log N)
func mergeByRRF(vectorResults, textResults []VectorSearchResult, vectorWeight, keywordWeight float64, topK int) []VectorSearchResult {
	const rrfK = 60.0 // RRF 标准常数 k，平滑排名差异

	if vectorWeight == 0 {
		vectorWeight = 0.7 // 向量搜索权重默认 0.7（语义理解通常更重要）
	}
	if keywordWeight == 0 {
		keywordWeight = 0.3 // 关键词搜索权重默认 0.3
	}

	type scored struct {
		result VectorSearchResult
		score  float64
	}

	scoreMap := make(map[string]*scored)

	// 计算向量搜索结果的 RRF 分数: vectorWeight × 1/(k + rank)
	for rank, r := range vectorResults {
		rrfScore := vectorWeight * (1.0 / (rrfK + float64(rank+1)))
		key := r.Key
		if key == "" {
			key = fmt.Sprintf("vec_%d", rank)
		}
		scoreMap[key] = &scored{result: r, score: rrfScore}
	}

	// 计算关键词搜索结果的 RRF 分数并累加（同一文档出现在两路中则分数叠加）
	for rank, r := range textResults {
		rrfScore := keywordWeight * (1.0 / (rrfK + float64(rank+1)))
		key := r.Key
		if key == "" {
			key = fmt.Sprintf("text_%d", rank)
		}
		if existing, ok := scoreMap[key]; ok {
			// 同一文档同时被向量和关键词命中，分数累加 → 排名提升
			existing.score += rrfScore
		} else {
			scoreMap[key] = &scored{result: r, score: rrfScore}
		}
	}

	results := make([]VectorSearchResult, 0, len(scoreMap))
	for _, s := range scoreMap {
		s.result.Score = s.score
		results = append(results, s.result)
	}

	// 按 RRF 融合分数降序排列
	// sort.Slice 使用内省排序 (Introsort)，平均 O(N log N)，最坏 O(N log N)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// 截取 TopK 个结果
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

// parseRawSearchResult 解析 FT.SEARCH 的原始返回值
//
// 【RESP2 / RESP3 双协议兼容】
// Redis 客户端库可能返回两种格式:
//   - RESP2 ([]interface{}): Redis 6.x 默认协议，数组形式
//   - RESP3 (map[interface{}]interface{}): Redis 7.x 新协议，结构化 Map 形式
//
// 此函数通过类型断言自动分发到对应的解析函数。
func parseRawSearchResult(result interface{}) ([]VectorSearchResult, error) {
	// 优先检测 RESP3 格式（Redis 7.x）
	if mapResult, ok := result.(map[interface{}]interface{}); ok {
		return parseRawRESP3(mapResult)
	}

	// RESP2 格式: [total, key1, [field1, val1, ...], key2, [field2, val2, ...], ...]
	arr, ok := result.([]interface{})
	if !ok || len(arr) < 1 {
		return []VectorSearchResult{}, nil
	}

	totalCount, _ := arr[0].(int64)
	if totalCount == 0 {
		return []VectorSearchResult{}, nil
	}

	var results []VectorSearchResult
	// 步长为 2: 偶数位置是 Key，奇数位置是字段数组
	for i := 1; i < len(arr); i += 2 {
		if i+1 >= len(arr) {
			break
		}
		key, _ := arr[i].(string)
		fields, ok := arr[i+1].([]interface{})
		if !ok {
			continue
		}

		// 字段数组为 [name1, val1, name2, val2, ...] 的扁平结构
		fieldMap := make(map[string]string)
		for j := 0; j < len(fields)-1; j += 2 {
			fname, _ := fields[j].(string)
			fval, _ := fields[j+1].(string)
			fieldMap[fname] = fval
		}

		results = append(results, VectorSearchResult{
			Key:    key,
			Fields: fieldMap,
		})
	}

	return results, nil
}

// parseRawRESP3 解析 RESP3 格式的搜索结果
//
// RESP3 结构:
//
//	{
//	  "total_results": int64,
//	  "results": [
//	    {"id": "key1", "extra_attributes": {"field1": "val1", ...}},
//	    {"id": "key2", "extra_attributes": {"field1": "val1", ...}},
//	    ...
//	  ]
//	}
func parseRawRESP3(mapResult map[interface{}]interface{}) ([]VectorSearchResult, error) {
	var totalResults int64
	if tr, ok := mapResult["total_results"]; ok {
		switch v := tr.(type) {
		case int64:
			totalResults = v
		case int:
			totalResults = int64(v)
		}
	}

	if totalResults == 0 {
		return []VectorSearchResult{}, nil
	}

	resultsData, ok := mapResult["results"]
	if !ok {
		return []VectorSearchResult{}, nil
	}

	resultsArray, ok := resultsData.([]interface{})
	if !ok {
		return []VectorSearchResult{}, nil
	}

	var results []VectorSearchResult
	for _, item := range resultsArray {
		docMap, ok := item.(map[interface{}]interface{})
		if !ok {
			continue
		}

		vsr := VectorSearchResult{Fields: make(map[string]string)}

		if id, ok := docMap["id"]; ok {
			if idStr, ok := id.(string); ok {
				vsr.Key = idStr
			}
		}

		// RESP3 将文档字段放在 "extra_attributes" 子 Map 中
		if attrs, ok := docMap["extra_attributes"]; ok {
			if attrsMap, ok := attrs.(map[interface{}]interface{}); ok {
				for k, v := range attrsMap {
					if ks, ok := k.(string); ok {
						vsr.Fields[ks] = fmt.Sprintf("%v", v)
					}
				}
			}
		}

		// 同时处理 docMap 顶层的其他字段（兼容不同 Redis 版本行为）
		for k, v := range docMap {
			ks, ok := k.(string)
			if !ok || ks == "id" || ks == "extra_attributes" {
				continue
			}
			vsr.Fields[ks] = fmt.Sprintf("%v", v)
		}

		results = append(results, vsr)
	}

	return results, nil
}

// escapeRedisQuery 转义 Redis 查询字符串中的特殊字符
// RediSearch 查询语法中以下字符有特殊含义，需要用 \ 转义以作为字面值匹配
func escapeRedisQuery(query string) string {
	special := []string{
		"@", "!", "{", "}", "(", ")", "[", "]", "\\", ":", ";",
		"~", "&", "*", "$", "^", "-", "+", "=", ">", "<", "|",
	}
	result := query
	for _, ch := range special {
		result = strings.ReplaceAll(result, ch, "\\"+ch)
	}
	return result
}

// Float32SliceToBytes 将 float32 切片转换为 Little-Endian 字节序列
//
// 【格式说明】
// Redis RediSearch 的 VECTOR 字段要求传入 Little-Endian 格式的二进制数据:
//   - 每个 float32 占 4 字节
//   - 使用 IEEE 754 标准编码
//   - 字节序为 Little-Endian（低位字节在前）
//
// 例: float32(1.0) → 二进制 0x3F800000 → LE 字节 [0x00, 0x00, 0x80, 0x3F]
func Float32SliceToBytes(floats []float32) []byte {
	bytes := make([]byte, len(floats)*4)
	for i, f := range floats {
		bits := math.Float32bits(f)
		bytes[i*4] = byte(bits)
		bytes[i*4+1] = byte(bits >> 8)
		bytes[i*4+2] = byte(bits >> 16)
		bytes[i*4+3] = byte(bits >> 24)
	}
	return bytes
}
