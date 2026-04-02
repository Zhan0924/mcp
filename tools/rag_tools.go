/*
┌──────────────────────────────────────────────────────────────────────────────┐
│ rag_tools.go — MCP 工具实现 (RAG Tool Provider)                               │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标: 将 rag 包能力以 MCP Tool 形式暴露给客户端                               │
│                                                                              │
│ 结构:                                                                       │
│  - RAGToolProvider: 统一持有 store / config / queue                          │
│  - toolError/toolErrorSimple: MCP 统一错误输出格式                            │
│  - 9 个工具:                                                                │
│      rag_search / rag_index_document / rag_index_url / rag_build_prompt       │
│      rag_chunk_text / rag_status / rag_delete_document / rag_parse_document   │
│      rag_task_status                                                          │
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
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mcp_rag_server/rag"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sirupsen/logrus"
)

const (
	defaultToolTimeout  = 60 * time.Second
	urlFetchTimeout     = 30 * time.Second
	maxTopKHardLimit    = 100
	maxURLResponseBytes = 5 * 1024 * 1024 // 5MB
)

// RAGToolProvider RAG 工具提供者
type RAGToolProvider struct {
	store           rag.VectorStore
	retCfg          *rag.RetrieverConfig
	chunkCfg        *rag.ChunkingConfig
	rerankCfg       rag.RerankConfig
	maxContentSize  int
	taskQueue       *rag.TaskQueue      // nil 表示未启用异步索引
	graphStore      rag.GraphStore      // nil 表示未启用 Graph RAG
	entityExtractor rag.EntityExtractor // nil 表示未启用实体提取
	uploadStore     *rag.UploadStore    // nil 表示未启用文件上传
	uploadCfg       rag.UploadConfig    // 上传配置（auto-async 阈值等）
}

// NewRAGToolProvider 创建 RAG 工具提供者
func NewRAGToolProvider(store rag.VectorStore, retCfg *rag.RetrieverConfig, chunkCfg *rag.ChunkingConfig, rerankCfg rag.RerankConfig, maxContentSize int, taskQueue *rag.TaskQueue, graphStore rag.GraphStore, extractor rag.EntityExtractor, uploadStore *rag.UploadStore, uploadCfg rag.UploadConfig) *RAGToolProvider {
	if maxContentSize <= 0 {
		maxContentSize = 10 * 1024 * 1024
	}
	return &RAGToolProvider{
		store:           store,
		retCfg:          retCfg,
		chunkCfg:        chunkCfg,
		rerankCfg:       rerankCfg,
		maxContentSize:  maxContentSize,
		taskQueue:       taskQueue,
		graphStore:      graphStore,
		entityExtractor: extractor,
		uploadStore:     uploadStore,
		uploadCfg:       uploadCfg,
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
		p.indexURLTool(),
		p.buildPromptTool(),
		p.chunkTextTool(),
		p.statusTool(),
		p.deleteDocumentTool(),
		p.parseDocumentTool(),
		p.listDocumentsTool(),
		p.exportDataTool(),
	}
	// 异步索引未启用时，不暴露 task_status，避免客户端误调用
	if p.taskQueue != nil {
		tools = append(tools, p.taskStatusTool())
	}
	// Graph RAG 启用时暴露图谱搜索工具
	if p.graphStore != nil {
		tools = append(tools, p.graphSearchTool())
	}
	return tools
}

func (p *RAGToolProvider) createRetriever(ctx context.Context, userID uint) (*rag.MultiFileRetriever, error) {
	return rag.NewMultiFileRetriever(ctx, p.store, nil, p.retCfg, p.chunkCfg, userID)
}

// createRetrieverWithCollection 创建带 collection 的检索器
func (p *RAGToolProvider) createRetrieverWithCollection(ctx context.Context, userID uint, collection string) (*rag.MultiFileRetriever, error) {
	r, err := rag.NewMultiFileRetriever(ctx, p.store, nil, p.retCfg, p.chunkCfg, userID)
	if err != nil {
		return nil, err
	}
	if collection != "" && collection != "default" {
		r.SetCollection(collection)
	}
	return r, nil
}

// extractCollection 从工具参数中提取 collection 名称
func extractCollection(args map[string]interface{}) string {
	if c, ok := args["collection"].(string); ok && c != "" {
		return c
	}
	return ""
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
		mcp.WithString("collection", mcp.Description("知识库集合名称（可选，默认使用主知识库）")),
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

		// Collection 支持
		if col := extractCollection(args); col != "" {
			retriever.SetCollection(col)
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
		mcp.WithDescription("文档索引：将文档分块、向量化并存入向量索引。支持纯文本、Markdown、HTML、PDF、DOCX 格式，自动识别表格和图片，结构感知分块。PDF/DOCX 内容需 base64 编码传入。设置 async=true 可异步执行，立即返回 task_id。如需索引网页，请使用 rag_index_url 工具。大文件可先通过 POST /upload 上传，再传入 upload_id。"),
		mcp.WithString("content", mcp.Description("文档内容（PDF/DOCX 为 base64 编码）。与 upload_id 二选一，小文件直接传内容，大文件用 upload_id")),
		mcp.WithString("upload_id", mcp.Description("上传文件 ID（通过 POST /upload 获取，与 content 二选一，大文件使用）")),
		mcp.WithString("file_id", mcp.Description("文件唯一标识"), mcp.Required()),
		mcp.WithNumber("user_id", mcp.Description("用户 ID"), mcp.Required()),
		mcp.WithString("file_name", mcp.Description("文件名（可选）")),
		mcp.WithString("format", mcp.Description("文档格式: text/markdown/html/pdf/docx（可选，默认自动检测）")),
		mcp.WithString("collection", mcp.Description("知识库集合名称（可选，默认使用主知识库）")),
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

		content, _ := args["content"].(string)
		uploadID, _ := args["upload_id"].(string)

		// upload_id 模式：从 UploadStore 加载文件内容
		if uploadID != "" && p.uploadStore != nil {
			data, meta, loadErr := p.uploadStore.Load(ctx, uploadID)
			if loadErr != nil {
				return toolError(rag.ErrCodeInvalidInput, "upload not found or expired: "+uploadID)
			}
			// 根据格式决定内容处理方式
			detectedFmt := rag.DetectFormatByFileName(meta.FileName)
			if detectedFmt == rag.FormatPDF || detectedFmt == rag.FormatDOCX {
				// 二进制格式: base64 编码后走现有解析流程
				content = base64Encode(data)
			} else {
				content = string(data)
			}
			if fileName == "" || fileName == fileID {
				fileName = meta.FileName
			}
			if format == "" {
				format = meta.Format
			}
			logrus.Infof("[rag_index_document] Loaded upload %s: size=%d, file=%s", uploadID, len(data), meta.FileName)
			// 加载成功后异步删除暂存
			go p.uploadStore.Delete(context.Background(), uploadID)
		}

		if content == "" {
			return toolError(rag.ErrCodeInvalidInput, "content or upload_id is required")
		}
		if len(content) > p.maxContentSize {
			return toolError(rag.ErrCodeContentTooLarge,
				fmt.Sprintf("%d bytes (max %d)", len(content), p.maxContentSize))
		}

		// 智能异步：内容超过阈值时自动切换异步模式
		autoAsync := false
		if p.uploadCfg.AutoAsyncThreshold > 0 && len(content) > p.uploadCfg.AutoAsyncThreshold && p.taskQueue != nil && !asyncMode {
			autoAsync = true
			asyncMode = true
			logrus.Infof("[rag_index_document] Auto-async: content size %d > threshold %d",
				len(content), p.uploadCfg.AutoAsyncThreshold)
		}
		if asyncMode && p.taskQueue != nil {
			taskID, err := p.taskQueue.Submit(ctx, userID, fileID, fileName, content, format)
			if err != nil {
				logrus.Errorf("[rag_index_document] Async submit failed: %v", err)
				return toolError(rag.ErrCodeBatchFailed, "async submit failed: "+err.Error())
			}
			msg := "Document indexing submitted. Use rag_task_status to check progress."
			if autoAsync {
				msg = fmt.Sprintf("Large document (%d bytes) auto-submitted for async indexing. Use rag_task_status to check progress.", len(content))
			}
			result := map[string]interface{}{
				"task_id": taskID,
				"status":  rag.TaskStatusPending,
				"message": msg,
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		}

		// 同步模式：保持原有行为，调用方同步等待索引完成
		docFormat := rag.DocumentFormat(format)
		if docFormat == "" && isPDFContent(content) {
			docFormat = rag.FormatPDF
		}
		if docFormat == "" && isDOCXContent(content) {
			docFormat = rag.FormatDOCX
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

		// Collection 支持
		if col := extractCollection(args); col != "" {
			retriever.SetCollection(col)
		}

		result, err := retriever.IndexDocument(ctx, fileID, fileName, content)
		if err != nil {
			logrus.Errorf("[rag_index_document] Indexing failed: %v", err)
			return toolError(rag.ErrCodeBatchFailed, "indexing failed")
		}

		// Graph RAG 自动实体提取：索引成功后异步提取实体和关系写入图存储
		p.extractAndStoreEntities(ctx, content, fileID)

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

// isDOCXContent 检测内容是否为 base64 编码的 DOCX
func isDOCXContent(content string) bool {
	if strings.HasPrefix(content, "data:application/vnd.openxmlformats-officedocument") {
		return true
	}
	// base64 编码的 ZIP 文件 (PK\x03\x04) 头部为 "UEsDB"
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "UEsDB")
}

// --- Tool 3: rag_index_url ---

func (p *RAGToolProvider) indexURLTool() Tool {
	toolOpts := []mcp.ToolOption{
		mcp.WithDescription("网页索引：抓取指定 URL 的网页内容，自动提取正文（去除 HTML 标签），分块向量化后存入知识库索引。适用于将在线文档、博客文章、技术文档等网页内容纳入 RAG 检索范围。对于内容较多的网页，建议设置 async=true 异步索引以避免超时。"),
		mcp.WithString("url", mcp.Description("要抓取的网页 URL（必须是 http:// 或 https:// 开头）"), mcp.Required()),
		mcp.WithNumber("user_id", mcp.Description("用户 ID"), mcp.Required()),
		mcp.WithString("file_id", mcp.Description("文件唯一标识（可选，默认根据 URL 自动生成）")),
		mcp.WithString("file_name", mcp.Description("文件名（可选，默认使用 URL）")),
	}
	if p.taskQueue != nil {
		toolOpts = append(toolOpts,
			mcp.WithBoolean("async", mcp.Description("是否异步索引（默认 false）。大网页建议开启，避免 Embedding 批量超时触发熔断。启用后立即返回 task_id。")),
		)
	}
	definition := mcp.NewTool("rag_index_url", toolOpts...)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
		defer cancel()

		args := request.GetArguments()

		rawURL, _ := args["url"].(string)
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return toolError(rag.ErrCodeInvalidInput, "url is required")
		}
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
			return toolError(rag.ErrCodeInvalidInput, "url must start with http:// or https://")
		}

		userIDFloat, _ := args["user_id"].(float64)
		if userIDFloat <= 0 {
			return toolError(rag.ErrCodeInvalidInput, "user_id is required and must be positive")
		}
		userID := uint(userIDFloat)

		fileID, _ := args["file_id"].(string)
		if fileID == "" {
			// 根据 URL 生成确定性 file_id，同一 URL 多次索引会覆盖而非重复
			hash := sha256.Sum256([]byte(rawURL))
			fileID = fmt.Sprintf("url_%x", hash[:8])
		}

		fileName, _ := args["file_name"].(string)
		if fileName == "" {
			fileName = rawURL
		}

		// --- 1. 抓取网页内容 ---
		logrus.Infof("[rag_index_url] Fetching URL: %s", rawURL)

		httpClient := &http.Client{Timeout: urlFetchTimeout}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return toolError(rag.ErrCodeInvalidInput, "invalid URL: "+err.Error())
		}
		// 设置 User-Agent 避免被反爬策略拒绝
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; RAG-MCP-Bot/1.0)")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,*/*")

		resp, err := httpClient.Do(req)
		if err != nil {
			logrus.Errorf("[rag_index_url] Fetch failed: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "failed to fetch URL: "+err.Error())
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return toolError(rag.ErrCodeSearchFailed,
				fmt.Sprintf("URL returned HTTP %d", resp.StatusCode))
		}

		// 限制读取大小，防止抓取超大页面耗尽内存
		limitedReader := io.LimitReader(resp.Body, maxURLResponseBytes)
		htmlBytes, err := io.ReadAll(limitedReader)
		if err != nil {
			logrus.Errorf("[rag_index_url] Read body failed: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "failed to read URL content")
		}

		htmlContent := string(htmlBytes)
		if strings.TrimSpace(htmlContent) == "" {
			return toolError(rag.ErrCodeInvalidInput, "URL returned empty content")
		}

		logrus.Infof("[rag_index_url] Fetched %d bytes from %s", len(htmlBytes), rawURL)

		// --- 2. 解析 HTML，提取纯文本 ---
		doc, err := rag.ParseDocument(htmlContent, rag.FormatHTML)
		var content string
		if err != nil {
			logrus.Warnf("[rag_index_url] HTML parse failed, using raw content: %v", err)
			content = htmlContent
		} else {
			content = doc.Content
			logrus.Infof("[rag_index_url] Parsed HTML: title=%q, chars=%d",
				doc.Metadata.Title, doc.Metadata.CharCount)
			// 如果解析出了 title 且用户没指定 file_name，用 title 替代 URL
			if doc.Metadata.Title != "" && fileName == rawURL {
				fileName = doc.Metadata.Title
			}
		}

		if strings.TrimSpace(content) == "" {
			return toolError(rag.ErrCodeInvalidInput, "URL content is empty after HTML parsing")
		}

		// --- 3. 异步模式：大网页提交到 TaskQueue，避免同步超时触发 Embedding 熔断 ---
		asyncMode, _ := args["async"].(bool)
		if asyncMode && p.taskQueue != nil {
			taskID, err := p.taskQueue.Submit(ctx, userID, fileID, fileName, content, "markdown")
			if err != nil {
				logrus.Errorf("[rag_index_url] Async submit failed: %v", err)
				return toolError(rag.ErrCodeBatchFailed, "async submit failed: "+err.Error())
			}
			result := map[string]interface{}{
				"url":       rawURL,
				"file_id":   fileID,
				"file_name": fileName,
				"task_id":   taskID,
				"status":    rag.TaskStatusPending,
				"chars":     len(content),
				"message":   "URL content fetched and submitted for async indexing. Use rag_task_status to check progress.",
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		}

		// --- 4. 同步模式：复用已有索引流程 ---
		retriever, err := p.createRetriever(ctx, userID)
		if err != nil {
			logrus.Errorf("[rag_index_url] Failed to create retriever: %v", err)
			return toolError(rag.ErrCodeIndexCreateFailed, "failed to create retriever")
		}

		result, err := retriever.IndexDocument(ctx, fileID, fileName, content)
		if err != nil {
			logrus.Errorf("[rag_index_url] Indexing failed: %v", err)
			return toolError(rag.ErrCodeBatchFailed, "indexing failed")
		}

		// 在返回结果中附带 URL 和实际使用的 file_id
		output := map[string]interface{}{
			"url":          rawURL,
			"file_id":      fileID,
			"file_name":    fileName,
			"total_chunks": result.TotalChunks,
			"indexed":      result.Indexed,
			"failed":       result.Failed,
			"cached":       result.Cached,
		}

		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return toolError(rag.ErrCodeBatchFailed, "failed to serialize result")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- Tool 4: rag_build_prompt ---

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
		mcp.WithDescription("文档解析：解析文档并提取元数据、章节结构。支持 Markdown 结构识别、HTML 文本提取、PDF/DOCX 结构化解析。"),
		mcp.WithString("content", mcp.Description("文档内容（PDF/DOCX 为 base64 编码）"), mcp.Required()),
		mcp.WithString("format", mcp.Description("文档格式: text/markdown/html/pdf/docx（可选，默认自动检测）")),
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

// --- Tool 10: rag_list_documents ---

func (p *RAGToolProvider) listDocumentsTool() Tool {
	definition := mcp.NewTool(
		"rag_list_documents",
		mcp.WithDescription("文档列表：列出用户知识库中已索引的所有文档，返回文件 ID、名称和分块数量。"),
		mcp.WithNumber("user_id", mcp.Description("用户 ID"), mcp.Required()),
		mcp.WithString("collection", mcp.Description("知识库集合名称（可选，默认使用主知识库）")),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
		defer cancel()

		args := request.GetArguments()

		userIDFloat, _ := args["user_id"].(float64)
		if userIDFloat <= 0 {
			return toolError(rag.ErrCodeInvalidInput, "user_id is required and must be positive")
		}
		userID := uint(userIDFloat)

		retriever, err := p.createRetriever(ctx, userID)
		if err != nil {
			logrus.Errorf("[rag_list_documents] Failed to create retriever: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "failed to create retriever")
		}

		if col := extractCollection(args); col != "" {
			retriever.SetCollection(col)
		}

		docs, err := retriever.ListDocuments(ctx)
		if err != nil {
			logrus.Errorf("[rag_list_documents] List failed: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "list documents failed")
		}

		output := map[string]interface{}{
			"total_documents": len(docs),
			"documents":       docs,
		}

		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return toolError(rag.ErrCodeSearchFailed, "failed to serialize results")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// --- Tool 11: rag_export_data ---

func (p *RAGToolProvider) exportDataTool() Tool {
	definition := mcp.NewTool(
		"rag_export_data",
		mcp.WithDescription("数据导出：导出用户知识库中指定文档的所有分块内容，可用于备份、迁移或导入到其他系统。"),
		mcp.WithNumber("user_id", mcp.Description("用户 ID"), mcp.Required()),
		mcp.WithString("file_id", mcp.Description("要导出的文件 ID（可选，不填则导出文档列表）")),
		mcp.WithString("collection", mcp.Description("知识库集合名称（可选）")),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
		defer cancel()

		args := request.GetArguments()

		userIDFloat, _ := args["user_id"].(float64)
		if userIDFloat <= 0 {
			return toolError(rag.ErrCodeInvalidInput, "user_id is required and must be positive")
		}
		userID := uint(userIDFloat)

		collection := extractCollection(args)

		// 如果指定了 file_id，导出该文档的所有分块
		fileID, _ := args["file_id"].(string)
		if fileID != "" {
			retriever, err := p.createRetrieverWithCollection(ctx, userID, collection)
			if err != nil {
				return toolError(rag.ErrCodeSearchFailed, "failed to create retriever")
			}
			_ = retriever // 仅用于生成索引名

			// 直接通过 VectorStore 获取文档分块
			indexName := fmt.Sprintf(p.retCfg.UserIndexNameTemplate, userID)
			indexPrefix := fmt.Sprintf(p.retCfg.UserIndexPrefixTemplate, userID)
			if collection != "" && collection != "default" {
				indexName = strings.Replace(indexName, ":idx", "_"+collection+":idx", 1)
				indexPrefix = strings.TrimSuffix(indexPrefix, ":") + "_" + collection + ":"
			}

			chunks, err := p.store.GetDocumentChunks(ctx, indexName, indexPrefix, fileID)
			if err != nil {
				logrus.Errorf("[rag_export_data] Export failed: %v", err)
				return toolError(rag.ErrCodeSearchFailed, "export failed")
			}

			output := map[string]interface{}{
				"file_id":      fileID,
				"total_chunks": len(chunks),
				"chunks":       chunks,
				"export_time":  time.Now().UTC(),
			}

			data, _ := json.MarshalIndent(output, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		}

		// 未指定 file_id 时导出文档列表概览
		retriever, err := p.createRetrieverWithCollection(ctx, userID, collection)
		if err != nil {
			return toolError(rag.ErrCodeSearchFailed, "failed to create retriever")
		}

		docs, err := retriever.ListDocuments(ctx)
		if err != nil {
			return toolError(rag.ErrCodeSearchFailed, "list documents failed")
		}

		output := map[string]interface{}{
			"total_documents": len(docs),
			"documents":       docs,
			"export_time":     time.Now().UTC(),
			"hint":            "Specify file_id to export full document chunks for backup.",
		}

		data, _ := json.MarshalIndent(output, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

// base64Encode 将二进制数据编码为 base64 字符串
func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// RegisterAllRAGTools 注册所有 RAG 工具、资源和提示词
func RegisterAllRAGTools(registry *Registry, store rag.VectorStore, retCfg *rag.RetrieverConfig, chunkCfg *rag.ChunkingConfig, rerankCfg rag.RerankConfig, maxContentSize int, taskQueue *rag.TaskQueue, uploadStore *rag.UploadStore, uploadCfg rag.UploadConfig, graphStore ...interface{}) {
	var gs rag.GraphStore
	var ee rag.EntityExtractor
	if len(graphStore) >= 1 && graphStore[0] != nil {
		gs, _ = graphStore[0].(rag.GraphStore)
	}
	if len(graphStore) >= 2 && graphStore[1] != nil {
		ee, _ = graphStore[1].(rag.EntityExtractor)
	}
	registry.RegisterProvider(NewRAGToolProvider(store, retCfg, chunkCfg, rerankCfg, maxContentSize, taskQueue, gs, ee, uploadStore, uploadCfg))
	registry.RegisterProvider(NewRAGResourceProvider(store, retCfg))
	registry.RegisterProvider(NewRAGPromptProvider(store, retCfg, chunkCfg, rerankCfg))
}

// extractAndStoreEntities 索引成功后自动提取实体和关系写入图存储。
// 采用 fire-and-forget 策略：在独立 goroutine 中使用独立的 context（不继承 tool 超时），
// 避免多次 LLM 调用耗时受 tool 的 60s 超时限制。
func (p *RAGToolProvider) extractAndStoreEntities(_ context.Context, content, fileID string) {
	if p.graphStore == nil || p.entityExtractor == nil {
		return
	}

	// 用独立 context，给实体提取充足时间（10分钟）
	go func() {
		extractCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		entities, relations, err := p.entityExtractor.Extract(extractCtx, content, fileID)
		if err != nil {
			logrus.Warnf("[rag_index_document] Entity extraction failed (non-blocking): %v", err)
			return
		}

		if len(entities) > 0 {
			if err := p.graphStore.AddEntities(extractCtx, entities); err != nil {
				logrus.Warnf("[rag_index_document] Failed to store entities: %v", err)
			}
		}
		if len(relations) > 0 {
			if err := p.graphStore.AddRelations(extractCtx, relations); err != nil {
				logrus.Warnf("[rag_index_document] Failed to store relations: %v", err)
			}
		}

		if len(entities) > 0 || len(relations) > 0 {
			logrus.Infof("[rag_index_document] Graph RAG: extracted %d entities, %d relations for file %s",
				len(entities), len(relations), fileID)
		}
	}()
}

// --- Tool 9: rag_graph_search ---

func (p *RAGToolProvider) graphSearchTool() Tool {
	definition := mcp.NewTool(
		"rag_graph_search",
		mcp.WithDescription("知识图谱搜索：在知识图谱中搜索实体和关系。支持按实体名称或自然语言查询搜索，返回相关实体、关系和上下文。可与向量检索结合使用，提升多跳推理和关系查询场景的 RAG 效果。"),
		mcp.WithString("query", mcp.Description("搜索查询（实体名称或自然语言问题）"), mcp.Required()),
		mcp.WithString("search_type", mcp.Description("搜索类型: entity 或 query（默认 query）")),
		mcp.WithNumber("depth", mcp.Description("图遍历深度（1-3，默认 2，仅 entity 模式）")),
		mcp.WithNumber("top_k", mcp.Description("返回结果数量（默认 10，仅 query 模式）")),
		mcp.WithBoolean("merge_vector", mcp.Description("是否与向量检索结果融合（默认 false）")),
		mcp.WithNumber("user_id", mcp.Description("用户 ID（融合向量检索时需要）")),
	)

	return NewBaseTool(definition, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
		defer cancel()

		args := request.GetArguments()

		query, _ := args["query"].(string)
		if query == "" {
			return toolError(rag.ErrCodeInvalidInput, "query is required")
		}

		searchType, _ := args["search_type"].(string)
		if searchType == "" {
			searchType = "query"
		}

		var graphResult *rag.GraphSearchResult
		var err error

		switch searchType {
		case "entity":
			depth := 2
			if d, ok := args["depth"].(float64); ok && d > 0 {
				depth = int(d)
			}
			graphResult, err = p.graphStore.SearchByEntity(ctx, query, depth)
		case "query":
			topK := 10
			if tk, ok := args["top_k"].(float64); ok && tk > 0 {
				topK = int(tk)
			}
			graphResult, err = p.graphStore.SearchByQuery(ctx, query, topK)
		default:
			return toolError(rag.ErrCodeInvalidInput, "search_type must be 'entity' or 'query'")
		}

		if err != nil {
			logrus.Errorf("[rag_graph_search] Search failed: %v", err)
			return toolError(rag.ErrCodeSearchFailed, "graph search failed")
		}

		// 可选: 与向量检索结果融合
		mergeVector, _ := args["merge_vector"].(bool)
		if mergeVector {
			userIDFloat, _ := args["user_id"].(float64)
			if userIDFloat > 0 {
				userID := uint(userIDFloat)
				retriever, rErr := p.createRetriever(ctx, userID)
				if rErr == nil {
					retriever.SetTopK(5)
					vectorResults, vErr := retriever.Retrieve(ctx, query, nil)
					if vErr == nil {
						merged := rag.MergeGraphAndVectorResults(graphResult, vectorResults)
						data, _ := json.MarshalIndent(merged, "", "  ")
						return mcp.NewToolResultText(string(data)), nil
					}
				}
			}
		}

		data, err := json.MarshalIndent(graphResult, "", "  ")
		if err != nil {
			return toolError(rag.ErrCodeSearchFailed, "failed to serialize results")
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}
