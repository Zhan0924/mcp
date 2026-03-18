/*
rag/config.go — RAG 子系统默认配置中心

设计原则:
  所有配置采用「合理默认值 + TOML 覆盖」两层策略:
    1. Default*Config() 函数提供开箱即用的基线值，零配置即可运行
    2. 外部 config.toml 通过 BurntSushi/toml 反序列化后覆盖非零字段
  这样既保证新部署无需配置文件也能启动，又允许生产环境按需调优。

导出结构体:
  - EmbeddingConfig      — Embedding API 连接参数（降级旁路用）
  - RetrieverConfig      — 检索器全量配置（索引、算法、批量、混合检索）
  - ChunkingConfig       — 文档分块策略参数

导出函数:
  - DefaultRetrieverConfig() *RetrieverConfig   — 检索器默认值
  - DefaultChunkingConfig()  *ChunkingConfig    — 分块策略默认值
*/
package rag

// EmbeddingConfig Embedding 连接配置
// 仅在 EmbeddingManager 不可用、需要直接创建 Embedder 时使用（降级旁路）。
// 正常流程应通过 EmbeddingManager 获取 provider，此结构体是兜底方案。
type EmbeddingConfig struct {
	APIKey         string // Embedding API Key
	BaseURL        string // Embedding API 地址
	EmbeddingModel string // Embedding 模型名称

	// APIKeyEnvVar 当 APIKey 为空时，从此环境变量读取 API Key
	// 默认值: "OPENAI_API_KEY"
	APIKeyEnvVar string
}

// RetrieverConfig 检索器运行配置
//
// 字段分为五个逻辑区域:
//   Redis 索引 → 向量维度 → 检索参数 → 索引算法 → 批量 & 混合检索
// 每个区域由 DefaultRetrieverConfig() 赋予合理基线值。
type RetrieverConfig struct {
	// ---- Redis 索引配置 ----
	// 模板中 %d 占位符在运行时替换为 userID，实现多租户索引隔离
	UserIndexNameTemplate   string
	UserIndexPrefixTemplate string

	// Dimension 向量维度，0 表示「延迟推断」:
	// 首次调用 Embedding API 后从返回向量长度自动设定，
	// 避免在配置层硬编码模型维度（不同模型维度各异）。
	Dimension int

	VectorFieldName string
	ReturnFields    []string
	SearchDialect   int

	// ---- 检索参数 ----
	DefaultTopK int     // 用户未指定 topK 时的默认值
	MaxTopK     int     // 防止客户端传入过大 topK 导致 Redis 压力
	MinScore    float64 // 低于此阈值的结果直接丢弃，减少噪声

	// ---- 索引算法 ----
	// FLAT: 暴力扫描，数据量小时精度最高、零额外内存
	// HNSW: 近似最近邻，数据量大时用吞吐换精度
	IndexAlgorithm string
	HNSWParams     *HNSWParams

	// ---- 批量操作 ----
	EmbeddingBatchSize int // Embedding 分批大小，控制单次 API 调用的 chunk 数量
	PipelineBatchSize  int // Redis Pipeline 分批大小，平衡网络 RTT 与内存占用

	// ---- 混合检索 ----
	// 混合检索将向量相似度与 BM25 关键词得分加权融合，
	// VectorWeight + KeywordWeight 应等于 1.0
	HybridSearchEnabled bool
	VectorWeight        float64
	KeywordWeight       float64
}

// ChunkingConfig 文档分块配置
//
// 分块质量直接影响检索召回率:
//   - 过大 → 噪声多、embedding 模糊
//   - 过小 → 上下文断裂、语义不完整
//   - Overlap 保证跨块语义连续性
type ChunkingConfig struct {
	MaxChunkSize   int  // 最大分块大小(字符)，默认 1000
	MinChunkSize   int  // 最小分块大小(字符)，过小的尾块会被合并到前一块
	OverlapSize    int  // 重叠大小(字符)，保证相邻块之间的语义连续
	StructureAware bool // 结构感知模式: 按 Markdown 标题/代码块边界优先分割
}

// DefaultRetrieverConfig 返回检索器的开箱即用默认配置。
// 设计意图: 这些值适合中小规模文档集（< 10 万 chunks）；
// 大规模场景应通过 config.toml 覆盖 IndexAlgorithm 为 HNSW 并调整批量参数。
func DefaultRetrieverConfig() *RetrieverConfig {
	return &RetrieverConfig{
		UserIndexNameTemplate:   "rag_user_%d:idx",
		UserIndexPrefixTemplate: "rag_user_%d:",
		VectorFieldName:         "vector",
		ReturnFields:            []string{"content", "file_id", "file_name", "chunk_id", "chunk_index", "distance"},
		SearchDialect:           2, // RediSearch 2.x 查询语法
		DefaultTopK:             5,
		MaxTopK:                 20,
		MinScore:                0.5,
		IndexAlgorithm:          "FLAT", // 默认暴力扫描，小数据集下精度最优
		EmbeddingBatchSize:      10,
		PipelineBatchSize:       500,
		HybridSearchEnabled:     false,  // 默认关闭混合检索，纯向量即可满足大多数场景
		VectorWeight:            0.7,    // 向量 70% + 关键词 30% 是经验平衡点
		KeywordWeight:           0.3,
	}
}

// DefaultChunkingConfig 返回分块策略的开箱即用默认配置。
// 1000 字符上限约等于 ~250 个 token（英文），与主流 embedding 模型的最佳输入区间匹配。
func DefaultChunkingConfig() *ChunkingConfig {
	return &ChunkingConfig{
		MaxChunkSize:   1000,
		MinChunkSize:   100,
		OverlapSize:    200,  // 约 20% 重叠率，在召回率和存储间取得平衡
		StructureAware: true, // 默认开启，利用 Markdown 结构提升分块质量
	}
}
