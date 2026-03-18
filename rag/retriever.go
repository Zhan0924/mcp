/*
┌─────────────────────────────────────────────────────────────────────────────┐
│                        retriever.go — 多文件 RAG 检索器                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  核心职责: 编排 "分块 → 向量化 → 存储 → 检索" 全流程，支持多租户隔离          │
│                                                                             │
│  ┌─── 类型 ──────────────────────────────────────────────────────────────┐  │
│  │ MultiFileRetriever  多文件检索器，持有 VectorStore / Embedder 等依赖   │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─── 构造 & 配置 ───────────────────────────────────────────────────────┐  │
│  │ NewMultiFileRetriever  创建检索器：优先走全局 Manager（带故障转移），   │  │
│  │                        否则直接创建 Ark Embedder                       │  │
│  │ SetTopK               设置每次检索返回的最大结果数，受 MaxTopK 上限约束 │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─── 索引写入流程 ─────────────────────────────────────────────────────┐  │
│  │ IndexDocument   分块 → 缓存去重 → 分批向量化 → Pipeline 批量写入      │  │
│  │ EnsureIndex     惰性创建 Redis 向量索引（首次使用时才触发）           │  │
│  │ DeleteDocument  通过 FT.SEARCH file_id 过滤 + DEL 删除指定文件全部块  │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─── 检索流程 ─────────────────────────────────────────────────────────┐  │
│  │ Retrieve         查询向量化 → 向量/混合检索 → 最低分过滤 → 返回结果   │  │
│  │ hybridRetrieve   混合检索：向量 + 关键词加权融合，失败时降级纯向量     │  │
│  │ filterByMinScore 过滤低于 MinScore 阈值的结果                         │  │
│  │ convertVectorResults  VectorSearchResult → RetrievalResult 格式转换   │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─── Embedding 辅助 ───────────────────────────────────────────────────┐  │
│  │ embedTexts         优先缓存 → 全局 Manager → 直接 Embedder 三级降级   │  │
│  │ embedWithoutCache  绕过缓存层直接调用 Manager 或 Embedder             │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
│  ┌─── 工具函数 ─────────────────────────────────────────────────────────┐  │
│  │ generateUserIndexName    按模板生成用户专属索引名（多租户隔离）        │  │
│  │ generateUserIndexPrefix  按模板生成 Key 前缀（Redis Cluster Hash Tag） │  │
│  │ escapeTagValue           转义 RediSearch TAG 字段查询中的特殊字符     │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	embeddingArk "github.com/cloudwego/eino-ext/components/embedding/ark"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/sirupsen/logrus"
)

// MultiFileRetriever 多文件检索器，编排 "分块 → 向量化 → 存储 → 检索" 全流程。
// 每个实例绑定一个 userID，通过索引名模板 (如 mcp_rag_user_%d:idx) 实现多租户数据隔离，
// 避免不同用户的文档在同一索引中互相污染。
type MultiFileRetriever struct {
	store      VectorStore        // 底层向量存储抽象（Redis / 其它实现）
	embCfg     *EmbeddingConfig   // Embedding API 连接配置
	retCfg     *RetrieverConfig   // 检索行为配置（TopK、权重、索引模板等）
	chunkCfg   *ChunkingConfig    // 文档分块策略配置
	embedding  embedding.Embedder // 直连 Embedder（无 Manager 时的降级方案）
	useManager bool               // 是否使用全局 EmbeddingManager（带多 Provider 故障转移）
	userID     uint               // 所属用户 ID，决定索引名和 Key 前缀
	topK       int                // 当前检索返回数量上限
}

// NewMultiFileRetriever 创建多文件检索器。
// Embedding 来源选择策略：优先使用全局 EmbeddingManager（支持多 Provider 故障转移和负载均衡），
// 仅当 Manager 不存在或无可用 Provider 时，才降级为直接创建单一 Ark Embedder。
// 这种两级降级保证了在 Manager 配置缺失时系统仍可运行。
func NewMultiFileRetriever(ctx context.Context, store VectorStore, embCfg *EmbeddingConfig, retCfg *RetrieverConfig, chunkCfg *ChunkingConfig, userID uint) (*MultiFileRetriever, error) {
	r := &MultiFileRetriever{
		store:    store,
		embCfg:   embCfg,
		retCfg:   retCfg,
		chunkCfg: chunkCfg,
		userID:   userID,
		topK:     retCfg.DefaultTopK,
	}

	// 优先检测全局 Manager 是否就绪（至少注册了一个 Provider）
	if manager := GetGlobalManager(); manager != nil {
		stats := manager.GetStats()
		if len(stats) > 0 {
			logrus.Debug("[RAG] Using embedding manager with failover support")
			r.useManager = true
			return r, nil
		}
	}

	// Manager 不可用时，降级创建直连 Embedder
	logrus.Debug("[RAG] Using direct embedder (no failover)")
	if embCfg == nil {
		return r, nil
	}

	// API Key 解析优先级: 配置字段 > 环境变量（默认 OPENAI_API_KEY）
	apiKey := embCfg.APIKey
	if apiKey == "" {
		envVar := embCfg.APIKeyEnvVar
		if envVar == "" {
			envVar = "OPENAI_API_KEY"
		}
		apiKey = os.Getenv(envVar)
	}

	arkConfig := &embeddingArk.EmbeddingConfig{
		BaseURL: embCfg.BaseURL,
		APIKey:  apiKey,
		Model:   embCfg.EmbeddingModel,
	}

	embedder, err := embeddingArk.NewEmbedder(ctx, arkConfig)
	if err != nil {
		return nil, NewRAGError(ErrCodeEmbeddingFailed, "create embedder", err)
	}

	r.embedding = embedder
	return r, nil
}

// SetTopK 设置每次检索返回的最大结果数。
// 通过 MaxTopK 做上界钳制，防止调用方传入过大值导致 Redis 返回海量数据拖慢响应。
func (r *MultiFileRetriever) SetTopK(topK int) {
	if topK < 1 {
		return
	}
	maxK := r.retCfg.MaxTopK
	if maxK > 0 && topK > maxK {
		topK = maxK
	}
	r.topK = topK
}

// embedTexts 三级降级 Embedding 入口：
//  1. 全局缓存层 (CachedEmbedStrings) — 命中则零 API 调用
//  2. 全局 Manager (EmbedStrings)       — 多 Provider 故障转移
//  3. 直连 Embedder                     — 无故障转移的最终降级
//
// 这里与 embedWithoutCache 的区别：本方法走缓存，用于正常检索；
// embedWithoutCache 专门供 IndexDocument 缓存去重后的"未命中"部分调用，
// 避免重复经过缓存层查询造成无意义开销。
func (r *MultiFileRetriever) embedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	cache := GetGlobalCache()
	if cache != nil && cache.config.Enabled {
		return CachedEmbedStrings(ctx, texts)
	}
	if r.useManager {
		return EmbedStrings(ctx, texts)
	}
	if r.embedding != nil {
		return r.embedding.EmbedStrings(ctx, texts)
	}
	return nil, NewRAGError(ErrCodeManagerNotReady, "no embedding source available", nil)
}

// Retrieve 检索相关文档。完整流程：
//  1. 将用户查询文本向量化
//  2. 通过用户专属索引名实现租户隔离
//  3. 若指定 fileIDs，构造 RediSearch TAG 过滤表达式限定文件范围
//  4. 根据配置选择纯向量检索或混合检索
//  5. 对结果执行最低分阈值过滤
func (r *MultiFileRetriever) Retrieve(ctx context.Context, query string, fileIDs []string) ([]RetrievalResult, error) {
	logrus.Infof("[RAG Query] Starting retrieval for user %d, query: %s, fileIDs: %v", r.userID, query, fileIDs)

	vectors, err := r.embedTexts(ctx, []string{query})
	if err != nil {
		return nil, NewRAGError(ErrCodeEmbeddingFailed, "embed query", err)
	}

	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, NewRAGError(ErrCodeEmbeddingFailed, "empty embedding result", nil)
	}

	queryVector := vectors[0]
	logrus.Infof("[RAG Query] Query vector dimension: %d", len(queryVector))

	// 每个用户拥有独立索引名，确保多租户检索不会跨用户泄露数据
	indexName := generateUserIndexName(r.retCfg.UserIndexNameTemplate, r.userID)

	// float64 → float32 → []byte: RediSearch VECTOR 字段要求小端序 float32 二进制
	queryVector32 := make([]float32, len(queryVector))
	for i, v := range queryVector {
		queryVector32[i] = float32(v)
	}
	vectorBytes := Float32SliceToBytes(queryVector32)

	// 构造 RediSearch 过滤表达式：TAG 字段 file_id 使用 OR 语义 ({v1|v2})
	// 未指定 fileIDs 时用 "*" 表示全量搜索
	var filterQuery string
	if len(fileIDs) > 0 {
		escapedIDs := make([]string, len(fileIDs))
		for i, id := range fileIDs {
			escapedIDs[i] = escapeTagValue(id)
		}
		filterQuery = fmt.Sprintf("@file_id:{%s}", strings.Join(escapedIDs, "|"))
	} else {
		filterQuery = "*"
	}

	vectorField := r.retCfg.VectorFieldName
	if vectorField == "" {
		vectorField = "vector"
	}

	returnFields := r.retCfg.ReturnFields
	if len(returnFields) == 0 {
		returnFields = []string{"content", "file_id", "file_name", "chunk_id", "chunk_index", "distance"}
	}

	dialect := r.retCfg.SearchDialect
	if dialect == 0 {
		dialect = 2
	}

	effectiveTopK := r.topK

	// 混合检索（向量 + 关键词加权融合），适合语义与字面匹配都重要的场景
	if r.retCfg.HybridSearchEnabled && query != "" {
		return r.hybridRetrieve(ctx, query, vectorBytes, filterQuery, indexName, vectorField, returnFields, dialect, effectiveTopK)
	}

	// 纯向量语义检索
	vq := VectorQuery{
		IndexName:       indexName,
		Vector:          vectorBytes,
		TopK:            effectiveTopK,
		VectorFieldName: vectorField,
		ReturnFields:    returnFields,
		SearchDialect:   dialect,
		FilterQuery:     filterQuery,
	}

	rawResults, err := r.store.SearchVectors(ctx, vq)
	if err != nil {
		return nil, err
	}

	results := convertVectorResults(rawResults)
	results = r.filterByMinScore(results)

	return results, nil
}

// hybridRetrieve 混合检索：向量相似度 + 关键词 BM25 加权融合。
// TopK × 3 的过采样是因为融合排序会重排结果，需要更大候选池才能保证最终 TopK 质量。
// 若混合检索底层不支持（如 Redis 版本不够），则优雅降级为纯向量检索。
func (r *MultiFileRetriever) hybridRetrieve(ctx context.Context, query string, vectorBytes []byte, filterQuery, indexName, vectorField string, returnFields []string, dialect, topK int) ([]RetrievalResult, error) {
	hq := HybridQuery{
		VectorQuery: VectorQuery{
			IndexName:       indexName,
			Vector:          vectorBytes,
			TopK:            topK * 3, // 过采样：融合排序需要更大候选池
			VectorFieldName: vectorField,
			ReturnFields:    returnFields,
			SearchDialect:   dialect,
			FilterQuery:     filterQuery,
		},
		TextQuery:     query,
		VectorWeight:  r.retCfg.VectorWeight,
		KeywordWeight: r.retCfg.KeywordWeight,
	}

	rawResults, err := r.store.HybridSearch(ctx, hq)
	if err != nil {
		// 降级策略：混合检索失败时回退到纯向量检索，保证可用性
		logrus.Warnf("[RAG Query] Hybrid search failed, falling back to vector search: %v", err)
		vq := hq.VectorQuery
		vq.TopK = topK
		rawResults, err = r.store.SearchVectors(ctx, vq)
		if err != nil {
			return nil, err
		}
	}

	results := convertVectorResults(rawResults)

	// 过采样后截断到用户实际请求的 TopK
	if len(results) > topK {
		results = results[:topK]
	}
	results = r.filterByMinScore(results)

	return results, nil
}

// filterByMinScore 过滤低质量结果。MinScore 阈值避免将不相关的低分块返回给 LLM，
// 减少 prompt 中的噪声，提高最终回答质量。
func (r *MultiFileRetriever) filterByMinScore(results []RetrievalResult) []RetrievalResult {
	if r.retCfg.MinScore <= 0 || len(results) == 0 {
		return results
	}

	var filtered []RetrievalResult
	for _, res := range results {
		if res.RelevanceScore >= r.retCfg.MinScore {
			filtered = append(filtered, res)
		}
	}
	logrus.Infof("[RAG Query] Filtered results: %d -> %d (minScore: %.2f)",
		len(results), len(filtered), r.retCfg.MinScore)
	return filtered
}

// convertVectorResults 将底层 VectorSearchResult 转换为上层 RetrievalResult。
// 相关性分数计算说明：
//   - distance 字段（余弦距离）转换公式: score = 1 - distance/2，将 [0,2] 映射到 [0,1]
//   - 若底层已提供 Score（如混合检索的融合分数），则直接覆盖 distance 计算值
//   - 丢弃 Content 为空的结果，避免返回无意义的空块
func convertVectorResults(raw []VectorSearchResult) []RetrievalResult {
	results := make([]RetrievalResult, 0, len(raw))
	for _, r := range raw {
		res := RetrievalResult{}

		// Redis Key 格式为 "prefix:chunkID"，从末段提取 ChunkID 作为兜底
		if key := r.Key; key != "" {
			parts := strings.Split(key, ":")
			if len(parts) > 1 {
				res.ChunkID = parts[len(parts)-1]
			}
		}

		if v, ok := r.Fields["content"]; ok {
			res.Content = v
		}
		if v, ok := r.Fields["file_id"]; ok {
			res.FileID = v
		}
		if v, ok := r.Fields["file_name"]; ok {
			res.FileName = v
		}
		if v, ok := r.Fields["chunk_id"]; ok {
			res.ChunkID = v
		}
		if v, ok := r.Fields["chunk_index"]; ok {
			res.ChunkIndex, _ = strconv.Atoi(v)
		}

		// 余弦距离 → 相关性分数: distance ∈ [0, 2]，score ∈ [0, 1]
		if v, ok := r.Fields["distance"]; ok {
			dist, _ := strconv.ParseFloat(v, 64)
			res.RelevanceScore = 1 - dist/2
		}

		// 混合检索等场景底层已计算融合分数，优先使用
		if r.Score > 0 {
			res.RelevanceScore = r.Score
		}

		if res.Content != "" {
			results = append(results, res)
		}
	}
	return results
}

// EnsureIndex 确保用户的 Redis 向量索引存在。
// 采用惰性创建策略：仅在 IndexDocument 首次写入时调用，而非构造器中急切创建。
// 这避免了为从未上传文档的用户浪费 Redis 资源（FT.CREATE 会占用内存和 CPU）。
// vectorDim 从首批 Embedding 结果动态获取，无需配置中硬编码维度。
func (r *MultiFileRetriever) EnsureIndex(ctx context.Context, vectorDim int) error {
	indexName := generateUserIndexName(r.retCfg.UserIndexNameTemplate, r.userID)
	prefix := generateUserIndexPrefix(r.retCfg.UserIndexPrefixTemplate, r.userID)

	vectorField := r.retCfg.VectorFieldName
	if vectorField == "" {
		vectorField = "vector"
	}

	algorithm := IndexAlgorithm(r.retCfg.IndexAlgorithm)
	if algorithm == "" {
		algorithm = IndexAlgorithmFLAT
	}

	config := IndexConfig{
		IndexName:       indexName,
		Prefix:          prefix,
		VectorFieldName: vectorField,
		Dimension:       vectorDim,
		Algorithm:       algorithm,
		HNSWParams:      r.retCfg.HNSWParams,
	}

	return r.store.EnsureIndex(ctx, config)
}

// IndexDocument 将文档分块、向量化并存入向量数据库。完整流程：
//  1. 分块：优先尝试结构感知分块（保留 Markdown 标题层次），失败则回退固定窗口分块
//  2. 缓存去重：相同文本不重复调用 Embedding API，降低成本和延迟
//  3. 分批向量化：按 EmbeddingBatchSize 拆分，尊重 API 速率限制
//  4. 惰性建索引：首次写入时才调用 EnsureIndex
//  5. Pipeline 批量 Upsert：将 chunk 元数据 + 向量一次性写入 Redis
func (r *MultiFileRetriever) IndexDocument(ctx context.Context, fileID, fileName, content string) (*IndexResult, error) {
	logrus.Infof("[RAG Index] Indexing document: file_id=%s, file_name=%s, user=%d, content_len=%d",
		fileID, fileName, r.userID, len(content))

	chunkCfg := DefaultChunkingConfig()
	if r.chunkCfg != nil {
		chunkCfg = r.chunkCfg
	}

	// 结构感知分块：对 Markdown 文档按标题层次切分，保留语义完整性；
	// 其它格式或解析失败时回退到固定窗口 + 重叠的通用分块策略
	var chunks []Chunk
	if chunkCfg.StructureAware {
		doc, err := ParseDocument(content, "")
		if err == nil && doc.Format == FormatMarkdown && len(doc.Sections) > 0 {
			chunks = StructureAwareChunk(doc, *chunkCfg)
			logrus.Infof("[RAG Index] Used structure-aware chunking (format=%s, sections=%d)",
				doc.Format, len(doc.Sections))
		}
	}
	if len(chunks) == 0 {
		chunks = ChunkDocument(content, *chunkCfg)
	}

	result := &IndexResult{
		FileID:      fileID,
		FileName:    fileName,
		TotalChunks: len(chunks),
	}

	if len(chunks) == 0 {
		return result, nil
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}

	// 分批大小控制：过大会触发 API 速率限制或超时，过小则增加网络往返开销
	batchSize := r.retCfg.EmbeddingBatchSize
	if batchSize <= 0 {
		batchSize = 10
	}

	// 分批向量化主循环，每批独立处理，单批失败不影响其余批次（部分成功语义）
	allVectors := make([][]float64, 0, len(texts))
	cache := GetGlobalCache()

	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		batchTexts := texts[start:end]

		// 缓存去重策略：先查缓存拿到已有向量，仅对缓存未命中的文本调用 Embedding API。
		// 这在重复上传或增量更新场景下能大幅减少 API 调用量。
		if cache != nil && cache.config.DeduplicateOn {
			cachedMap, missedIdx := cache.GetBatch(ctx, batchTexts)

			// 全部命中缓存，跳过 API 调用
			if len(missedIdx) == 0 {
				batchVectors := make([][]float64, len(batchTexts))
				for i, vec := range cachedMap {
					batchVectors[i] = vec
				}
				allVectors = append(allVectors, batchVectors...)
				result.Cached += len(batchTexts)
				continue
			}

			// 仅对未命中的文本调用 embedWithoutCache（绕过缓存层避免重复查询）
			missedTexts := make([]string, len(missedIdx))
			for i, idx := range missedIdx {
				missedTexts[i] = batchTexts[idx]
			}

			newVecs, err := r.embedWithoutCache(ctx, missedTexts)
			if err != nil {
				logrus.Errorf("[RAG Index] Embedding batch [%d:%d] failed: %v", start, end, err)
				result.Failed += end - start
				continue
			}

			// 将新向量回填到 cachedMap 并写入缓存，下次相同文本无需再调 API
			for i, idx := range missedIdx {
				if i < len(newVecs) {
					cachedMap[idx] = newVecs[i]
					cache.Put(ctx, batchTexts[idx], newVecs[i])
				}
			}

			batchVectors := make([][]float64, len(batchTexts))
			for i := range batchTexts {
				batchVectors[i] = cachedMap[i]
			}
			allVectors = append(allVectors, batchVectors...)
			result.Cached += len(batchTexts) - len(missedIdx)
		} else {
			vectors, err := r.embedTexts(ctx, batchTexts)
			if err != nil {
				logrus.Errorf("[RAG Index] Embedding batch [%d:%d] failed: %v", start, end, err)
				result.Failed += end - start
				continue
			}
			allVectors = append(allVectors, vectors...)
		}
	}

	if len(allVectors) == 0 {
		return result, NewRAGError(ErrCodeBatchFailed, "all embedding batches failed", nil)
	}

	// 维度从首批 Embedding 结果动态推断，避免配置与模型实际输出不一致
	vectorDim := r.retCfg.Dimension
	if vectorDim == 0 {
		vectorDim = len(allVectors[0])
	}
	// 惰性建索引：仅在确认有数据要写入时才创建 Redis 索引
	if err := r.EnsureIndex(ctx, vectorDim); err != nil {
		return result, err
	}

	prefix := generateUserIndexPrefix(r.retCfg.UserIndexPrefixTemplate, r.userID)
	vectorField := r.retCfg.VectorFieldName
	if vectorField == "" {
		vectorField = "vector"
	}

	// 构造 VectorEntry 列表，Key = prefix + chunkID 确保用户隔离且 chunk 唯一
	entries := make([]VectorEntry, 0, len(chunks))
	for i, chunk := range chunks {
		if i >= len(allVectors) || allVectors[i] == nil {
			result.Failed++
			continue
		}

		vec := allVectors[i]
		vec32 := make([]float32, len(vec))
		for j, v := range vec {
			vec32[j] = float32(v)
		}
		vecBytes := Float32SliceToBytes(vec32)

		entries = append(entries, VectorEntry{
			Key: fmt.Sprintf("%s%s", prefix, chunk.ChunkID),
			Fields: map[string]interface{}{
				"content":     chunk.Content,
				"file_id":     fileID,
				"file_name":   fileName,
				"chunk_id":    chunk.ChunkID,
				"chunk_index": chunk.ChunkIndex,
				vectorField:   vecBytes,
			},
		})
	}

	indexed, err := r.store.UpsertVectors(ctx, entries)
	if err != nil {
		return result, NewRAGError(ErrCodeBatchFailed, "upsert vectors", err)
	}
	result.Indexed = indexed
	result.Failed += len(entries) - indexed

	logrus.Infof("[RAG Index] Done: total=%d, indexed=%d, failed=%d, cached=%d",
		result.TotalChunks, result.Indexed, result.Failed, result.Cached)

	return result, nil
}

// DeleteDocument 删除指定文件的所有向量数据。
// 底层通过 FT.SEARCH 按 file_id TAG 过滤找到该文件的所有 chunk Key，再逐一 DEL。
// 这种基于标签的删除方式无需维护文件到 chunk 的映射表，简化了数据模型。
func (r *MultiFileRetriever) DeleteDocument(ctx context.Context, fileID string) (*DeleteResult, error) {
	indexName := generateUserIndexName(r.retCfg.UserIndexNameTemplate, r.userID)
	prefix := generateUserIndexPrefix(r.retCfg.UserIndexPrefixTemplate, r.userID)

	deleted, err := r.store.DeleteByFileID(ctx, indexName, prefix, fileID)
	if err != nil {
		return nil, err
	}

	logrus.Infof("[RAG Delete] Deleted %d chunks for file_id=%s, user=%d", deleted, fileID, r.userID)
	return &DeleteResult{FileID: fileID, Deleted: deleted}, nil
}

// embedWithoutCache 绕过缓存层直接调用 Embedding API。
// 专门供 IndexDocument 缓存去重流程中 "未命中" 的文本使用：
// 这些文本刚在 GetBatch 中确认不在缓存里，再走 CachedEmbedStrings 只会白查一次缓存。
func (r *MultiFileRetriever) embedWithoutCache(ctx context.Context, texts []string) ([][]float64, error) {
	if r.useManager {
		return EmbedStrings(ctx, texts)
	}
	if r.embedding != nil {
		return r.embedding.EmbedStrings(ctx, texts)
	}
	return nil, NewRAGError(ErrCodeManagerNotReady, "no embedding source", nil)
}

// generateUserIndexName 根据模板生成用户专属的 RediSearch 索引名。
// 默认模板 "rag_user_%d:idx" 中的冒号是 Redis Cluster Hash Tag 友好的分隔符，
// 确保同一用户的索引和数据落在相同的 slot，避免跨 slot 操作。
func generateUserIndexName(template string, userID uint) string {
	if template == "" {
		template = "rag_user_%d:idx"
	}
	return fmt.Sprintf(template, userID)
}

// generateUserIndexPrefix 生成用户专属的 Key 前缀，用于 FT.CREATE 的 PREFIX 参数。
// 所有属于该用户的 chunk Key 都以此前缀开头，实现 Redis 层面的多租户数据隔离。
// 模板中的 Hash Tag（如 {user_1}:）可用于 Redis Cluster 场景下的 slot 路由控制。
func generateUserIndexPrefix(template string, userID uint) string {
	if template == "" {
		template = "rag_user_%d:"
	}
	return fmt.Sprintf(template, userID)
}

// escapeTagValue 转义 RediSearch TAG 字段查询中的特殊字符。
// RediSearch 的 TAG 过滤语法 (@field:{value}) 中，大量标点符号具有特殊含义
// （如 | 是 OR 分隔符，{} 是 TAG 表达式边界，空格是分词符等），
// 若 file_id 包含这些字符而不转义，会导致查询解析错误或静默返回错误结果。
func escapeTagValue(value string) string {
	replacer := strings.NewReplacer(
		",", "\\,",
		".", "\\.",
		"<", "\\<",
		">", "\\>",
		"{", "\\{",
		"}", "\\}",
		"[", "\\[",
		"]", "\\]",
		"\"", "\\\"",
		"'", "\\'",
		":", "\\:",
		";", "\\;",
		"!", "\\!",
		"@", "\\@",
		"#", "\\#",
		"$", "\\$",
		"%", "\\%",
		"^", "\\^",
		"&", "\\&",
		"*", "\\*",
		"(", "\\(",
		")", "\\)",
		"-", "\\-",
		"+", "\\+",
		"=", "\\=",
		"~", "\\~",
		"|", "\\|",
		"/", "\\/",
		"\\", "\\\\",
		" ", "\\ ",
	)
	return replacer.Replace(value)
}
