/*
┌──────────────────────────────────────────────────────────────────────────────┐
│ rag_tools.go — MCP 工具实现 (RAG Tool Provider)                               │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标: 将 rag 包能力以 MCP Tool 形式暴露给客户端                               │
│                                                                              │
│ 结构:                                                                       │
│  - RAGToolProvider: 统一持有 store / config / queue                          │
│  - toolError/toolErrorSimple: MCP 统一错误输出格式                            │
│  - 8 个工具:                                                                │
│      rag_search / rag_index_document / rag_build_prompt / rag_chunk_text      │
│      rag_status / rag_delete_document / rag_parse_document / rag_task_status  │
│                                                                              │
│ 设计原则:                                                                    │
│  - 工具参数做强校验，避免错误请求进入底层逻辑                                │
│  - 异步索引可选：当 TaskQueue 为 nil 时不暴露 rag_task_status                │
│  - 结果统一序列化为 JSON 文本，便于 MCP 客户端处理                            │
└──────────────────────────────────────────────────────────────────────────────┘
*/
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mcp_rag_server/rag"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sirupsen/logrus"
)

const (
	defaultToolTimeout = 60 * time.Second
	maxTopKHardLimit   = 100
)

// RAGToolProvider RAG 工具提供者
type RAGToolProvider struct {
	store          rag.VectorStore
	retCfg         *rag.RetrieverConfig
	chunkCfg       *rag.ChunkingConfig
	rerankCfg      rag.RerankConfig
	maxContentSize int
	taskQueue      *rag.TaskQueue // nil 表示未启用异步索引
}

// NewRAGToolProvider 创建 RAG 工具提供者
func NewRAGToolProvider(store rag.VectorStore, retCfg *rag.RetrieverConfig, chunkCfg *rag.ChunkingConfig, rerankCfg rag.RerankConfig, maxContentSize int, taskQueue *rag.TaskQueue) *RAGToolProvider {
	if maxContentSize <= 0 {
		maxContentSize = 10 * 1024 * 1024
	}
	return &RAGToolProvider{
		store:          store,
		retCfg:         retCfg,
		chunkCfg:       chunkCfg,
		rerankCfg:      rerankCfg,
		maxContentSize: maxContentSize,
		taskQueue:      taskQueue,
	}
}

func toolError(code rag.ErrorCode, detail string) (*mcp.CallToolResult, error) {
	msg := fmt.Sprintf("[%s] %s: %s", code, rag.ErrorCodeMessage(code), detail)
	return mcp.NewToolResultError(msg), nil
}

func toolErrorSimple(msg string) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(msg), nil
}

func (p *RAGToolProvider) GetTools() []Tool {
	tools := []Tool{
		p.searchTool(),
		p.indexDocumentTool(),
		p.buildPromptTool(),
		p.chunkTextTool(),
		p.statusTool(),
		p.deleteDocumentTool(),
		p.parseDocumentTool(),
	}
	// 异步索引未启用时，不暴露 task_status，避免客户端误调用
	if p.taskQueue != nil {
		tools = append(tools, p.taskStatusTool())
	}
	return tools
}

func (p *RAGToolProvider) createRetriever(ctx context.Context, userID uint) (*rag.MultiFileRetriever, error) {
	return rag.NewMultiFileRetriever(ctx, p.store, nil, p.retCfg, p.chunkCfg, userID)
}

// --- Tool 1: rag_search ---

