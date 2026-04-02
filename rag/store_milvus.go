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
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// MilvusConfig Milvus 连接配置
type MilvusConfig struct {
	Addr       string        `toml:"addr"`        // http://localhost:19530
	Token      string        `toml:"token"`       // API Token (Milvus Cloud)
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
	config             MilvusConfig
	httpClient         *http.Client
	mu                 sync.RWMutex // 保护 lastCollectionName 的并发安全
	lastCollectionName string       // 最近一次 EnsureIndex 创建的 collection 名（用于 UpsertVectors 推断）
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
	// 配置连接池：MaxIdleConnsPerHost 默认仅 2，高并发下严重不足
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 30,
		IdleConnTimeout:     90 * time.Second,
	}
	return &MilvusVectorStore{
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout, Transport: transport},
	}
}

// setLastCollection 线程安全地设置 lastCollectionName
func (s *MilvusVectorStore) setLastCollection(name string) {
	s.mu.Lock()
	s.lastCollectionName = name
	s.mu.Unlock()
}

// getLastCollection 线程安全地获取 lastCollectionName
func (s *MilvusVectorStore) getLastCollection() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastCollectionName
}

// doRequest 执行 Milvus REST API 请求（带自动重试，最多 3 次）
func (s *MilvusVectorStore) doRequest(ctx context.Context, method, path string, body interface{}) (map[string]interface{}, error) {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		result, err := s.doRequestOnce(ctx, method, path, body)
		if err == nil {
			return result, nil
		}
		lastErr = err
		// 仅对网络层错误重试，Milvus 业务错误（如 collection not found）不重试
		if strings.Contains(err.Error(), "milvus error") {
			return result, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		backoff := time.Duration(1<<uint(attempt)) * 200 * time.Millisecond
		logrus.Warnf("[MilvusStore] Request %s %s failed (attempt %d/%d): %v, retrying in %v", method, path, attempt+1, maxRetries, err, backoff)
		time.Sleep(backoff)
	}
	return nil, fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

// doRequestOnce 执行单次 Milvus REST API 请求
func (s *MilvusVectorStore) doRequestOnce(ctx context.Context, method, path string, body interface{}) (map[string]interface{}, error) {
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
		s.setLastCollection(config.IndexName)
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

	// 缓存最近创建的 collection 名，供 UpsertVectors 使用
	s.setLastCollection(config.IndexName)
	return nil
}

// UpsertVectors 批量写入向量数据
func (s *MilvusVectorStore) UpsertVectors(ctx context.Context, entries []VectorEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	// 优先使用 EnsureIndex 缓存的 collection 名，否则从 Key 推断
	collectionName := s.getLastCollection()
	if collectionName == "" {
		collectionName = inferCollectionName(entries[0].Key)
	}

	const batchSize = 500
	totalInserted := 0
	var batchErrors []string

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
			logrus.Errorf("[MilvusStore] Upsert batch [%d:%d] error: %v", start, end, err)
			batchErrors = append(batchErrors, fmt.Sprintf("batch[%d:%d]: %v", start, end, err))
			continue
		}
		totalInserted += len(batch)
	}

	// 部分批次失败时返回错误（附带已成功数量），调用方可据此判断
	if len(batchErrors) > 0 && totalInserted < len(entries) {
		return totalInserted, fmt.Errorf("partial upsert: %d/%d inserted, errors: %s",
			totalInserted, len(entries), strings.Join(batchErrors, "; "))
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

	// 构造过滤表达式：优先使用抽象 Filters，降级使用 FilterQuery（问题 11）
	filterExpr := ""
	if len(query.Filters) > 0 {
		filterExpr = buildMilvusFilter(query.Filters)
	} else if query.FilterQuery != "" && query.FilterQuery != "*" {
		filterExpr = convertToMilvusFilter(query.FilterQuery)
	}
	if filterExpr != "" {
		searchBody["filter"] = filterExpr
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
// 双路检索 + RRF 融合：向量搜索 + content 字段 like 过滤模拟全文检索
func (s *MilvusVectorStore) HybridSearch(ctx context.Context, query HybridQuery) ([]VectorSearchResult, error) {
	// 第 1 路: 向量搜索
	vectorResults, err := s.SearchVectors(ctx, query.VectorQuery)
	if err != nil {
		return nil, err
	}
	if query.TextQuery == "" {
		return vectorResults, nil
	}

	// 第 2 路: 基于 content 字段的文本匹配搜索
	keywords := strings.Fields(query.TextQuery)
	if len(keywords) == 0 {
		return vectorResults, nil
	}

	var filterParts []string
	for _, kw := range keywords {
		kw = strings.ReplaceAll(kw, `"`, `\"`)
		filterParts = append(filterParts, fmt.Sprintf(`content like "%%%s%%"`, kw))
	}
	textFilter := strings.Join(filterParts, " or ")

	if len(query.Filters) > 0 {
		mf := buildMilvusFilter(query.Filters)
		if mf != "" {
			textFilter = fmt.Sprintf("(%s) and (%s)", mf, textFilter)
		}
	} else if query.FilterQuery != "" && query.FilterQuery != "*" {
		mf := convertToMilvusFilter(query.FilterQuery)
		if mf != "" {
			textFilter = fmt.Sprintf("(%s) and (%s)", mf, textFilter)
		}
	}

	expandedTopK := query.TopK * 3
	textBody := map[string]interface{}{
		"collectionName": query.IndexName,
		"filter":         textFilter,
		"outputFields":   query.ReturnFields,
		"limit":          expandedTopK,
	}
	if s.config.Database != "" {
		textBody["dbName"] = s.config.Database
	}

	textResult, err := s.doRequest(ctx, "POST", "/v2/vectordb/entities/query", textBody)
	var textResults []VectorSearchResult
	if err == nil {
		textResults = parseMilvusQueryAsSearchResult(textResult)
	} else {
		logrus.Warnf("[MilvusStore] Text query failed, falling back to vector-only: %v", err)
		return vectorResults, nil
	}

	return mergeByRRF(vectorResults, textResults, query.VectorWeight, query.KeywordWeight, query.TopK), nil
}

// parseMilvusQueryAsSearchResult 将 query 结果转为 VectorSearchResult 以复用 RRF
func parseMilvusQueryAsSearchResult(result map[string]interface{}) []VectorSearchResult {
	var results []VectorSearchResult
	data, ok := result["data"].([]interface{})
	if !ok {
		return results
	}
	for _, item := range data {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		vsr := VectorSearchResult{Fields: make(map[string]string)}
		if id, ok := row["id"].(string); ok {
			vsr.Key = id
		}
		for k, v := range row {
			if k == "id" {
				continue
			}
			vsr.Fields[k] = fmt.Sprintf("%v", v)
		}
		results = append(results, vsr)
	}
	return results
}

// DeleteByFileID 按 file_id 删除，先计数再删除
func (s *MilvusVectorStore) DeleteByFileID(ctx context.Context, indexName, prefix, fileID string) (int64, error) {
	filterExpr := fmt.Sprintf("file_id == \"%s\"", sanitizeMilvusString(fileID))

	// 先统计匹配数量
	countBody := map[string]interface{}{
		"collectionName": indexName,
		"filter":         filterExpr,
		"outputFields":   []string{"id"},
		"limit":          10000,
	}
	if s.config.Database != "" {
		countBody["dbName"] = s.config.Database
	}

	var deletedCount int64
	countResult, err := s.doRequest(ctx, "POST", "/v2/vectordb/entities/query", countBody)
	if err == nil {
		if data, ok := countResult["data"].([]interface{}); ok {
			deletedCount = int64(len(data))
		}
	}

	// 执行删除
	body := map[string]interface{}{
		"collectionName": indexName,
		"filter":         filterExpr,
	}
	if s.config.Database != "" {
		body["dbName"] = s.config.Database
	}

	_, err = s.doRequest(ctx, "POST", "/v2/vectordb/entities/delete", body)
	if err != nil {
		return 0, fmt.Errorf("delete by file_id: %w", err)
	}

	logrus.Infof("[MilvusStore] Deleted ~%d entities for file_id=%s", deletedCount, fileID)
	return deletedCount, nil
}

// GetDocumentChunks 获取文档分块（分页查询，避免硬编码上限）
func (s *MilvusVectorStore) GetDocumentChunks(ctx context.Context, indexName, prefix, fileID string) ([]string, error) {
	const pageSize = 1000
	var allChunks []string

	for offset := 0; ; offset += pageSize {
		body := map[string]interface{}{
			"collectionName": indexName,
			"filter":         fmt.Sprintf("file_id == \"%s\"", sanitizeMilvusString(fileID)),
			"outputFields":   []string{"content", "chunk_index"},
			"limit":          pageSize,
			"offset":         offset,
		}
		if s.config.Database != "" {
			body["dbName"] = s.config.Database
		}

		result, err := s.doRequest(ctx, "POST", "/v2/vectordb/entities/query", body)
		if err != nil {
			if len(allChunks) > 0 {
				break
			}
			return nil, err
		}

		pageCount := 0
		if data, ok := result["data"].([]interface{}); ok {
			for _, item := range data {
				if row, ok := item.(map[string]interface{}); ok {
					if content, ok := row["content"].(string); ok {
						allChunks = append(allChunks, content)
						pageCount++
					}
				}
			}
		}

		if pageCount < pageSize {
			break
		}
	}

	return allChunks, nil
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

// sanitizeMilvusString 防止 Milvus 过滤表达式注入
// 移除双引号和反斜杠等可能改变表达式语义的字符
func sanitizeMilvusString(s string) string {
	s = strings.ReplaceAll(s, `\`, ``)
	s = strings.ReplaceAll(s, `"`, ``)
	s = strings.ReplaceAll(s, `'`, ``)
	return s
}

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

// buildMilvusFilter 将抽象 FilterCondition 转为 Milvus 表达式
func buildMilvusFilter(filters []FilterCondition) string {
	if len(filters) == 0 {
		return ""
	}
	var parts []string
	for _, f := range filters {
		switch f.Op {
		case FilterOpIn:
			quoted := make([]string, len(f.Values))
			for i, v := range f.Values {
				quoted[i] = fmt.Sprintf(`"%s"`, strings.ReplaceAll(v, `"`, `\"`))
			}
			parts = append(parts, fmt.Sprintf("%s in [%s]", f.Field, strings.Join(quoted, ", ")))
		case FilterOpEqual:
			if len(f.Values) > 0 {
				parts = append(parts, fmt.Sprintf(`%s == "%s"`, f.Field, f.Values[0]))
			}
		}
	}
	return strings.Join(parts, " and ")
}
