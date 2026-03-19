/*
┌─────────────────────────────────────────────────────────────────────────────┐
│             multi_query.go — 多查询检索 (Multi-Query Retrieval)              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  核心思想: 将用户的单一查询扩展为多个语义变体，分别检索后合并去重，           │
│  显著提升召回率。                                                            │
│                                                                             │
│  工作流程:                                                                   │
│    1. 用 LLM 生成 N 个查询变体（同义改写、角度转换、粒度缩放）              │
│    2. 每个变体独立执行向量检索                                               │
│    3. RRF 融合去重，保留 TopK 个最终结果                                    │
│                                                                             │
│  示例:                                                                       │
│    原始查询: "如何部署 Kubernetes"                                           │
│    变体1: "K8s 集群搭建步骤和最佳实践"                                      │
│    变体2: "容器编排平台部署指南"                                             │
│    变体3: "Kubernetes 安装配置教程"                                          │
│                                                                             │
│  导出类型:                                                                   │
│    MultiQueryConfig     — 多查询检索配置                                    │
│    MultiQueryRetriever  — 多查询检索器                                      │
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
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// MultiQueryConfig 多查询检索配置
type MultiQueryConfig struct {
	Enabled     bool          `toml:"enabled"`
	BaseURL     string        `toml:"base_url"`
	APIKey      string        `toml:"api_key"`
	Model       string        `toml:"model"`
	NumVariants int           `toml:"num_variants"` // 生成的查询变体数量
	Timeout     time.Duration `toml:"timeout"`
}

// DefaultMultiQueryConfig 默认配置
func DefaultMultiQueryConfig() MultiQueryConfig {
	return MultiQueryConfig{
		Enabled:     false,
		Model:       "qwen-turbo",
		NumVariants: 3,
		Timeout:     15 * time.Second,
	}
}

// MultiQueryRetriever 多查询检索器
// 将单一查询扩展为多个变体，分别检索后 RRF 融合
type MultiQueryRetriever struct {
	config     MultiQueryConfig
	httpClient *http.Client
}

// NewMultiQueryRetriever 创建多查询检索器
func NewMultiQueryRetriever(cfg MultiQueryConfig) *MultiQueryRetriever {
	if cfg.NumVariants <= 0 {
		cfg.NumVariants = 3
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &MultiQueryRetriever{
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

// queryGenerationPrompt 查询变体生成 Prompt
const queryGenerationPrompt = `You are a search query optimization expert. Given a user query, generate %d alternative search queries that capture different aspects or phrasings of the same information need.

Rules:
1. Each variant should use different words/phrases but seek the same information
2. Include both broader and narrower perspectives
3. Use different technical terms or synonyms where applicable
4. Output ONLY the queries, one per line, no numbering or prefixes
5. Keep each query concise (under 50 words)
6. Respond in the same language as the input query`

// GenerateQueryVariants 使用 LLM 生成查询变体
func (r *MultiQueryRetriever) GenerateQueryVariants(ctx context.Context, query string) ([]string, error) {
	url := strings.TrimSuffix(r.config.BaseURL, "/") + "/chat/completions"

	systemPrompt := fmt.Sprintf(queryGenerationPrompt, r.config.NumVariants)

	reqBody := map[string]interface{}{
		"model": r.config.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": query},
		},
		"temperature": 0.7, // 适度多样性
		"max_tokens":  512,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API returned %d: %s", resp.StatusCode, truncateStr(string(respBody), 300))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("empty LLM response")
	}

	// 解析生成的变体（每行一个）
	content := chatResp.Choices[0].Message.Content
	lines := strings.Split(content, "\n")
	var variants []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 去掉可能的序号前缀: "1. ", "- ", "1) " 等
		line = stripNumberPrefix(line)
		if line != "" && line != query {
			variants = append(variants, line)
		}
	}

	// 限制到 NumVariants 个
	if len(variants) > r.config.NumVariants {
		variants = variants[:r.config.NumVariants]
	}

	logrus.Infof("[MultiQuery] Generated %d query variants for: %s", len(variants), query)
	return variants, nil
}

// MultiQueryRetrieve 多查询并发检索
// 对原始查询和每个变体并发执行检索，RRF 融合去重后返回 topK 结果。
// 并发策略：所有查询同时发起，通过 goroutine + channel 收集结果，
// 单路失败不影响其他路（部分成功语义），避免串行叠加 LLM 延迟。
func MultiQueryRetrieve(
	ctx context.Context,
	retriever *MultiFileRetriever,
	originalQuery string,
	variants []string,
	fileIDs []string,
	topK int,
) ([]RetrievalResult, error) {
	allQueries := append([]string{originalQuery}, variants...)

	type queryResult struct {
		queryIdx int
		results  []RetrievalResult
		err      error
	}

	resultCh := make(chan queryResult, len(allQueries))

	// ── 并发扇出: 所有查询同时执行 ──
	for qi, query := range allQueries {
		go func(idx int, q string) {
			results, err := retriever.Retrieve(ctx, q, fileIDs)
			resultCh <- queryResult{queryIdx: idx, results: results, err: err}
		}(qi, query)
	}

	// ── 扇入: 收集所有结果 ──
	type rankedResult struct {
		result RetrievalResult
		ranks  []int
	}
	resultMap := make(map[string]*rankedResult)
	successCount := 0

	for i := 0; i < len(allQueries); i++ {
		qr := <-resultCh
		if qr.err != nil {
			logrus.Warnf("[MultiQuery] Query %d/%d failed: %v", qr.queryIdx+1, len(allQueries), qr.err)
			continue
		}
		successCount++
		for rank, r := range qr.results {
			key := r.FileID + ":" + r.ChunkID
			if existing, ok := resultMap[key]; ok {
				existing.ranks = append(existing.ranks, rank+1)
			} else {
				resultMap[key] = &rankedResult{result: r, ranks: []int{rank + 1}}
			}
		}
	}

	if successCount == 0 {
		return nil, fmt.Errorf("all %d multi-query variants failed", len(allQueries))
	}

	// ── RRF 融合排序 ──
	const rrfK = 60.0
	type scoredResult struct {
		result RetrievalResult
		score  float64
	}

	scored := make([]scoredResult, 0, len(resultMap))
	for _, rr := range resultMap {
		var totalScore float64
		for _, rank := range rr.ranks {
			totalScore += 1.0 / (rrfK + float64(rank))
		}
		if len(rr.ranks) > 1 {
			totalScore *= 1.0 + 0.1*float64(len(rr.ranks)-1)
		}
		scored = append(scored, scoredResult{result: rr.result, score: totalScore})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	results := make([]RetrievalResult, 0, topK)
	for i, s := range scored {
		if i >= topK {
			break
		}
		s.result.RelevanceScore = s.score
		results = append(results, s.result)
	}

	logrus.Infof("[MultiQuery] Fused %d unique results from %d queries (%d succeeded), returning top %d",
		len(resultMap), len(allQueries), successCount, len(results))
	return results, nil
}

// stripNumberPrefix 去掉常见的序号前缀
func stripNumberPrefix(s string) string {
	// "1. xxx" → "xxx"
	// "1) xxx" → "xxx"
	// "- xxx" → "xxx"
	// "* xxx" → "xxx"
	if len(s) < 2 {
		return s
	}
	for i := 0; i < len(s) && i < 5; i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			continue
		}
		if (c == '.' || c == ')' || c == ':') && i > 0 {
			rest := strings.TrimSpace(s[i+1:])
			if rest != "" {
				return rest
			}
		}
		break
	}
	if s[0] == '-' || s[0] == '*' {
		rest := strings.TrimSpace(s[1:])
		if rest != "" {
			return rest
		}
	}
	return s
}