func (p *RAGToolProvider) searchTool() Tool {
	definition := mcp.NewTool(
		"rag_search",
		mcp.WithDescription("向量语义检索：在用户的 RAG 知识库中搜索与查询最相关的文档片段。支持向量检索和混合检索（BM25+向量），可选 Rerank 重排序。"),
		mcp.WithString("query", mcp.Description("搜索查询文本"), mcp.Required()),
		mcp.WithNumber("user_id", mcp.Description("用户 ID（确定 Redis 索引）"), mcp.Required()),
		mcp.WithNumber("top_k", mcp.Description("返回结果数量（默认 5）")),
		mcp.WithString("file_ids", mcp.Description("限定文件 ID 列表（逗号分隔，可选）")),
		mcp.WithNumber("min_score", mcp.Description("最低相关度阈值 0~1（可选）")),
		mcp.WithBoolean("rerank", mcp.Description("是否对结果进行 Rerank 重排序（默认 false）")),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
		defer cancel()

		args := request.GetArguments()

		query, _ := args["query"].(string)
		if query == "" {
			return toolError(rag.ErrCodeInvalidInput, "query is required")
		}

		userIDFloat, _ := args["user_id"].(float64)
		if userIDFloat <= 0 {
			return toolError(rag.ErrCodeInvalidInput, "user_id is required and must be positive")
		}
		userID := uint(userIDFloat)

		retriever, err := p.createRetriever(ctx, userID)
		if err != nil {
			logrus.Errorf("[rag_search] Failed to create retriever: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "failed to create retriever")
		}

		topK := p.retCfg.DefaultTopK
		if tk, ok := args["top_k"].(float64); ok {
			topK = int(tk)
			if topK < 1 {
				topK = 1
			}
			if topK > maxTopKHardLimit {
				topK = maxTopKHardLimit
			}
		}

		enableRerank, _ := args["rerank"].(bool)
		if enableRerank {
			effectiveK := rag.GetEffectiveRecallTopK(p.rerankCfg, topK)
			logrus.Infof("[rag_search] Rerank enabled: recall topK expanded %d -> %d", topK, effectiveK)
			retriever.SetTopK(effectiveK)
		} else {
			retriever.SetTopK(topK)
		}

		var fileIDs []string
		if fileIDsStr, ok := args["file_ids"].(string); ok && fileIDsStr != "" {
			for _, id := range strings.Split(fileIDsStr, ",") {
				id = strings.TrimSpace(id)
				if id != "" {
					fileIDs = append(fileIDs, id)
				}
			}
		}

		results, err := retriever.Retrieve(ctx, query, fileIDs)
		if err != nil {
			logrus.Errorf("[rag_search] Retrieval failed: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "retrieval failed")
		}

		if minScore, ok := args["min_score"].(float64); ok && minScore > 0 {
			var filtered []rag.RetrievalResult
			for _, r := range results {
				if r.RelevanceScore >= minScore {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}

		if enableRerank && len(results) > 0 {
			reranked, err := rag.RerankResults(ctx, query, results, topK)
			if err != nil {
				logrus.Warnf("[rag_search] Rerank failed, using original: %v", err)
			} else {
				results = reranked
			}
		}

		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return toolError(rag.ErrCodeSearchFailed, "failed to serialize results")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- Tool 2: rag_index_document ---

func (p *RAGToolProvider) indexDocumentTool() Tool {
	toolOpts := []mcp.ToolOption{
		mcp.WithDescription("文档索引：将文档分块、向量化并存入向量索引。支持纯文本、Markdown、HTML、PDF 格式，自动识别表格和图片，结构感知分块。PDF 内容需 base64 编码传入。设置 async=true 可异步执行，立即返回 task_id。"),
		mcp.WithString("content", mcp.Description("文档内容（PDF 为 base64 编码）"), mcp.Required()),
		mcp.WithString("file_id", mcp.Description("文件唯一标识"), mcp.Required()),
		mcp.WithNumber("user_id", mcp.Description("用户 ID"), mcp.Required()),
		mcp.WithString("file_name", mcp.Description("文件名（可选）")),
		mcp.WithString("format", mcp.Description("文档格式: text/markdown/html/pdf（可选，默认自动检测）")),
	}
	if p.taskQueue != nil {
		toolOpts = append(toolOpts,
			mcp.WithBoolean("async", mcp.Description("是否异步索引（默认 false）。启用后立即返回 task_id，可通过 rag_task_status 查询进度。")),
		)
	}
	definition := mcp.NewTool("rag_index_document", toolOpts...)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
		defer cancel()

		args := request.GetArguments()

		content, _ := args["content"].(string)
		if content == "" {
			return toolError(rag.ErrCodeInvalidInput, "content is required")
		}
		if len(content) > p.maxContentSize {
			return toolError(rag.ErrCodeContentTooLarge,
				fmt.Sprintf("%d bytes (max %d)", len(content), p.maxContentSize))
		}

		fileID, _ := args["file_id"].(string)
		if fileID == "" {
			return toolError(rag.ErrCodeInvalidInput, "file_id is required")
		}

		userIDFloat, _ := args["user_id"].(float64)
		if userIDFloat <= 0 {
			return toolError(rag.ErrCodeInvalidInput, "user_id is required and must be positive")
		}
		userID := uint(userIDFloat)

		fileName, _ := args["file_name"].(string)
		if fileName == "" {
			fileName = fileID
		}

		format, _ := args["format"].(string)

		// 异步模式：提交到 TaskQueue 立即返回（不阻塞索引过程）
		asyncMode, _ := args["async"].(bool)
		if asyncMode && p.taskQueue != nil {
			taskID, err := p.taskQueue.Submit(ctx, userID, fileID, fileName, content, format)
			if err != nil {
				logrus.Errorf("[rag_index_document] Async submit failed: %v", err)
				return toolError(rag.ErrCodeBatchFailed, "async submit failed: "+err.Error())
			}
			result := map[string]interface{}{
				"task_id": taskID,
				"status":  rag.TaskStatusPending,
				"message": "Document indexing submitted. Use rag_task_status to check progress.",
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		}

		// 同步模式：保持原有行为，调用方同步等待索引完成
		docFormat := rag.DocumentFormat(format)
		if docFormat == "" && isPDFContent(content) {
			docFormat = rag.FormatPDF
		}

		if docFormat != "" {
			doc, err := rag.ParseDocument(content, docFormat)
			if err != nil {
				logrus.Warnf("[rag_index_document] Parse %s failed: %v, using raw content", docFormat, err)
			} else {
				logrus.Infof("[rag_index_document] Parsed %s: tables=%d, images=%d, chars=%d",
					doc.Format, doc.Metadata.TableCount, doc.Metadata.ImageCount, doc.Metadata.CharCount)
				content = doc.Content
			}
		}

		retriever, err := p.createRetriever(ctx, userID)
		if err != nil {
			logrus.Errorf("[rag_index_document] Failed to create retriever: %v", err)
			return toolError(rag.ErrCodeIndexCreateFailed, "failed to create retriever")
		}

		result, err := retriever.IndexDocument(ctx, fileID, fileName, content)
		if err != nil {
			logrus.Errorf("[rag_index_document] Indexing failed: %v", err)
			return toolError(rag.ErrCodeBatchFailed, "indexing failed")
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return toolError(rag.ErrCodeBatchFailed, "failed to serialize result")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// isPDFContent 检测内容是否为 base64 编码的 PDF
func isPDFContent(content string) bool {
	if strings.HasPrefix(content, "base64:") {
		return true
	}
	if strings.HasPrefix(content, "data:application/pdf") {
		return true
	}
	// 尝试检测 base64 编码的 %PDF- 头 (JVBER)
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "JVBER")
}

// --- Tool 3: rag_build_prompt ---

func (p *RAGToolProvider) buildPromptTool() Tool {
	definition := mcp.NewTool(
		"rag_build_prompt",
		mcp.WithDescription("构建 RAG 提示词：自动检索相关文档，按文件分组构建包含上下文的提示词，可直接用于大模型输入。"),
		mcp.WithString("query", mcp.Description("用户问题"), mcp.Required()),
		mcp.WithNumber("user_id", mcp.Description("用户 ID"), mcp.Required()),
		mcp.WithNumber("top_k", mcp.Description("上下文数量（默认 5）")),
		mcp.WithString("file_ids", mcp.Description("限定文件 ID 列表（逗号分隔，可选）")),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
		defer cancel()

		args := request.GetArguments()

		query, _ := args["query"].(string)
		if query == "" {
			return toolError(rag.ErrCodeInvalidInput, "query is required")
		}

		userIDFloat, _ := args["user_id"].(float64)
		if userIDFloat <= 0 {
			return toolError(rag.ErrCodeInvalidInput, "user_id is required and must be positive")
		}
		userID := uint(userIDFloat)

		retriever, err := p.createRetriever(ctx, userID)
		if err != nil {
			logrus.Errorf("[rag_build_prompt] Failed to create retriever: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "failed to create retriever")
		}

		if topK, ok := args["top_k"].(float64); ok {
			k := int(topK)
			if k < 1 {
				k = 1
			}
			if k > maxTopKHardLimit {
				k = maxTopKHardLimit
			}
			retriever.SetTopK(k)
		}

		var fileIDs []string
		if fileIDsStr, ok := args["file_ids"].(string); ok && fileIDsStr != "" {
			for _, id := range strings.Split(fileIDsStr, ",") {
				id = strings.TrimSpace(id)
				if id != "" {
					fileIDs = append(fileIDs, id)
				}
			}
		}

		results, err := retriever.Retrieve(ctx, query, fileIDs)
		if err != nil {
			logrus.Errorf("[rag_build_prompt] Retrieval failed: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "retrieval failed")
		}

		prompt := rag.BuildMultiFileRAGPrompt(query, results)
		return mcp.NewToolResultText(prompt), nil
	})
}

// --- Tool 4: rag_chunk_text ---

func (p *RAGToolProvider) chunkTextTool() Tool {
	definition := mcp.NewTool(
		"rag_chunk_text",
		mcp.WithDescription("文档分块：将文本内容分割为语义完整的块。支持结构感知分块（Markdown 按章节分割）。"),
		mcp.WithString("content", mcp.Description("文档内容"), mcp.Required()),
		mcp.WithNumber("max_chunk_size", mcp.Description("最大分块大小（字符，默认 1000）")),
		mcp.WithNumber("min_chunk_size", mcp.Description("最小分块大小（字符，默认 100）")),
		mcp.WithNumber("overlap_size", mcp.Description("重叠大小（字符，默认 200）")),
		mcp.WithBoolean("structure_aware", mcp.Description("是否启用结构感知分块（默认 true）")),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		content, _ := args["content"].(string)
		if content == "" {
			return toolError(rag.ErrCodeInvalidInput, "content is required")
		}
		if len(content) > p.maxContentSize {
			return toolError(rag.ErrCodeContentTooLarge,
				fmt.Sprintf("%d bytes (max %d)", len(content), p.maxContentSize))
		}

		cfg := rag.ChunkingConfig{}
		if p.chunkCfg != nil {
			cfg = *p.chunkCfg
		}

		if v, ok := args["max_chunk_size"].(float64); ok && v > 0 {
			cfg.MaxChunkSize = int(v)
		}
		if v, ok := args["min_chunk_size"].(float64); ok && v > 0 {
			cfg.MinChunkSize = int(v)
		}
		if v, ok := args["overlap_size"].(float64); ok && v > 0 {
			cfg.OverlapSize = int(v)
		}
		if v, ok := args["structure_aware"].(bool); ok {
			cfg.StructureAware = v
		}

		var chunks []rag.Chunk
		if cfg.StructureAware {
			doc, err := rag.ParseDocument(content, "")
			if err == nil && doc.Format == rag.FormatMarkdown && len(doc.Sections) > 0 {
				chunks = rag.StructureAwareChunk(doc, cfg)
			}
		}
		if len(chunks) == 0 {
			chunks = rag.ChunkDocument(content, cfg)
		}

		logrus.Infof("[rag_chunk_text] Done: content_len=%d, chunks=%d, structure_aware=%v", len(content), len(chunks), cfg.StructureAware)

		type chunkOutput struct {
			ChunkIndex int    `json:"chunk_index"`
			Content    string `json:"content"`
			TokenCount int    `json:"token_count"`
			StartPos   int    `json:"start_pos"`
			EndPos     int    `json:"end_pos"`
		}

		output := make([]chunkOutput, len(chunks))
		for i, c := range chunks {
			output[i] = chunkOutput{
				ChunkIndex: c.ChunkIndex,
				Content:    c.Content,
				TokenCount: c.TokenCount,
				StartPos:   c.StartPos,
				EndPos:     c.EndPos,
			}
		}

		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return toolError(rag.ErrCodeParseFailed, "failed to serialize results")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- Tool 5: rag_status ---

func (p *RAGToolProvider) statusTool() Tool {
	definition := mcp.NewTool(
		"rag_status",
		mcp.WithDescription("系统状态：查看 Embedding Provider 健康状态、缓存命中率、Rerank 状态等信息。"),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		logrus.Info("[rag_status] Status requested")
		stats := rag.GetStats()

		type providerInfo struct {
			Name         string  `json:"name"`
			Status       string  `json:"status"`
			CircuitState string  `json:"circuit_state"`
			SuccessRate  float64 `json:"success_rate_percent"`
			AvgLatencyMs float64 `json:"avg_latency_ms"`
			Total        int64   `json:"total_requests"`
			Success      int64   `json:"success_requests"`
			Failed       int64   `json:"failed_requests"`
			Priority     int     `json:"priority"`
			Weight       int     `json:"weight"`
		}

		type cacheInfo struct {
			Hits      int64   `json:"hits"`
			Misses    int64   `json:"misses"`
			HitRate   float64 `json:"hit_rate_percent"`
			LocalSize int     `json:"local_size"`
			LocalCap  int     `json:"local_capacity"`
		}

		output := struct {
			Status    string         `json:"status"`
			Providers []providerInfo `json:"providers"`
			Cache     *cacheInfo     `json:"cache,omitempty"`
		}{
			Status: "ok",
		}

		if stats == nil {
			output.Status = "no embedding manager"
			output.Providers = []providerInfo{}
		} else {
			output.Providers = make([]providerInfo, len(stats))
			for i, s := range stats {
				output.Providers[i] = providerInfo{
					Name:         s.Name,
					Status:       string(s.Status),
					CircuitState: string(s.CircuitState),
					SuccessRate:  s.SuccessRate,
					AvgLatencyMs: float64(s.AvgLatency.Milliseconds()),
					Total:        s.TotalRequests,
					Success:      s.SuccessRequests,
					Failed:       s.FailedRequests,
					Priority:     s.Priority,
					Weight:       s.Weight,
				}
			}
		}

		if cache := rag.GetGlobalCache(); cache != nil {
			cs := cache.Stats()
			output.Cache = &cacheInfo{
				Hits:      cs.Hits,
				Misses:    cs.Misses,
				HitRate:   cs.HitRate,
				LocalSize: cs.LocalSize,
				LocalCap:  cs.LocalCap,
			}
		}

		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return toolError(rag.ErrCodeSearchFailed, "failed to serialize status")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- Tool 6: rag_delete_document ---

func (p *RAGToolProvider) deleteDocumentTool() Tool {
	definition := mcp.NewTool(
		"rag_delete_document",
		mcp.WithDescription("文档删除：删除指定文件的所有向量数据。"),
		mcp.WithString("file_id", mcp.Description("文件唯一标识"), mcp.Required()),
		mcp.WithNumber("user_id", mcp.Description("用户 ID"), mcp.Required()),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
		defer cancel()

		args := request.GetArguments()

		fileID, _ := args["file_id"].(string)
		if fileID == "" {
			return toolError(rag.ErrCodeInvalidInput, "file_id is required")
		}

		userIDFloat, _ := args["user_id"].(float64)
		if userIDFloat <= 0 {
			return toolError(rag.ErrCodeInvalidInput, "user_id is required and must be positive")
		}
		userID := uint(userIDFloat)

		retriever, err := p.createRetriever(ctx, userID)
		if err != nil {
			logrus.Errorf("[rag_delete_document] Failed to create retriever: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "failed to create retriever")
		}

		result, err := retriever.DeleteDocument(ctx, fileID)
		if err != nil {
			logrus.Errorf("[rag_delete_document] Delete failed: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "delete failed")
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return toolErrorSimple("failed to serialize result")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- Tool 7: rag_parse_document ---

func (p *RAGToolProvider) parseDocumentTool() Tool {
	definition := mcp.NewTool(
		"rag_parse_document",
		mcp.WithDescription("文档解析：解析文档并提取元数据、章节结构。支持 Markdown 结构识别、HTML 文本提取。"),
		mcp.WithString("content", mcp.Description("文档内容"), mcp.Required()),
		mcp.WithString("format", mcp.Description("文档格式: text/markdown/html（可选，默认自动检测）")),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		content, _ := args["content"].(string)
		if content == "" {
			return toolError(rag.ErrCodeInvalidInput, "content is required")
		}
		if len(content) > p.maxContentSize {
			return toolError(rag.ErrCodeContentTooLarge,
				fmt.Sprintf("%d bytes (max %d)", len(content), p.maxContentSize))
		}

		format, _ := args["format"].(string)

		doc, err := rag.ParseDocument(content, rag.DocumentFormat(format))
		if err != nil {
			return toolError(rag.ErrCodeParseFailed, err.Error())
		}

		logrus.Infof("[rag_parse_document] Parsed: format=%s, sections=%d, content_len=%d", doc.Format, len(doc.Sections), len(doc.Content))

		type output struct {
			Format     string                `json:"format"`
			Metadata   rag.DocumentMetadata  `json:"metadata"`
			Sections   []rag.DocumentSection `json:"sections,omitempty"`
			ContentLen int                   `json:"content_length"`
		}

		result := output{
			Format:     string(doc.Format),
			Metadata:   doc.Metadata,
			Sections:   doc.Sections,
			ContentLen: len(doc.Content),
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return toolError(rag.ErrCodeParseFailed, "failed to serialize result")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- Tool 8: rag_task_status ---

func (p *RAGToolProvider) taskStatusTool() Tool {
	definition := mcp.NewTool(
		"rag_task_status",
		mcp.WithDescription("异步任务状态：查询异步索引任务的进度和结果。"),
		mcp.WithString("task_id", mcp.Description("任务 ID（由 rag_index_document async=true 返回）"), mcp.Required()),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		taskID, _ := args["task_id"].(string)
		if taskID == "" {
			return toolError(rag.ErrCodeInvalidInput, "task_id is required")
		}

		if p.taskQueue == nil {
			// 防御式判断：避免异步索引未开启时调用导致空指针
			return toolErrorSimple("async indexing is not enabled")
		}

		task, err := p.taskQueue.GetStatus(ctx, taskID)
		if err != nil {
			logrus.Errorf("[rag_task_status] Failed to get status: %v", err)
			return toolErrorSimple("failed to get task status: " + err.Error())
		}
		if task == nil {
			return toolErrorSimple("task not found (may have expired): " + taskID)
		}

		logrus.Infof("[rag_task_status] task_id=%s, status=%s, file_id=%s", taskID, task.Status, task.FileID)

		data, err := json.MarshalIndent(task, "", "  ")
		if err != nil {
			return toolErrorSimple("failed to serialize task status")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// RegisterAllRAGTools 注册所有 RAG 工具
func RegisterAllRAGTools(registry *Registry, store rag.VectorStore, retCfg *rag.RetrieverConfig, chunkCfg *rag.ChunkingConfig, rerankCfg rag.RerankConfig, maxContentSize int, taskQueue *rag.TaskQueue) {
	registry.RegisterProvider(NewRAGToolProvider(store, retCfg, chunkCfg, rerankCfg, maxContentSize, taskQueue))
}
