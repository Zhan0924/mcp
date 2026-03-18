/*
┌──────────────────────────────────────────────────────────────────────────────┐
│ reranker.go — RAG 检索结果重排序引擎                                           │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标:                                                                       │
│  - 使用外部 Rerank 模型对候选片段重排序，提高最终相关性                       │
│  - 失败时优雅降级为分数排序，确保检索链路不因外部依赖中断                     │
│                                                                              │
│ 结构:                                                                       │
│  - 接口: Reranker                                                           │
│  - 配置: RerankConfig / DefaultRerankConfig                                 │
│  - 实现: DashScopeReranker / GenericHTTPReranker / ScoreReranker             │
│  - 全局: InitGlobalReranker / GetGlobalReranker / RerankResults              │
│  - 辅助: GetEffectiveRecallTopK / GetRerankModelInfo                         │
└──────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// Reranker 重排序接口
type Reranker interface {
	Rerank(ctx context.Context, query string, documents []RetrievalResult, topN int) ([]RetrievalResult, error)
}

// RerankConfig 重排序配置
type RerankConfig struct {
	Enabled    bool          `toml:"enabled"`
	Provider   string        `toml:"provider"` // dashscope / cohere / jina / custom
	BaseURL    string        `toml:"base_url"` // 自定义 URL (不填则按 provider 自动选择)
	APIKey     string        `toml:"api_key"`
	Model      string        `toml:"model"` // qwen3-rerank / gte-rerank-v2 / qwen3-vl-rerank
	TopN       int           `toml:"top_n"`
	RecallTopK int           `toml:"recall_top_k"`
	Timeout    time.Duration `toml:"timeout"`
	Instruct   string        `toml:"instruct"` // qwen3-rerank 排序任务说明
}

// DefaultRerankConfig 默认 Rerank 配置
func DefaultRerankConfig() RerankConfig {
	return RerankConfig{
		Enabled:    false,
		Provider:   "dashscope",
		Model:      "qwen3-rerank",
		TopN:       5,
		RecallTopK: 20,
		Timeout:    15 * time.Second,
		Instruct:   "Given a web search query, retrieve relevant passages that answer the query.",
	}
}

// ================================================================
// DashScope Reranker — 支持 qwen3-rerank 和 gte-rerank-v2
// ================================================================

// DashScopeReranker 阿里云百炼 Rerank 服务
type DashScopeReranker struct {
	config RerankConfig
	client *http.Client
}

func NewDashScopeReranker(config RerankConfig) *DashScopeReranker {
	return &DashScopeReranker{
		config: config,
		// 独立 HTTP Client 便于控制超时与连接池，避免拖慢检索主链路
		client: &http.Client{Timeout: config.Timeout},
	}
}

// --- qwen3-rerank 兼容 API 请求/响应 ---

type qwen3RerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
	Instruct  string   `json:"instruct,omitempty"`
}

type qwen3RerankResponse struct {
	Results []qwen3RerankResult `json:"results"`
}

type qwen3RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// --- gte-rerank-v2 原生 API 请求/响应 ---

type gteRerankRequest struct {
	Model      string          `json:"model"`
	Input      gteRerankInput  `json:"input"`
	Parameters *gteRerankParam `json:"parameters,omitempty"`
}

type gteRerankInput struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type gteRerankParam struct {
	TopN            int  `json:"top_n,omitempty"`
	ReturnDocuments bool `json:"return_documents,omitempty"`
}

type gteRerankResponse struct {
	Output    gteRerankOutput `json:"output"`
	RequestID string          `json:"request_id"`
	Code      string          `json:"code,omitempty"`
	Message   string          `json:"message,omitempty"`
}

type gteRerankOutput struct {
	Results []gteRerankResult `json:"results"`
}

type gteRerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

func (r *DashScopeReranker) Rerank(ctx context.Context, query string, documents []RetrievalResult, topN int) ([]RetrievalResult, error) {
	if len(documents) == 0 {
		return documents, nil
	}

	if topN <= 0 {
		topN = r.config.TopN
	}
	if topN <= 0 {
		topN = 5
	}
	if topN > len(documents) {
		topN = len(documents)
	}

	docs := make([]string, len(documents))
	for i, d := range documents {
		docs[i] = d.Content
	}

	model := r.config.Model
	if model == "" {
		model = "qwen3-rerank"
	}

	// 根据模型选择 API 格式
	if isQwen3Rerank(model) {
		return r.rerankQwen3(ctx, model, query, docs, documents, topN)
	}
	return r.rerankGTE(ctx, model, query, docs, documents, topN)
}

// isQwen3Rerank 判断是否使用 qwen3-rerank 兼容 API 格式
func isQwen3Rerank(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "qwen3-rerank") || strings.HasPrefix(m, "qwen3-vl-rerank")
}

// rerankQwen3 使用 qwen3-rerank 兼容 API
// POST https://dashscope.aliyuncs.com/compatible-api/v1/reranks
func (r *DashScopeReranker) rerankQwen3(ctx context.Context, model, query string, docs []string, origDocs []RetrievalResult, topN int) ([]RetrievalResult, error) {
	reqBody := qwen3RerankRequest{
		Model:     model,
		Query:     query,
		Documents: docs,
		TopN:      topN,
	}
	if r.config.Instruct != "" {
		reqBody.Instruct = r.config.Instruct
	}

	url := r.config.BaseURL
	if url == "" {
		url = "https://dashscope.aliyuncs.com/compatible-api/v1/reranks"
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "marshal qwen3-rerank request", err)
	}

	logrus.Debugf("[Reranker] qwen3-rerank request: model=%s, docs=%d, topN=%d", model, len(docs), topN)

	respBody, err := r.doHTTP(ctx, url, bodyBytes)
	if err != nil {
		return nil, err
	}

	var resp qwen3RerankResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, NewRAGErrorf(ErrCodeRerankFailed, err,
			"unmarshal qwen3-rerank response: %s", truncateBody(respBody))
	}

	return buildRerankedResults(resp.Results, origDocs, topN, func(r qwen3RerankResult) (int, float64) {
		return r.Index, r.RelevanceScore
	})
}

// rerankGTE 使用 gte-rerank-v2 原生 API
// POST https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank
func (r *DashScopeReranker) rerankGTE(ctx context.Context, model, query string, docs []string, origDocs []RetrievalResult, topN int) ([]RetrievalResult, error) {
	reqBody := gteRerankRequest{
		Model: model,
		Input: gteRerankInput{
			Query:     query,
			Documents: docs,
		},
		Parameters: &gteRerankParam{
			TopN:            topN,
			ReturnDocuments: false,
		},
	}

	url := r.config.BaseURL
	if url == "" {
		url = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "marshal gte-rerank request", err)
	}

	logrus.Debugf("[Reranker] gte-rerank request: model=%s, docs=%d, topN=%d", model, len(docs), topN)

	respBody, err := r.doHTTP(ctx, url, bodyBytes)
	if err != nil {
		return nil, err
	}

	var resp gteRerankResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, NewRAGErrorf(ErrCodeRerankFailed, err,
			"unmarshal gte-rerank response: %s", truncateBody(respBody))
	}

	if resp.Code != "" {
		return nil, NewRAGErrorf(ErrCodeRerankFailed, nil,
			"DashScope error [%s]: %s (request_id=%s)", resp.Code, resp.Message, resp.RequestID)
	}

	return buildRerankedResults(resp.Output.Results, origDocs, topN, func(r gteRerankResult) (int, float64) {
		return r.Index, r.RelevanceScore
	})
}

// doHTTP 执行 HTTP 请求并返回响应体
func (r *DashScopeReranker) doHTTP(ctx context.Context, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "create http request", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if r.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "http call", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "read response", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, NewRAGErrorf(ErrCodeRerankFailed, nil,
			"rerank API returned %d: %s", resp.StatusCode, truncateBody(respBody))
	}

	return respBody, nil
}

// buildRerankedResults 通用结果构建：从 API 结果映射回 RetrievalResult
func buildRerankedResults[T any](apiResults []T, origDocs []RetrievalResult, topN int, extract func(T) (int, float64)) ([]RetrievalResult, error) {
	type scored struct {
		index int
		score float64
	}

	items := make([]scored, 0, len(apiResults))
	for _, r := range apiResults {
		idx, score := extract(r)
		items = append(items, scored{index: idx, score: score})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].score > items[j].score
	})

	reranked := make([]RetrievalResult, 0, topN)
	for _, item := range items {
		if item.index < 0 || item.index >= len(origDocs) {
			continue
		}
		doc := origDocs[item.index]
		doc.RelevanceScore = item.score
		reranked = append(reranked, doc)
		if len(reranked) >= topN {
			break
		}
	}

	logrus.Infof("[Reranker] Reranked %d -> %d documents", len(origDocs), len(reranked))
	return reranked, nil
}

func truncateBody(body []byte) string {
	if len(body) > 500 {
		return string(body[:500]) + "..."
	}
	return string(body)
}

// ================================================================
// Generic HTTP Reranker — Cohere / Jina 等第三方兼容
// ================================================================

type GenericHTTPReranker struct {
	config RerankConfig
	client *http.Client
}

func NewGenericHTTPReranker(config RerankConfig) *GenericHTTPReranker {
	return &GenericHTTPReranker{
		config: config,
		client: &http.Client{Timeout: config.Timeout},
	}
}

type genericRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

type genericRerankResponse struct {
	Results []genericRerankResult `json:"results"`
}

type genericRerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

func (r *GenericHTTPReranker) Rerank(ctx context.Context, query string, documents []RetrievalResult, topN int) ([]RetrievalResult, error) {
	if len(documents) == 0 {
		return documents, nil
	}

	if topN <= 0 {
		topN = r.config.TopN
	}
	if topN <= 0 {
		topN = 5
	}
	if topN > len(documents) {
		topN = len(documents)
	}

	docs := make([]string, len(documents))
	for i, d := range documents {
		docs[i] = d.Content
	}

	reqBody := genericRerankRequest{
		Model:     r.config.Model,
		Query:     query,
		Documents: docs,
		TopN:      topN,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "marshal request", err)
	}

	url := r.config.BaseURL
	if url == "" {
		switch r.config.Provider {
		case "cohere":
			url = "https://api.cohere.ai/v1/rerank"
		case "jina":
			url = "https://api.jina.ai/v1/rerank"
		default:
			return nil, NewRAGError(ErrCodeRerankFailed, "no base_url configured for provider: "+r.config.Provider, nil)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "create request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "http call", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "read response", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, NewRAGErrorf(ErrCodeRerankFailed, nil,
			"rerank API returned %d: %s", resp.StatusCode, truncateBody(respBody))
	}

	var rerankResp genericRerankResponse
	if err := json.Unmarshal(respBody, &rerankResp); err != nil {
		return nil, NewRAGError(ErrCodeRerankFailed, "unmarshal response", err)
	}

	return buildRerankedResults(rerankResp.Results, documents, topN, func(r genericRerankResult) (int, float64) {
		return r.Index, r.RelevanceScore
	})
}

// ================================================================
// Score-based Reranker (无需外部 API)
// ================================================================

type ScoreReranker struct{}

func NewScoreReranker() *ScoreReranker {
	return &ScoreReranker{}
}

func (r *ScoreReranker) Rerank(_ context.Context, _ string, documents []RetrievalResult, topN int) ([]RetrievalResult, error) {
	if len(documents) == 0 {
		return documents, nil
	}

	sorted := make([]RetrievalResult, len(documents))
	copy(sorted, documents)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].RelevanceScore > sorted[j].RelevanceScore
	})

	if topN > 0 && topN < len(sorted) {
		sorted = sorted[:topN]
	}
	return sorted, nil
}

// ================================================================
// 全局 Reranker 管理
// ================================================================

var globalReranker Reranker

// InitGlobalReranker 初始化全局 Reranker
func InitGlobalReranker(config RerankConfig) Reranker {
	if !config.Enabled {
		// 禁用时回退到纯分数排序，保证结果可用且不依赖外部 API
		logrus.Info("[Reranker] Reranker disabled, using score-based fallback")
		globalReranker = NewScoreReranker()
		return globalReranker
	}

	// Provider 选择策略：显式配置优先；未知 provider 时默认 DashScope
	switch config.Provider {
	case "dashscope":
		globalReranker = NewDashScopeReranker(config)
		logrus.Infof("[Reranker] DashScope Reranker initialized (model=%s, topN=%d, recall=%d)",
			config.Model, config.TopN, config.RecallTopK)
	case "cohere", "jina", "custom":
		globalReranker = NewGenericHTTPReranker(config)
		logrus.Infof("[Reranker] Generic HTTP Reranker initialized (provider=%s, model=%s, topN=%d)",
			config.Provider, config.Model, config.TopN)
	default:
		globalReranker = NewDashScopeReranker(config)
		logrus.Infof("[Reranker] Defaulting to DashScope Reranker (model=%s)", config.Model)
	}

	return globalReranker
}

// GetGlobalReranker 获取全局 Reranker
func GetGlobalReranker() Reranker {
	if globalReranker == nil {
		return NewScoreReranker()
	}
	return globalReranker
}

// RerankResults 使用全局 Reranker 重排序
func RerankResults(ctx context.Context, query string, results []RetrievalResult, topN int) ([]RetrievalResult, error) {
	// 统一入口：调用方只需关心结果或错误，降级策略由具体实现决定
	return GetGlobalReranker().Rerank(ctx, query, results, topN)
}

// GetEffectiveRecallTopK 返回考虑 Rerank 后的实际召回数量
func GetEffectiveRecallTopK(config RerankConfig, requestedTopK int) int {
	if !config.Enabled {
		return requestedTopK
	}
	recallK := config.RecallTopK
	if recallK <= 0 {
		recallK = requestedTopK * 4
	}
	if recallK < requestedTopK {
		recallK = requestedTopK
	}
	return recallK
}

// GetRerankModelInfo 获取模型约束信息
func GetRerankModelInfo(model string) (maxDocs int, maxTokenPerDoc int, maxRequestTokens int) {
	switch strings.ToLower(model) {
	case "qwen3-rerank":
		return 500, 4000, 0
	case "qwen3-vl-rerank":
		return 100, 8000, 120000
	case "gte-rerank-v2":
		return 30000, 0, 0
	default:
		return 500, 4000, 0
	}
}

// ValidateRerankInput 校验 Rerank 输入是否超限
func ValidateRerankInput(model string, docCount int) error {
	maxDocs, _, _ := GetRerankModelInfo(model)
	if maxDocs > 0 && docCount > maxDocs {
		return fmt.Errorf("document count %d exceeds model %s limit of %d", docCount, model, maxDocs)
	}
	return nil
}
