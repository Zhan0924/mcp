/*
rag/types.go — RAG 对外 API 边界类型定义

设计原则:

	本文件定义的结构体是 rag 包与上层 MCP tools 层之间的「契约类型」。
	tools 层（tools/rag_tools.go）将这些结构体序列化为 JSON 返回给 MCP 客户端，
	因此:
	  1. 所有字段均带 json tag，字段命名遵循 snake_case 以符合 JSON 惯例
	  2. omitempty 仅用于真正可选的字段（如 Cached、Format），避免零值噪声
	  3. 结构体只携带数据，不包含行为方法——保持纯 DTO 语义

导出结构体:
  - RetrievalResult  — 单条检索命中结果（query 操作返回）
  - IndexResult      — 文档索引操作摘要（index 操作返回）
  - DeleteResult     — 文档删除操作摘要（delete 操作返回）

内部函数:
  - newUUID()        — 基于 UUID v4 生成全局唯一标识符
*/
package rag

import "github.com/google/uuid"

// RetrievalResult 单条检索命中结果。
// 由 Retriever.Search 返回，经 tools 层序列化后作为 MCP JSON 响应的一部分。
// RelevanceScore 已经过归一化（0~1），分数越高相关性越强。
type RetrievalResult struct {
	ChunkID        string        `json:"chunk_id"`
	ParentChunkID  string        `json:"parent_chunk_id,omitempty"` // 用于 Parent-Child Retriever 的分组去重
	FileID         string        `json:"file_id"`
	FileName       string        `json:"file_name"`
	ChunkIndex     int           `json:"chunk_index"` // 该 chunk 在原始文档中的顺序号，便于上层重建上下文
	Content        string        `json:"content"`
	RelevanceScore float64       `json:"relevance_score"`         // 归一化相关性得分 [0, 1]
	ScoreDetails   *ScoreDetails `json:"score_details,omitempty"` // 检索分数可解释性明细
}

// ScoreDetails 检索分数可解释性明细。
// 各字段仅在对应检索路径生效时有值，使用 omitempty 避免无效零值噪声。
// Source 字段标识该结果的来源路径，帮助调用方理解排名依据：
//   - "vector_only"  — 混合检索中仅向量路命中
//   - "keyword_only" — 混合检索中仅关键词路命中
//   - "hybrid"       — 同时被向量和关键词搜索召回（RRF 融合加分）
//   - "graph"        — 来自 Graph RAG 知识图谱
type ScoreDetails struct {
	VectorScore  float64 `json:"vector_score,omitempty"`  // 原始向量相似度 (1 - cosine_distance/2)
	KeywordScore float64 `json:"keyword_score,omitempty"` // BM25 关键词匹配得分
	RRFScore     float64 `json:"rrf_score,omitempty"`     // RRF 融合后得分
	RerankScore  float64 `json:"rerank_score,omitempty"`  // Reranker 重排序得分
	Source       string  `json:"source,omitempty"`        // 来源路径标识
}

// IndexResult 文档索引操作的汇总报告。
// TotalChunks = Indexed + Failed + Cached，调用方可据此判断索引是否完整。
type IndexResult struct {
	FileID      string `json:"file_id"`
	FileName    string `json:"file_name"`
	TotalChunks int    `json:"total_chunks"`
	Indexed     int    `json:"indexed"`          // 本次新写入 Redis 的 chunk 数
	Failed      int    `json:"failed"`           // 写入失败的 chunk 数
	Cached      int    `json:"cached,omitempty"` // 命中 embedding 缓存而跳过重新计算的 chunk 数
	Format      string `json:"format,omitempty"` // 检测到的文档格式（markdown / txt / pdf）
}

// DeleteResult 文档删除操作的汇总报告。
// Deleted 为实际从 Redis 中移除的 key 数量（含所有 chunk key）。
type DeleteResult struct {
	FileID  string `json:"file_id"`
	Deleted int64  `json:"deleted"`
}

// newUUID 生成 UUID v4 字符串。
// 用于为新文档和 chunk 分配全局唯一 ID，保证多租户环境下不冲突。
func newUUID() string {
	return uuid.New().String()
}
