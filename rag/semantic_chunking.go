/*
┌─────────────────────────────────────────────────────────────────────────────┐
│             semantic_chunking.go — 基于语义相似度的动态分块                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  核心思想: 将文本分成句子，计算相邻句子间的 embedding 余弦相似度，            │
│  在相似度骤降处（语义转折点）切分，使每个 chunk 内部语义高度一致。            │
│                                                                             │
│  算法流程:                                                                   │
│    1. 句子分割: 按中英文句号/问号/感叹号分句                                 │
│    2. 滑动窗口 Embedding: 每次取 window_size 个句子拼接后做 embedding         │
│       （单句太短语义稀疏，窗口聚合后信号更强）                               │
│    3. 计算相邻窗口的余弦相似度                                               │
│    4. 断点检测: 相似度低于 mean - k*std 的位置标记为断点                      │
│    5. 按断点切分: 断点之间的句子合并为一个 chunk                             │
│    6. 大 chunk 二次切割 + 小 chunk 合并                                      │
│                                                                             │
│  与固定窗口分块的对比:                                                       │
│    固定窗口: 简单高效，但可能在话题中间切断                                   │
│    语义分块: 保持语义完整性，但需额外 embedding 开销（索引时一次性付出）       │
│                                                                             │
│  导出函数:                                                                   │
│    SemanticChunking(content, config, embedFn) []Chunk                       │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// SemanticChunkingConfig 语义分块配置
type SemanticChunkingConfig struct {
	Enabled             bool    `toml:"enabled"`
	WindowSize          int     `toml:"window_size"`          // 滑动窗口大小（句子数），默认 3
	BreakpointThreshold float64 `toml:"breakpoint_threshold"` // 断点阈值 (标准差倍数)，默认 1.0
	MaxChunkSize        int     `toml:"max_chunk_size"`       // 最大 chunk 大小（字符），超过则二次切割
	MinChunkSize        int     `toml:"min_chunk_size"`       // 最小 chunk 大小（字符），低于则与邻居合并
}

// DefaultSemanticChunkingConfig 默认语义分块配置
func DefaultSemanticChunkingConfig() SemanticChunkingConfig {
	return SemanticChunkingConfig{
		Enabled:             false,
		WindowSize:          3,
		BreakpointThreshold: 1.0,
		MaxChunkSize:        1500,
		MinChunkSize:        100,
	}
}

// EmbedFunc Embedding 回调函数类型
// 接受文本列表，返回对应的向量列表
type EmbedFunc func(ctx context.Context, texts []string) ([][]float64, error)

// SemanticChunking 基于语义相似度的动态分块
// embedFn 用于计算句子窗口的 embedding，由调用方注入（可走缓存或直连）
func SemanticChunking(ctx context.Context, content string, cfg SemanticChunkingConfig, embedFn EmbedFunc) []Chunk {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 3
	}
	if cfg.BreakpointThreshold <= 0 {
		cfg.BreakpointThreshold = 1.0
	}
	if cfg.MaxChunkSize <= 0 {
		cfg.MaxChunkSize = 1500
	}
	if cfg.MinChunkSize <= 0 {
		cfg.MinChunkSize = 100
	}

	// Step 1: 句子分割
	sentences := splitIntoSentences(content)
	if len(sentences) <= 1 {
		return []Chunk{{
			ChunkID:    uuid.New().String(),
			Content:    content,
			ChunkIndex: 0,
			StartPos:   0,
			EndPos:     utf8.RuneCountInString(content),
			TokenCount: estimateTokenCount(content),
		}}
	}

	logrus.Infof("[SemanticChunking] Split into %d sentences", len(sentences))

	// Step 2: 构造滑动窗口文本
	windows := buildSentenceWindows(sentences, cfg.WindowSize)

	// Step 3: 分批计算窗口 embedding
	// DashScope API 单次最多处理 10 条文本，超过会返回 400。
	// 在语义分块内部就地分批，避免上层调用方感知批次限制。
	const embedBatchSize = 10
	embeddings := make([][]float64, 0, len(windows))
	for start := 0; start < len(windows); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(windows) {
			end = len(windows)
		}
		batch, err := embedFn(ctx, windows[start:end])
		if err != nil {
			logrus.Warnf("[SemanticChunking] Embedding batch [%d:%d] failed, falling back: %v", start, end, err)
			return ChunkDocument(content, ChunkingConfig{
				MaxChunkSize: cfg.MaxChunkSize,
				MinChunkSize: cfg.MinChunkSize,
				OverlapSize:  200,
			})
		}
		embeddings = append(embeddings, batch...)
	}

	if len(embeddings) < 2 {
		return []Chunk{{
			ChunkID:    uuid.New().String(),
			Content:    content,
			ChunkIndex: 0,
			StartPos:   0,
			EndPos:     utf8.RuneCountInString(content),
			TokenCount: estimateTokenCount(content),
		}}
	}

	// Step 4: 计算相邻窗口的余弦相似度
	similarities := make([]float64, len(embeddings)-1)
	for i := 0; i < len(embeddings)-1; i++ {
		similarities[i] = cosineSimilarity(embeddings[i], embeddings[i+1])
	}

	// Step 5: 检测断点（相似度骤降处）
	breakpoints := detectBreakpoints(similarities, cfg.BreakpointThreshold)

	logrus.Infof("[SemanticChunking] Detected %d breakpoints from %d similarity scores",
		len(breakpoints), len(similarities))

	// Step 6: 按断点切分句子为 chunk
	chunks := splitByBreakpoints(sentences, breakpoints, cfg)

	return chunks
}

// splitIntoSentences 将文本分割为句子
// 支持中英文标点，保留句子结尾标点
func splitIntoSentences(text string) []string {
	var sentences []string
	var current strings.Builder
	runes := []rune(text)

	for i := 0; i < len(runes); i++ {
		current.WriteRune(runes[i])

		// 检查是否为句子结束标记
		isSentenceEnd := false
		switch runes[i] {
		case '.', '!', '?', '。', '！', '？', '；':
			isSentenceEnd = true
		case '\n':
			// 连续换行也视为句子分隔
			if i+1 < len(runes) && runes[i+1] == '\n' {
				isSentenceEnd = true
			}
		}

		if isSentenceEnd {
			s := strings.TrimSpace(current.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			current.Reset()
		}
	}

	// 处理尾部残余
	if s := strings.TrimSpace(current.String()); s != "" {
		sentences = append(sentences, s)
	}

	return sentences
}

// buildSentenceWindows 构造滑动窗口文本
// 每个窗口由 windowSize 个连续句子拼接而成，相邻窗口滑动 1 个句子
func buildSentenceWindows(sentences []string, windowSize int) []string {
	if windowSize >= len(sentences) {
		return []string{strings.Join(sentences, " ")}
	}

	windows := make([]string, len(sentences)-windowSize+1)
	for i := 0; i <= len(sentences)-windowSize; i++ {
		windows[i] = strings.Join(sentences[i:i+windowSize], " ")
	}
	return windows
}

// cosineSimilarity 计算两个向量的余弦相似度
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	normA = math.Sqrt(normA)
	normB = math.Sqrt(normB)

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (normA * normB)
}

// detectBreakpoints 使用均值-标准差方法检测语义断点
// 断点定义: similarity[i] < mean - k * std （相似度显著低于平均水平的位置）
func detectBreakpoints(similarities []float64, kStd float64) []int {
	if len(similarities) == 0 {
		return nil
	}

	// 计算均值
	sum := 0.0
	for _, s := range similarities {
		sum += s
	}
	mean := sum / float64(len(similarities))

	// 计算标准差
	varSum := 0.0
	for _, s := range similarities {
		diff := s - mean
		varSum += diff * diff
	}
	std := math.Sqrt(varSum / float64(len(similarities)))

	// 阈值: 低于此值的位置为断点
	threshold := mean - kStd*std

	var breakpoints []int
	for i, s := range similarities {
		if s < threshold {
			breakpoints = append(breakpoints, i)
		}
	}

	return breakpoints
}

// splitByBreakpoints 按断点将句子分组为 chunk
func splitByBreakpoints(sentences []string, breakpoints []int, cfg SemanticChunkingConfig) []Chunk {
	// 构造断点集合（断点索引表示在 sentences[i] 和 sentences[i+1] 之间切分）
	bpSet := make(map[int]bool)
	for _, bp := range breakpoints {
		bpSet[bp] = true
	}

	// 按断点分组
	var groups [][]string
	var currentGroup []string

	for i, sentence := range sentences {
		currentGroup = append(currentGroup, sentence)

		if bpSet[i] || i == len(sentences)-1 {
			groups = append(groups, currentGroup)
			currentGroup = nil
		}
	}

	// 合并过小的组 + 切割过大的组
	var chunks []Chunk
	chunkIndex := 0
	currentPos := 0

	for _, group := range groups {
		text := strings.Join(group, " ")
		runeCount := utf8.RuneCountInString(text)

		if runeCount > cfg.MaxChunkSize {
			// 过大的组进行二次固定窗口切割
			subChunks := ChunkDocument(text, ChunkingConfig{
				MaxChunkSize: cfg.MaxChunkSize,
				MinChunkSize: cfg.MinChunkSize,
				OverlapSize:  100,
			})
			for _, sc := range subChunks {
				sc.ChunkIndex = chunkIndex
				sc.StartPos = currentPos + sc.StartPos
				sc.EndPos = currentPos + sc.EndPos
				chunks = append(chunks, sc)
				chunkIndex++
			}
		} else if runeCount < cfg.MinChunkSize && len(chunks) > 0 {
			// 过小的组合并到前一个 chunk
			lastIdx := len(chunks) - 1
			merged := chunks[lastIdx].Content + "\n" + text
			if utf8.RuneCountInString(merged) <= cfg.MaxChunkSize {
				chunks[lastIdx].Content = merged
				chunks[lastIdx].EndPos = currentPos + len(text)
				chunks[lastIdx].TokenCount = estimateTokenCount(merged)
			} else {
				chunks = append(chunks, Chunk{
					ChunkID:    uuid.New().String(),
					Content:    text,
					ChunkIndex: chunkIndex,
					StartPos:   currentPos,
					EndPos:     currentPos + len(text),
					TokenCount: estimateTokenCount(text),
				})
				chunkIndex++
			}
		} else {
			chunks = append(chunks, Chunk{
				ChunkID:    uuid.New().String(),
				Content:    text,
				ChunkIndex: chunkIndex,
				StartPos:   currentPos,
				EndPos:     currentPos + len(text),
				TokenCount: estimateTokenCount(text),
			})
			chunkIndex++
		}
		currentPos += len(text) + 1
	}

	logrus.Infof("[SemanticChunking] Produced %d chunks from %d sentence groups", len(chunks), len(groups))
	return chunks
}
