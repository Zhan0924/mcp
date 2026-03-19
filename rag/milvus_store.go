/*
┌─────────────────────────────────────────────────────────────────────────────┐
│              milvus_store.go — Milvus 向量数据库 VectorStore 实现             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  基于 Milvus RESTful API v2 实现 VectorStore 接口。                          │
│  使用 HTTP 而非 gRPC SDK，避免引入重量级依赖（protobuf + gRPC）。            │
│                                                                             │
│  Milvus 概念映射:                                                            │
│    VectorStore.EnsureIndex    → 创建 Collection + Index                     │
│    VectorStore.UpsertVectors  → Insert/Upsert 数据                          │
│    VectorStore.SearchVectors  → ANN Search                                  │
│    VectorStore.HybridSearch   → Milvus 2.4+ Hybrid Search                  │
│    VectorStore.DeleteByFileID → Delete by expression                        │
│                                                                             │
│  适用场景: 百万级以上向量，需要 GPU 加速或复杂过滤条件的场景                  │
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
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// MilvusConfig Milvus 连接配置
type MilvusConfig struct {
	Addr       string        `toml:"addr"`       // http://localhost:19530
	Token      string        `toml:"token"`      // API Token (Milvus Cloud)
	Database   string        `toml:"database"`    // 数据库名
	Timeout    time.Duration `toml:"timeout"`     // HTTP 超时
	MetricType string        `toml:"metric_type"` // COSINE / L2 / IP
}

// DefaultMilvusConfig 默认 Milvus 配置
func DefaultMilvusConfig() MilvusConfig {
	return MilvusConfig{
		Addr:       "http://localhost:19530",
		Database:   "default",
		Timeout:    30 * time.Second,
		MetricType: "COSINE",
	}
}

// MilvusVectorStore 基于 Milvus RESTful API 的向量存储
type MilvusVectorStore struct {
	config     MilvusConfig
	httpClient *http.Client
}

// NewMilvusVectorStore 创建 Milvus 向量存储实例
func NewMilvusVectorStore(cfg MilvusConfig) *MilvusVectorStore {
	if cfg.Addr == "" {
		cfg.Addr = "http://localhost:19530"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MetricType == "" {
		cfg.MetricType = "COSINE"
	}
	return &MilvusVectorStore{
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

// doRequest 执行 Milvus REST API 请求
func (s *MilvusVectorStore) doRequest(ctx context.Context, method, path string, body interface{}) (map[string]interface{}, error) {
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
	req.Header.Set("Accept", "application/json")
	if s.config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.config.Token)
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

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w (body: %s)", err, truncateStr(string(respBody), 500))
	}

	// 检查 Milvus 错误码
	if code, ok := result["code"].(float64); ok && code != 0 {
		msg, _ := result["message"].(string)
		return result, fmt.Errorf("milvus error %d: %s", int(code), msg)
	}

	return result, nil
}

// EnsureIndex 确保 Milvus Collection 和索引存在
func (s *MilvusVectorStore) EnsureIndex(ctx context.Context, config IndexConfig) error {
	// 检查 Collection 是否已存在
	checkBody := map[string]interface{}{
		"collectionName": config.IndexName,
	}
	if s.config.Database != "" {
		checkBody["dbName"] = s.config.Database
	}

	_, err := s.doRequest(ctx, "POST", "/v2/vectordb/collections/describe", checkBody)
	if err == nil {
		logrus.Infof("[MilvusStore] Collection %s already exists", config.IndexName)
		return nil
	}

	// 创建 Collection
	// Milvus 要求至少一个主键字段和一个向量字段
	schema := map[string]interface{}{
		"autoId": false,
		"fields": []map[string]interface{}{
			{"fieldName": "id", "dataType": "VarChar", "isPrimary": true, "elementTypeParams": map[string]interface{}{"max_length": "256"}},
			{"fieldName": "content", "dataType": "VarChar", "elementTypeParams": map[string]interface{}{"max_length": "65535"}},
			{"fieldName": "file_id", "dataType": "VarChar", "elementTypeParams": map[string]interface{}{"max_length": "256"}},
			{"fieldName": "file_name", "dataType": "VarChar", "elementTypeParams": map[string]interface{}{"max_length": "512"}},
			{"fieldName": "chunk_id", "dataType": "VarChar", "elementTypeParams": map[string]interface{}{"max_length": "256"}},
			{"fieldName": "chunk_index", "dataType": "Int32"},
			{"fieldName": "parent_chunk_id", "dataType": "VarChar", "elementTypeParams": map[string]interface{}{"max_length": "256"}},
			{
				"fieldName": config.VectorFieldName,
				"dataType":  "FloatVector",
				"elementTypeParams": map[string]interface{}{
					"dim": fmt.Sprintf("%d", config.Dimension),
				},
			},
		},
	}

	// 确定索引类型
	indexType := "FLAT"
	indexParams := map[string]interface{}{}
	if config.Algorithm == IndexAlgorithmHNSW {
		indexType = "HNSW"
		params := config.HNSWParams
		if params == nil {
			params = DefaultHNSWParams()
		}
		indexParams["M"] = params.M
		indexParams["efConstruction"] = params.EFConstruction
	}

	createBody := map[string]interface{}{
		"collectionName": config.IndexName,
		"schema":         schema,
		"indexParams": []map[string]interface{}{
			{
				"fieldName":  config.VectorFieldName,
				"indexName":  "vector_idx",
				"metricType": s.config.MetricType,
				"indexType":  indexType,
				"params":     indexParams,
			},
		},
	}
	if s.config.Database != "" {
		createBody["dbName"] = s.config.Database
	}

	_, err = s.doRequest(ctx, "POST", "/v2/vectordb/collections/create", createBody)
	if err != nil {
		return NewRAGError(ErrCodeIndexCreateFailed, config.IndexName, err)
	}

	logrus.Infof("[MilvusStore] Created collection %s (dim=%d, algo=%s)", config.IndexName, config.Dimension, config.Algorithm)
	return nil
}

// UpsertVectors 批量写入向量数据
func (s *MilvusVectorStore) UpsertVectors(ctx context.Context, entries []VectorEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	// 按 collection 分组（从 Key 中解析 collection 名，或使用默认）
	// 这里假设所有 entries 属于同一个 collection
	// 从第一个 entry 的 Key 前缀推断 collection name
	collectionName := inferCollectionName(entries[0].Key)

	const batchSize = 500
	totalInserted := 0

	for start := 0; start < len(entries); start += batchSize {
		end := start + batchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[start:end]

		data := make([]map[string]interface{}, len(batch))
		for i, e := range batch {
			row := map[string]interface{}{
				"id": e.Key,
			}
			for k, v := range e.Fields {
				switch val := v.(type) {
				case []byte:
					// 将 []byte 向量转为 []float32
					row[k] = bytesToFloat32Slice(val)
				default:
					row[k] = val
				}
			}
			data[i] = row
		}

		body := map[string]interface{}{
			"collectionName": collectionName,
			"data":           data,
		}
		if s.config.Database != "" {
			body["dbName"] = s.config.Database
		}

		_, err := s.doRequest(ctx, "POST", "/v2/vectordb/entities/upsert", body)
		if err != nil {
			logrus.Errorf("[MilvusStore] Upsert batch error: %v", err)
			continue
		}
		totalInserted += len(batch)
	}

	return totalInserted, nil
}

// SearchVectors KNN 向量搜索
func (s *MilvusVectorStore) SearchVectors(ctx context.Context, query VectorQuery) ([]VectorSearchResult, error) {
	vectorField := query.VectorFieldName
	if vectorField == "" {
		vectorField = "vector"
	}

	queryVector := bytesToFloat32Slice(query.Vector)

	searchBody := map[string]interface{}{
		"collectionName": query.IndexName,
		"data":           [][]float32{queryVector},
		"annsField":      vectorField,
		"limit":          query.TopK,
		"outputFields":   query.ReturnFields,
	}

	// 构造过滤表达式
	if query.FilterQuery != "" && query.FilterQuery != "*" {
		searchBody["filter"] = convertToMilvusFilter(query.FilterQuery)
	}

	if s.config.Database != "" {
		searchBody["dbName"] = s.config.Database
	}

	result, err := s.doRequest(ctx, "POST", "/v2/vectordb/entities/search", searchBody)
	if err != nil {
		return nil, NewRAGError(ErrCodeSearchFailed, query.IndexName, err)
	}

	return parseMilvusSearchResult(result)
}

// HybridSearch Milvus 混合搜索
// Milvus 2.4+ 支持 multi-vector search + Ranker 融合
func (s *MilvusVectorStore) HybridSearch(ctx context.Context, query HybridQuery) ([]VectorSearchResult, error) {
	// Milvus 的 hybrid search 需要多个 ANN 请求 + RRF/WeightedRanker
	// 当前退化为纯向量搜索 + 客户端过滤
	return s.SearchVectors(ctx, query.VectorQuery)
}

// DeleteByFileID 按 file_id 删除
func (s *MilvusVectorStore) DeleteByFileID(ctx context.Context, indexName, prefix, fileID string) (int64, error) {
	body := map[string]interface{}{
		"collectionName": indexName,
		"filter":         fmt.Sprintf("file_id == \"%s\"", fileID),
	}
	if s.config.Database != "" {
		body["dbName"] = s.config.Database
	}

	_, err := s.doRequest(ctx, "POST", "/v2/vectordb/entities/delete", body)
	if err != nil {
		return 0, fmt.Errorf("delete by file_id: %w", err)
	}
	return 0, nil // Milvus delete 不返回具体数量
}

// GetDocumentChunks 获取文档分块
func (s *MilvusVectorStore) GetDocumentChunks(ctx context.Context, indexName, prefix, fileID string) ([]string, error) {
	body := map[string]interface{}{
		"collectionName": indexName,
		"filter":         fmt.Sprintf("file_id == \"%s\"", fileID),
		"outputFields":   []string{"content", "chunk_index"},
		"limit":          10000,
	}
	if s.config.Database != "" {
		body["dbName"] = s.config.Database
	}

	result, err := s.doRequest(ctx, "POST", "/v2/vectordb/entities/query", body)
	if err != nil {
		return nil, err
	}

	var chunks []string
	if data, ok := result["data"].([]interface{}); ok {
		for _, item := range data {
			if row, ok := item.(map[string]interface{}); ok {
				if content, ok := row["content"].(string); ok {
					chunks = append(chunks, content)
				}
			}
		}
	}
	return chunks, nil
}

// ListDocuments 列出文档
func (s *MilvusVectorStore) ListDocuments(ctx context.Context, indexName string) ([]DocumentMeta, error) {
	body := map[string]interface{}{
		"collectionName": indexName,
		"outputFields":   []string{"file_id", "file_name"},
		"limit":          10000,
	}
	if s.config.Database != "" {
		body["dbName"] = s.config.Database
	}

	result, err := s.doRequest(ctx, "POST", "/v2/vectordb/entities/query", body)
	if err != nil {
		return nil, err
	}

	// 手动去重统计
	docMap := make(map[string]*DocumentMeta)
	if data, ok := result["data"].([]interface{}); ok {
		for _, item := range data {
			if row, ok := item.(map[string]interface{}); ok {
				fid, _ := row["file_id"].(string)
				fname, _ := row["file_name"].(string)
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

	docs := make([]DocumentMeta, 0, len(docMap))
	for _, dm := range docMap {
		docs = append(docs, *dm)
	}
	return docs, nil
}

// Close 关闭连接
func (s *MilvusVectorStore) Close() error {
	return nil
}

// --- 辅助函数 ---

// bytesToFloat32Slice 将字节切片还原为 float32 切片
func bytesToFloat32Slice(data []byte) []float32 {
	if len(data)%4 != 0 {
		return nil
	}
	result := make([]float32, len(data)/4)
	for i := range result {
		bits := uint32(data[i*4]) | uint32(data[i*4+1])<<8 | uint32(data[i*4+2])<<16 | uint32(data[i*4+3])<<24
		result[i] = math.Float32frombits(bits)
	}
	return result
}

// inferCollectionName 从 Key 前缀推断 Collection 名称
func inferCollectionName(key string) string {
	// Key 格式: "mcp_rag_user_1:chunk-xxx" → collection = "mcp_rag_user_1"
	parts := strings.SplitN(key, ":", 2)
	if len(parts) > 0 {
		return strings.ReplaceAll(parts[0], ":", "_")
	}
	return "default_collection"
}

// convertToMilvusFilter 将 RediSearch 过滤语法转为 Milvus 表达式
func convertToMilvusFilter(redisFilter string) string {
	// @file_id:{id1|id2} → file_id in ["id1", "id2"]
	if strings.HasPrefix(redisFilter, "@file_id:{") {
		inner := strings.TrimPrefix(redisFilter, "@file_id:{")
		inner = strings.TrimSuffix(inner, "}")
		ids := strings.Split(inner, "|")
		quoted := make([]string, len(ids))
		for i, id := range ids {
			id = strings.TrimSpace(id)
			id = strings.ReplaceAll(id, "\\", "") // 移除转义
			quoted[i] = fmt.Sprintf(`"%s"`, id)
		}
		return fmt.Sprintf("file_id in [%s]", strings.Join(quoted, ", "))
	}
	return ""
}

// parseMilvusSearchResult 解析 Milvus 搜索结果
func parseMilvusSearchResult(result map[string]interface{}) ([]VectorSearchResult, error) {
	var results []VectorSearchResult

	data, ok := result["data"].([]interface{})
	if !ok {
		return results, nil
	}

	for _, item := range data {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		vsr := VectorSearchResult{
			Fields: make(map[string]string),
		}

		if id, ok := row["id"].(string); ok {
			vsr.Key = id
		}
		if distance, ok := row["distance"].(float64); ok {
			vsr.Fields["distance"] = fmt.Sprintf("%f", distance)
		}

		for k, v := range row {
			if k == "id" || k == "distance" {
				continue
			}
			vsr.Fields[k] = fmt.Sprintf("%v", v)
		}

		results = append(results, vsr)
	}

	return results, nil
}
