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
	ChunkID        string  `json:"chunk_id"`
	ParentChunkID  string  `json:"parent_chunk_id,omitempty"` // 用于 Parent-Child Retriever 的分组去重
	FileID         string  `json:"file_id"`
	FileName       string  `json:"file_name"`
	ChunkIndex     int     `json:"chunk_index"` // 该 chunk 在原始文档中的顺序号，便于上层重建上下文
	Content        string  `json:"content"`
	RelevanceScore float64 `json:"relevance_score"` // 归一化相关性得分 [0, 1]
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
