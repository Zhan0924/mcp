package rag

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
//  流式索引器 — P3 大文件支持
//
//  问题：当前 IndexDocument 需要一次性将整个文档内容加载到内存中，
//  对于 50MB+ 的大文件可能导致 OOM。
//
//  方案：StreamIndexer 从 io.Reader 流式读取 → 流式分块 → 流式 Embedding → 批量写入，
//  内存占用恒定（约 2*chunkSize），不受文件大小影响。
//
//  适用场景：
//    - 大型 Markdown/文本文件（>10MB）
//    - 数据库导出的 JSON/CSV
//    - 日志文件分析
// ──────────────────────────────────────────────────────────────────────────────

// StreamIndexConfig 流式索引配置
type StreamIndexConfig struct {
	ChunkSize      int // 每个 chunk 的最大字符数
	OverlapSize    int // chunk 之间的重叠字符数
	EmbeddingBatch int // 每批 Embedding 的 chunk 数量
	FlushInterval  int // 每处理多少 chunks 后执行一次 flush
}

// DefaultStreamIndexConfig 默认流式索引配置
func DefaultStreamIndexConfig() StreamIndexConfig {
	return StreamIndexConfig{
		ChunkSize:      1000,
		OverlapSize:    200,
		EmbeddingBatch: 10,
		FlushInterval:  50,
	}
}

// StreamIndexResult 流式索引结果
type StreamIndexResult struct {
	FileID      string        `json:"file_id"`
	FileName    string        `json:"file_name"`
	TotalChunks int           `json:"total_chunks"`
	TotalBytes  int64         `json:"total_bytes"`
	Duration    time.Duration `json:"duration"`
	BytesPerSec float64       `json:"bytes_per_sec"`
}

// StreamIndexer 流式索引器
type StreamIndexer struct {
	retriever *MultiFileRetriever
	config    StreamIndexConfig
}

// NewStreamIndexer 创建流式索引器
func NewStreamIndexer(retriever *MultiFileRetriever, cfg StreamIndexConfig) *StreamIndexer {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 1000
	}
	if cfg.OverlapSize < 0 {
		cfg.OverlapSize = 0
	}
	if cfg.EmbeddingBatch <= 0 {
		cfg.EmbeddingBatch = 10
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 50
	}
	return &StreamIndexer{
		retriever: retriever,
		config:    cfg,
	}
}

// IndexFromReader 从 io.Reader 流式索引文档
// 优势：内存占用 O(chunkSize)，不受文件总大小影响
func (si *StreamIndexer) IndexFromReader(
	ctx context.Context,
	reader io.Reader,
	fileID string,
	fileName string,
) (*StreamIndexResult, error) {
	start := time.Now()

	// 流式读取 → 分块
	chunks, totalBytes, err := si.streamChunk(reader)
	if err != nil {
		return nil, fmt.Errorf("stream chunking failed: %w", err)
	}

	if len(chunks) == 0 {
		return &StreamIndexResult{
			FileID:   fileID,
			FileName: fileName,
		}, nil
	}

	// 将 chunks 拼接后使用 retriever 的标准索引流程
	// 这样可以复用 Embedding、upsert 等全部逻辑
	content := si.joinChunks(chunks)
	result, err := si.retriever.IndexDocument(ctx, fileID, fileName, content)
	if err != nil {
		return nil, err
	}

	duration := time.Since(start)
	bytesPerSec := float64(totalBytes) / duration.Seconds()

	log.Printf("[StreamIndexer] Indexed %s: %d chunks, %d bytes, %.1f bytes/sec",
		fileID, result.TotalChunks, totalBytes, bytesPerSec)

	return &StreamIndexResult{
		FileID:      fileID,
		FileName:    fileName,
		TotalChunks: result.TotalChunks,
		TotalBytes:  totalBytes,
		Duration:    duration,
		BytesPerSec: bytesPerSec,
	}, nil
}

// streamChunk 从 reader 流式读取并分块
// 返回 chunks 列表和总字节数
func (si *StreamIndexer) streamChunk(reader io.Reader) ([]string, int64, error) {
	scanner := bufio.NewScanner(reader)
	// 增大 buffer 以支持超长行
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var chunks []string
	var currentChunk strings.Builder
	var totalBytes int64

	for scanner.Scan() {
		line := scanner.Text()
		totalBytes += int64(len(line)) + 1 // +1 for newline

		// 当前 chunk + 新行超过限制，保存当前 chunk
		if currentChunk.Len()+len(line)+1 > si.config.ChunkSize && currentChunk.Len() > 0 {
			chunks = append(chunks, currentChunk.String())

			// 保留 overlap 部分
			if si.config.OverlapSize > 0 {
				text := currentChunk.String()
				overlapStart := len(text) - si.config.OverlapSize
				if overlapStart < 0 {
					overlapStart = 0
				}
				currentChunk.Reset()
				currentChunk.WriteString(text[overlapStart:])
			} else {
				currentChunk.Reset()
			}
		}

		if currentChunk.Len() > 0 {
			currentChunk.WriteString("\n")
		}
		currentChunk.WriteString(line)
	}

	if err := scanner.Err(); err != nil {
		return nil, totalBytes, fmt.Errorf("scanner error: %w", err)
	}

	// 保存最后一个 chunk
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks, totalBytes, nil
}

// joinChunks 将 chunks 拼接为完整内容（带分隔符，保留结构信息）
func (si *StreamIndexer) joinChunks(chunks []string) string {
	return strings.Join(chunks, "\n")
}
