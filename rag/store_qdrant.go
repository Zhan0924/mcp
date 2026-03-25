/*
┌─────────────────────────────────────────────────────────────────────────────┐
│              qdrant_store.go — Qdrant 向量数据库 VectorStore 实现             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  基于 Qdrant RESTful API 实现 VectorStore 接口。                             │
│  Qdrant 是 Rust 编写的高性能向量数据库，特点：                               │
│    - 极低延迟（P99 < 10ms @百万级）                                          │
│    - 丰富的过滤条件（支持 JSON payload 上的任意条件组合）                     │
│    - 支持多向量字段（Named Vectors）和稀疏向量                               │
│                                                                             │
│  Qdrant 概念映射:                                                            │
│    VectorStore.EnsureIndex    → Create Collection                           │
│    VectorStore.UpsertVectors  → Upsert Points                               │
│    VectorStore.SearchVectors  → Search Points                               │
│    VectorStore.HybridSearch   → 稀疏+密集向量联合搜索 (Qdrant 原生)         │
│    VectorStore.DeleteByFileID → Delete by filter                            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// QdrantConfig Qdrant 连接配置
type QdrantConfig struct {
	Addr    string        `toml:"addr"`    // http://localhost:6333
	APIKey  string        `toml:"api_key"` // API Key (Qdrant Cloud)
	Timeout time.Duration `toml:"timeout"`
}

// DefaultQdrantConfig 默认 Qdrant 配置
func DefaultQdrantConfig() QdrantConfig {
	return QdrantConfig{
		Addr:    "http://localhost:6333",
		Timeout: 30 * time.Second,
	}
}

// QdrantVectorStore 基于 Qdrant REST API 的向量存储
type QdrantVectorStore struct {
	config     QdrantConfig
	httpClient *http.Client
}

// NewQdrantVectorStore 创建 Qdrant 向量存储实例
func NewQdrantVectorStore(cfg QdrantConfig) *QdrantVectorStore {
	if cfg.Addr == "" {
		cfg.Addr = "http://localhost:6333"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &QdrantVectorStore{
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

// doQdrantRequest 执行 Qdrant REST API 请求
func (s *QdrantVectorStore) doQdrantRequest(ctx context.Context, method, path string, body interface{}) (map[string]interface{}, error) {
	url := strings.TrimSuffix(s.config.Addr, "/") + path

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.config.APIKey != "" {
		req.Header.Set("api-key", s.config.APIKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("qdrant API %s returned %d: %s", path, resp.StatusCode, truncateStr(string(respBody), 500))
	}

	var result map[string]interface{}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return result, nil
}

// EnsureIndex 确保 Qdrant Collection 存在
func (s *QdrantVectorStore) EnsureIndex(ctx context.Context, config IndexConfig) error {
	collectionName := sanitizeCollectionName(config.IndexName)

	// 检查 Collection 是否存在
	_, err := s.doQdrantRequest(ctx, "GET", fmt.Sprintf("/collections/%s", collectionName), nil)
	if err == nil {
		logrus.Infof("[QdrantStore] Collection %s already exists", collectionName)
		return nil
	}

	// 确定 HNSW 参数
	hnswConfig := map[string]interface{}{}
	if config.Algorithm == IndexAlgorithmHNSW {
		params := config.HNSWParams
		if params == nil {
			params = DefaultHNSWParams()
		}
		hnswConfig["m"] = params.M
		hnswConfig["ef_construct"] = params.EFConstruction
	}

	// 创建 Collection
	createBody := map[string]interface{}{
		"vectors": map[string]interface{}{
			"size":     config.Dimension,
			"distance": "Cosine",
		},
	}
	if len(hnswConfig) > 0 {
		createBody["hnsw_config"] = hnswConfig
	}

	_, err = s.doQdrantRequest(ctx, "PUT", fmt.Sprintf("/collections/%s", collectionName), createBody)
	if err != nil {
		return NewRAGError(ErrCodeIndexCreateFailed, config.IndexName, err)
	}

	// 创建 payload 索引，加速按 file_id 过滤
	indexBody := map[string]interface{}{
		"field_name":   "file_id",
		"field_schema": "keyword",
	}
	_, _ = s.doQdrantRequest(ctx, "PUT",
		fmt.Sprintf("/collections/%s/index", collectionName), indexBody)

	// 创建 content 全文索引，支持混合检索中的关键词搜索（问题 1）
	contentIndexBody := map[string]interface{}{
		"field_name":   "content",
		"field_schema": "text",
	}
	_, _ = s.doQdrantRequest(ctx, "PUT",
		fmt.Sprintf("/collections/%s/index", collectionName), contentIndexBody)

	logrus.Infof("[QdrantStore] Created collection %s (dim=%d)", collectionName, config.Dimension)
	return nil
}

// UpsertVectors 批量写入向量
func (s *QdrantVectorStore) UpsertVectors(ctx context.Context, entries []VectorEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	collectionName := sanitizeCollectionName(inferCollectionName(entries[0].Key))

	const batchSize = 500
	totalInserted := 0

	for start := 0; start < len(entries); start += batchSize {
		end := start + batchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[start:end]

		points := make([]map[string]interface{}, len(batch))
		for i, e := range batch {
			// Qdrant 的 point id 支持 UUID string
			pointID := e.Key

			payload := make(map[string]interface{})
			var vectorData []float32

			for k, v := range e.Fields {
				switch val := v.(type) {
				case []byte:
					vectorData = bytesToFloat32Slice(val)
				default:
					payload[k] = val
				}
			}

			points[i] = map[string]interface{}{
				"id":      pointID,
				"vector":  vectorData,
				"payload": payload,
			}
		}

		body := map[string]interface{}{
			"points": points,
		}

		_, err := s.doQdrantRequest(ctx, "PUT",
			fmt.Sprintf("/collections/%s/points", collectionName), body)
		if err != nil {
			logrus.Errorf("[QdrantStore] Upsert batch error: %v", err)
			continue
		}
		totalInserted += len(batch)
	}

	return totalInserted, nil
}

// SearchVectors KNN 向量搜索
func (s *QdrantVectorStore) SearchVectors(ctx context.Context, query VectorQuery) ([]VectorSearchResult, error) {
	collectionName := sanitizeCollectionName(query.IndexName)
	queryVector := bytesToFloat32Slice(query.Vector)

	searchBody := map[string]interface{}{
		"vector":       queryVector,
		"limit":        query.TopK,
		"with_payload": true,
	}

	// 构造过滤条件：优先使用抽象 Filters，降级使用 FilterQuery（问题 11）
	var filter map[string]interface{}
	if len(query.Filters) > 0 {
		filter = buildQdrantFilterFromConditions(query.Filters)
	} else {
		filter = buildQdrantFilter(query.FilterQuery)
	}
	if filter != nil {
		searchBody["filter"] = filter
	}

	result, err := s.doQdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/search", collectionName), searchBody)
	if err != nil {
		return nil, NewRAGError(ErrCodeSearchFailed, query.IndexName, err)
	}

	return parseQdrantSearchResult(result)
}

// HybridSearch 混合搜索
// 双路检索 + RRF 融合：向量搜索 + Qdrant 全文匹配过滤
func (s *QdrantVectorStore) HybridSearch(ctx context.Context, query HybridQuery) ([]VectorSearchResult, error) {
	// 第 1 路: 向量搜索
	vectorResults, err := s.SearchVectors(ctx, query.VectorQuery)
	if err != nil {
		return nil, err
	}
	if query.TextQuery == "" {
		return vectorResults, nil
	}

	// 第 2 路: 使用 Qdrant 的 full-text match 做关键词召回
	collectionName := sanitizeCollectionName(query.IndexName)
	keywords := strings.Fields(query.TextQuery)
	if len(keywords) == 0 {
		return vectorResults, nil
	}

	// 构建 content 字段的 should (OR) 全文匹配条件
	var matchConditions []map[string]interface{}
	for _, kw := range keywords {
		matchConditions = append(matchConditions, map[string]interface{}{
			"key":   "content",
			"match": map[string]interface{}{"text": kw},
		})
	}

	scrollFilter := map[string]interface{}{
		"should": matchConditions,
	}

	// 如果有文件过滤, 包装到 must 中
	var fileFilter map[string]interface{}
	if len(query.Filters) > 0 {
		fileFilter = buildQdrantFilterFromConditions(query.Filters)
	} else {
		fileFilter = buildQdrantFilter(query.FilterQuery)
	}
	if fileFilter != nil {
		scrollFilter = map[string]interface{}{
			"must": []interface{}{
				fileFilter,
				map[string]interface{}{"should": matchConditions},
			},
		}
	}

	expandedTopK := query.TopK * 3
	scrollBody := map[string]interface{}{
		"filter":       scrollFilter,
		"limit":        expandedTopK,
		"with_payload": true,
	}

	scrollResult, err := s.doQdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/scroll", collectionName), scrollBody)
	var textResults []VectorSearchResult
	if err == nil {
		textResults = parseQdrantScrollAsSearchResult(scrollResult)
	} else {
		logrus.Warnf("[QdrantStore] Text scroll failed, falling back to vector-only: %v", err)
		return vectorResults, nil
	}

	return mergeByRRF(vectorResults, textResults, query.VectorWeight, query.KeywordWeight, query.TopK), nil
}

// parseQdrantScrollAsSearchResult 将 scroll 结果转为 VectorSearchResult 以复用 RRF
func parseQdrantScrollAsSearchResult(result map[string]interface{}) []VectorSearchResult {
	var results []VectorSearchResult
	resultData, ok := result["result"].(map[string]interface{})
	if !ok {
		return results
	}
	points, ok := resultData["points"].([]interface{})
	if !ok {
		return results
	}
	for _, p := range points {
		point, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		vsr := VectorSearchResult{Fields: make(map[string]string)}
		if id, ok := point["id"].(string); ok {
			vsr.Key = id
		}
		if payload, ok := point["payload"].(map[string]interface{}); ok {
			for k, v := range payload {
				vsr.Fields[k] = fmt.Sprintf("%v", v)
			}
		}
		results = append(results, vsr)
	}
	return results
}

// DeleteByFileID 按 file_id 删除，先计数再删除
func (s *QdrantVectorStore) DeleteByFileID(ctx context.Context, indexName, prefix, fileID string) (int64, error) {
	collectionName := sanitizeCollectionName(indexName)

	filterObj := map[string]interface{}{
		"must": []map[string]interface{}{
			{"key": "file_id", "match": map[string]interface{}{"value": fileID}},
		},
	}

	// 先 count：scroll 仅拿 id 计数
	countBody := map[string]interface{}{
		"filter":       filterObj,
		"limit":        10000,
		"with_payload": false,
		"with_vector":  false,
	}
	var deletedCount int64
	countResult, err := s.doQdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/scroll", collectionName), countBody)
	if err == nil {
		if resultData, ok := countResult["result"].(map[string]interface{}); ok {
			if points, ok := resultData["points"].([]interface{}); ok {
				deletedCount = int64(len(points))
			}
		}
	}

	// 执行删除
	deleteBody := map[string]interface{}{"filter": filterObj}
	_, err = s.doQdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/delete", collectionName), deleteBody)
	if err != nil {
		return 0, fmt.Errorf("delete by file_id: %w", err)
	}

	logrus.Infof("[QdrantStore] Deleted ~%d points for file_id=%s", deletedCount, fileID)
	return deletedCount, nil
}

// GetDocumentChunks 获取文档分块（分页查询，避免硬编码上限）
func (s *QdrantVectorStore) GetDocumentChunks(ctx context.Context, indexName, prefix, fileID string) ([]string, error) {
	collectionName := sanitizeCollectionName(indexName)

	const pageSize = 1000
	var allChunks []string
	var nextPageOffset interface{}

	for {
		body := map[string]interface{}{
			"filter": map[string]interface{}{
				"must": []map[string]interface{}{
					{"key": "file_id", "match": map[string]interface{}{"value": fileID}},
				},
			},
			"limit":        pageSize,
			"with_payload": true,
		}
		if nextPageOffset != nil {
			body["offset"] = nextPageOffset
		}

		result, err := s.doQdrantRequest(ctx, "POST",
			fmt.Sprintf("/collections/%s/points/scroll", collectionName), body)
		if err != nil {
			if len(allChunks) > 0 {
				break
			}
			return nil, err
		}

		pageCount := 0
		if resultData, ok := result["result"].(map[string]interface{}); ok {
			if points, ok := resultData["points"].([]interface{}); ok {
				for _, p := range points {
					if point, ok := p.(map[string]interface{}); ok {
						if payload, ok := point["payload"].(map[string]interface{}); ok {
							if content, ok := payload["content"].(string); ok {
								allChunks = append(allChunks, content)
								pageCount++
							}
						}
					}
				}
			}
			nextPageOffset = resultData["next_page_offset"]
		}

		if pageCount < pageSize || nextPageOffset == nil {
			break
		}
	}

	return allChunks, nil
}

// ListDocuments 列出所有文档
func (s *QdrantVectorStore) ListDocuments(ctx context.Context, indexName string) ([]DocumentMeta, error) {
	collectionName := sanitizeCollectionName(indexName)

	body := map[string]interface{}{
		"limit":        10000,
		"with_payload": map[string]interface{}{"include": []string{"file_id", "file_name"}},
	}

	result, err := s.doQdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/scroll", collectionName), body)
	if err != nil {
		return nil, err
	}

	docMap := make(map[string]*DocumentMeta)
	if resultData, ok := result["result"].(map[string]interface{}); ok {
		if points, ok := resultData["points"].([]interface{}); ok {
			for _, p := range points {
				if point, ok := p.(map[string]interface{}); ok {
					if payload, ok := point["payload"].(map[string]interface{}); ok {
						fid, _ := payload["file_id"].(string)
						fname, _ := payload["file_name"].(string)
						if fid == "" {
							continue
						}
						if dm, exists := docMap[fid]; exists {
							dm.ChunkCount++
						} else {
							docMap[fid] = &DocumentMeta{FileID: fid, FileName: fname, ChunkCount: 1}
						}
					}
				}
			}
		}
	}

	docs := make([]DocumentMeta, 0, len(docMap))
	for _, dm := range docMap {
		docs = append(docs, *dm)
	}
	return docs, nil
}

// Close 关闭
func (s *QdrantVectorStore) Close() error {
	return nil
}

// --- 辅助函数 ---

// sanitizeCollectionName 将 Redis 风格的索引名转为 Qdrant 合法的 collection 名
func sanitizeCollectionName(name string) string {
	name = strings.ReplaceAll(name, ":", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

// buildQdrantFilter 将 RediSearch 过滤语法转为 Qdrant filter
func buildQdrantFilter(redisFilter string) map[string]interface{} {
	if redisFilter == "" || redisFilter == "*" {
		return nil
	}

	// @file_id:{id1|id2} → must: [{key: "file_id", match: {any: ["id1","id2"]}}]
	if strings.HasPrefix(redisFilter, "@file_id:{") {
		inner := strings.TrimPrefix(redisFilter, "@file_id:{")
		inner = strings.TrimSuffix(inner, "}")
		ids := strings.Split(inner, "|")
		cleanIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			id = strings.TrimSpace(id)
			id = strings.ReplaceAll(id, "\\", "")
			if id != "" {
				cleanIDs = append(cleanIDs, id)
			}
		}

		if len(cleanIDs) == 1 {
			return map[string]interface{}{
				"must": []map[string]interface{}{
					{
						"key":   "file_id",
						"match": map[string]interface{}{"value": cleanIDs[0]},
					},
				},
			}
		}

		return map[string]interface{}{
			"should": func() []map[string]interface{} {
				conditions := make([]map[string]interface{}, len(cleanIDs))
				for i, id := range cleanIDs {
					conditions[i] = map[string]interface{}{
						"key":   "file_id",
						"match": map[string]interface{}{"value": id},
					}
				}
				return conditions
			}(),
		}
	}

	return nil
}

// parseQdrantSearchResult 解析 Qdrant 搜索结果
func parseQdrantSearchResult(result map[string]interface{}) ([]VectorSearchResult, error) {
	var results []VectorSearchResult

	data, ok := result["result"].([]interface{})
	if !ok {
		return results, nil
	}

	for _, item := range data {
		point, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		vsr := VectorSearchResult{
			Fields: make(map[string]string),
		}

		if id, ok := point["id"].(string); ok {
			vsr.Key = id
		}
		if score, ok := point["score"].(float64); ok {
			vsr.Score = score
			// Qdrant cosine distance → relevance score
			vsr.Fields["distance"] = fmt.Sprintf("%f", 1-score)
		}

		if payload, ok := point["payload"].(map[string]interface{}); ok {
			for k, v := range payload {
				vsr.Fields[k] = fmt.Sprintf("%v", v)
			}
		}

		results = append(results, vsr)
	}

	return results, nil
}

// buildQdrantFilterFromConditions 将抽象 FilterCondition 转为 Qdrant filter
func buildQdrantFilterFromConditions(filters []FilterCondition) map[string]interface{} {
	if len(filters) == 0 {
		return nil
	}
	var mustClauses []interface{}
	for _, f := range filters {
		switch f.Op {
		case FilterOpIn:
			if len(f.Values) == 1 {
				mustClauses = append(mustClauses, map[string]interface{}{
					"key": f.Field, "match": map[string]interface{}{"value": f.Values[0]},
				})
			} else {
				var should []map[string]interface{}
				for _, v := range f.Values {
					should = append(should, map[string]interface{}{
						"key": f.Field, "match": map[string]interface{}{"value": v},
					})
				}
				mustClauses = append(mustClauses, map[string]interface{}{"should": should})
			}
		case FilterOpEqual:
			if len(f.Values) > 0 {
				mustClauses = append(mustClauses, map[string]interface{}{
					"key": f.Field, "match": map[string]interface{}{"value": f.Values[0]},
				})
			}
		}
	}
	if len(mustClauses) == 0 {
		return nil
	}
	return map[string]interface{}{"must": mustClauses}
}
