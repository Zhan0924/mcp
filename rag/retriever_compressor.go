/*
┌─────────────────────────────────────────────────────────────────────────────┐
│          context_compressor.go — 检索结果上下文压缩                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  核心思想: 检索返回的 chunk 可能包含大量与查询无关的内容，                    │
│  上下文压缩只保留与 query 直接相关的片段，减少 LLM 输入 token 消耗。          │
│                                                                             │
│  两种压缩策略:                                                               │
│    1. LLMCompressor: 使用 LLM 对每个 chunk 提取与查询相关的关键信息           │
│       - 质量最高，但有 LLM 调用开销                                          │
│    2. EmbeddingSimilarityCompressor: 将 chunk 按句子拆分，                    │
│       只保留与 query 余弦相似度高的句子                                      │
│       - 无 LLM 依赖，速度快，适合实时检索场景                                │
│                                                                             │
│  导出类型:                                                                   │
│    ContextCompressor              — 上下文压缩器接口                        │
│    LLMCompressor                  — 基于 LLM 的压缩器                       │
│    EmbeddingSimilarityCompressor  — 基于 Embedding 相似度的压缩器            │
│    CompressorConfig               — 压缩器配置                              │
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

// ContextCompressor 上下文压缩器接口
type ContextCompressor interface {
	// Compress 压缩检索结果，只保留与查询相关的内容
	Compress(ctx context.Context, query string, results []RetrievalResult) ([]RetrievalResult, error)
}

// CompressorConfig 压缩器配置
type CompressorConfig struct {
	Enabled        bool          `toml:"enabled"`
	Type           string        `toml:"type"`     // llm / embedding
	BaseURL        string        `toml:"base_url"` // LLM 类型需要
	APIKey         string        `toml:"api_key"`  // LLM 类型需要
	Model          string        `toml:"model"`    // LLM 类型需要
	Timeout        time.Duration `toml:"timeout"`
	SimilarityTopN int           `toml:"similarity_top_n"` // Embedding 类型: 每个 chunk 保留前 N 个最相关句子
	MinSimilarity  float64       `toml:"min_similarity"`   // Embedding 类型: 最低相似度阈值
}

// DefaultCompressorConfig 默认配置
func DefaultCompressorConfig() CompressorConfig {
	return CompressorConfig{
		Enabled:        false,
		Type:           "embedding",
		Timeout:        15 * time.Second,
		SimilarityTopN: 5,
		MinSimilarity:  0.3,
	}
}

// ================================================================
// LLM Compressor — 使用 LLM 提取相关信息
// ================================================================

// LLMCompressor 基于 LLM 的上下文压缩器
// 对每个 chunk 调用 LLM 提取与 query 相关的关键信息
type LLMCompressor struct {
	config     CompressorConfig
	httpClient *http.Client
}

// NewLLMCompressor 创建 LLM 压缩器
func NewLLMCompressor(cfg CompressorConfig) *LLMCompressor {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &LLMCompressor{
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

const compressionPrompt = `Given the following question and document, extract ONLY the information that is directly relevant to answering the question. Remove all irrelevant content. If no relevant information exists, respond with "NO_RELEVANT_CONTENT".

Keep the extracted content concise but complete - do not lose important details.
Respond in the same language as the document.`

// Compress 使用 LLM 并发压缩检索结果
// 所有 chunk 的压缩请求同时发起，单个失败保留原始内容，避免串行叠加的 LLM 延迟。
func (c *LLMCompressor) Compress(ctx context.Context, query string, results []RetrievalResult) ([]RetrievalResult, error) {
	if len(results) == 0 {
		return results, nil
	}

	type compressResult struct {
		index   int
		content string
		err     error
	}

	ch := make(chan compressResult, len(results))

	// ── 并发扇出: 所有 chunk 同时压缩 ──
	for i, r := range results {
		go func(idx int, content string) {
			compressed, err := c.compressChunk(ctx, query, content)
			ch <- compressResult{index: idx, content: compressed, err: err}
		}(i, r.Content)
	}

	// ── 扇入: 收集并组装 ──
	compressedContents := make([]string, len(results))
	compressedOk := make([]bool, len(results))
	for i := 0; i < len(results); i++ {
		cr := <-ch
		if cr.err != nil {
			logrus.Warnf("[LLMCompressor] Compression failed for chunk %s, keeping original: %v",
				results[cr.index].ChunkID, cr.err)
			continue
		}
		if cr.content != "NO_RELEVANT_CONTENT" && strings.TrimSpace(cr.content) != "" {
			compressedContents[cr.index] = cr.content
			compressedOk[cr.index] = true
		}
	}

	compressed := make([]RetrievalResult, 0, len(results))
	for i, r := range results {
		if compressedOk[i] {
			r.Content = compressedContents[i]
			compressed = append(compressed, r)
		} else if compressedContents[i] == "" && !compressedOk[i] {
			// 压缩失败或返回空，保留原始内容
			compressed = append(compressed, r)
		}
	}

	logrus.Infof("[LLMCompressor] Compressed %d -> %d results", len(results), len(compressed))
	return compressed, nil
}

func (c *LLMCompressor) compressChunk(ctx context.Context, query, content string) (string, error) {
	url := strings.TrimSuffix(c.config.BaseURL, "/") + "/chat/completions"

	reqBody := map[string]interface{}{
		"model": c.config.Model,
		"messages": []map[string]string{
			{"role": "system", "content": compressionPrompt},
			{"role": "user", "content": fmt.Sprintf("Question: %s\n\nDocument:\n%s", query, content)},
		},
		"temperature": 0.0,
		"max_tokens":  1024,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM API returned %d", resp.StatusCode)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", err
	}

	if len(chatResp.Choices) == 0 {
		return content, nil
	}

	return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
}

// ================================================================
// Embedding Similarity Compressor — 基于 Embedding 相似度
// ================================================================

// EmbeddingSimilarityCompressor 基于 Embedding 相似度的上下文压缩器
// 将 chunk 拆分为句子，只保留与 query 相似度高的句子
// 优势: 不需要 LLM 调用，速度快，适合实时检索场景
type EmbeddingSimilarityCompressor struct {
	config CompressorConfig
}

// NewEmbeddingSimilarityCompressor 创建 Embedding 相似度压缩器
func NewEmbeddingSimilarityCompressor(cfg CompressorConfig) *EmbeddingSimilarityCompressor {
	if cfg.SimilarityTopN <= 0 {
		cfg.SimilarityTopN = 5
	}
	if cfg.MinSimilarity <= 0 {
		cfg.MinSimilarity = 0.3
	}
	return &EmbeddingSimilarityCompressor{config: cfg}
}

// Compress 基于 Embedding 相似度压缩
func (c *EmbeddingSimilarityCompressor) Compress(ctx context.Context, query string, results []RetrievalResult) ([]RetrievalResult, error) {
	if len(results) == 0 {
		return results, nil
	}

	// 获取 query 的 embedding
	queryVecs, err := EmbedStrings(ctx, []string{query})
	if err != nil {
		logrus.Warnf("[EmbeddingCompressor] Failed to embed query, returning original results: %v", err)
		return results, nil
	}
	if len(queryVecs) == 0 {
		return results, nil
	}
	queryVec := queryVecs[0]

	compressed := make([]RetrievalResult, 0, len(results))

	for _, r := range results {
		sentences := splitIntoSentences(r.Content)
		if len(sentences) <= 2 {
			// 太短的 chunk 无需压缩
			compressed = append(compressed, r)
			continue
		}

		// 分批 embed 所有句子（DashScope API batch size 上限 10）
		const sentenceBatchSize = 10
		sentVecs := make([][]float64, 0, len(sentences))
		sentEmbedFailed := false
		for start := 0; start < len(sentences); start += sentenceBatchSize {
			end := start + sentenceBatchSize
			if end > len(sentences) {
				end = len(sentences)
			}
			batch, err := CachedEmbedStrings(ctx, sentences[start:end])
			if err != nil {
				logrus.Debugf("[EmbeddingCompressor] Sentence embedding batch [%d:%d] failed: %v", start, end, err)
				sentEmbedFailed = true
				break
			}
			sentVecs = append(sentVecs, batch...)
		}
		if sentEmbedFailed || len(sentVecs) != len(sentences) {
			logrus.Debugf("[EmbeddingCompressor] Sentence embedding failed, keeping original: %v", err)
			compressed = append(compressed, r)
			continue
		}

		// 计算每个句子与 query 的相似度
		type scoredSentence struct {
			index      int
			sentence   string
			similarity float64
		}

		scored := make([]scoredSentence, len(sentences))
		for i, sv := range sentVecs {
			scored[i] = scoredSentence{
				index:      i,
				sentence:   sentences[i],
				similarity: cosineSimilarity(queryVec, sv),
			}
		}

		// 按相似度降序排列
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].similarity > scored[j].similarity
		})

		// 保留 TopN 个最相关的句子（高于阈值）
		var relevantSentences []scoredSentence
		for _, s := range scored {
			if s.similarity < c.config.MinSimilarity {
				break
			}
			relevantSentences = append(relevantSentences, s)
			if len(relevantSentences) >= c.config.SimilarityTopN {
				break
			}
		}

		if len(relevantSentences) == 0 {
			continue
		}

		// 按原始顺序重新排列
		sort.Slice(relevantSentences, func(i, j int) bool {
			return relevantSentences[i].index < relevantSentences[j].index
		})

		var sb strings.Builder
		for i, s := range relevantSentences {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(s.sentence)
		}

		r.Content = sb.String()
		compressed = append(compressed, r)
	}

	logrus.Infof("[EmbeddingCompressor] Compressed %d -> %d results", len(results), len(compressed))
	return compressed, nil
}

// ================================================================
// 全局压缩器管理
// ================================================================

var globalCompressor ContextCompressor

// InitGlobalCompressor 初始化全局压缩器
func InitGlobalCompressor(cfg CompressorConfig) ContextCompressor {
	if !cfg.Enabled {
		logrus.Info("[Compressor] Context compression disabled")
		globalCompressor = nil
		return nil
	}

	switch cfg.Type {
	case "llm":
		globalCompressor = NewLLMCompressor(cfg)
		logrus.Infof("[Compressor] LLM compressor initialized (model=%s)", cfg.Model)
	case "embedding":
		globalCompressor = NewEmbeddingSimilarityCompressor(cfg)
		logrus.Infof("[Compressor] Embedding similarity compressor initialized (topN=%d, minSim=%.2f)",
			cfg.SimilarityTopN, cfg.MinSimilarity)
	default:
		globalCompressor = NewEmbeddingSimilarityCompressor(cfg)
		logrus.Infof("[Compressor] Defaulting to embedding similarity compressor")
	}

	return globalCompressor
}

// GetGlobalCompressor 获取全局压缩器
func GetGlobalCompressor() ContextCompressor {
	return globalCompressor
}

// CompressResults 使用全局压缩器压缩检索结果
func CompressResults(ctx context.Context, query string, results []RetrievalResult) ([]RetrievalResult, error) {
	compressor := GetGlobalCompressor()
	if compressor == nil {
		return results, nil
	}
	return compressor.Compress(ctx, query, results)
}
