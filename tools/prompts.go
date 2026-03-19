package tools

import (
	"context"
	"fmt"
	"strconv"

	"mcp_rag_server/rag"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sirupsen/logrus"
)

// RAGPromptProvider 提供内置 RAG Prompt 模板，持有检索依赖以在 Server 端自动获取上下文
type RAGPromptProvider struct {
	store     rag.VectorStore
	retCfg    *rag.RetrieverConfig
	chunkCfg  *rag.ChunkingConfig
	rerankCfg rag.RerankConfig
}

func NewRAGPromptProvider(store rag.VectorStore, retCfg *rag.RetrieverConfig, chunkCfg *rag.ChunkingConfig, rerankCfg rag.RerankConfig) *RAGPromptProvider {
	return &RAGPromptProvider{
		store:     store,
		retCfg:    retCfg,
		chunkCfg:  chunkCfg,
		rerankCfg: rerankCfg,
	}
}

func (p *RAGPromptProvider) GetPrompts() []Prompt {
	return []Prompt{
		p.summaryPrompt(),
		p.codingPrompt(),
		p.qaPrompt(),
		p.comparePrompt(),
	}
}

// retrieveContext 内部检索辅助函数，执行检索 + 可选 Rerank + 构建 Prompt 上下文
func (p *RAGPromptProvider) retrieveContext(ctx context.Context, query string, userID uint, fileIDs []string) string {
	retriever, err := rag.NewMultiFileRetriever(ctx, p.store, nil, p.retCfg, p.chunkCfg, userID)
	if err != nil {
		logrus.Warnf("[PromptProvider] Failed to create retriever: %v", err)
		return ""
	}

	results, err := retriever.Retrieve(ctx, query, fileIDs)
	if err != nil {
		logrus.Warnf("[PromptProvider] Retrieval failed: %v", err)
		return ""
	}

	if p.rerankCfg.Enabled && len(results) > 0 {
		reranked, rerr := rag.RerankResults(ctx, query, results, p.rerankCfg.TopN)
		if rerr == nil {
			results = reranked
		}
	}

	if len(results) == 0 {
		return "No relevant documents found in the knowledge base."
	}

	return rag.BuildMultiFileRAGPrompt(query, results)
}

func parseUserID(s string) uint {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return uint(n)
}

func (p *RAGPromptProvider) summaryPrompt() Prompt {
	def := mcp.Prompt{
		Name:        "RAG_Summary",
		Description: "Summarize a topic using your RAG knowledge base. Context is auto-retrieved from indexed documents.",
		Arguments: []mcp.PromptArgument{
			{
				Name:        "topic",
				Description: "The topic or question you want summarized",
				Required:    true,
			},
			{
				Name:        "user_id",
				Description: "Your user ID for knowledge base access",
				Required:    true,
			},
		},
	}
	handler := func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		topic := "Unknown topic"
		if val, ok := request.Params.Arguments["topic"]; ok {
			topic = val
		}
		userID := parseUserID(request.Params.Arguments["user_id"])

		contextBlock := p.retrieveContext(ctx, topic, userID, nil)

		return &mcp.GetPromptResult{
			Description: "RAG Summary with auto-retrieved context",
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: mcp.TextContent{
						Type: "text",
						Text: contextBlock,
					},
				},
			},
		}, nil
	}
	return NewBasePrompt(def, handler)
}

func (p *RAGPromptProvider) codingPrompt() Prompt {
	def := mcp.Prompt{
		Name:        "RAG_Coding",
		Description: "A prompt for coding tasks that require codebase context. Context is auto-retrieved.",
		Arguments: []mcp.PromptArgument{
			{
				Name:        "task",
				Description: "The coding task to perform",
				Required:    true,
			},
			{
				Name:        "user_id",
				Description: "Your user ID for knowledge base access",
				Required:    true,
			},
		},
	}
	handler := func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		task := "Unknown task"
		if val, ok := request.Params.Arguments["task"]; ok {
			task = val
		}
		userID := parseUserID(request.Params.Arguments["user_id"])

		contextBlock := p.retrieveContext(ctx, task, userID, nil)

		promptText := fmt.Sprintf("You are an expert software engineer. Please complete the following coding task:\n\nTask: %s\n\n%s", task, contextBlock)

		return &mcp.GetPromptResult{
			Description: "RAG Coding with auto-retrieved context",
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: mcp.TextContent{
						Type: "text",
						Text: promptText,
					},
				},
			},
		}, nil
	}
	return NewBasePrompt(def, handler)
}

func (p *RAGPromptProvider) qaPrompt() Prompt {
	def := mcp.Prompt{
		Name:        "RAG_QA",
		Description: "Answer a question using RAG knowledge base. Supports optional file scope filtering.",
		Arguments: []mcp.PromptArgument{
			{
				Name:        "question",
				Description: "The question to answer",
				Required:    true,
			},
			{
				Name:        "user_id",
				Description: "Your user ID for knowledge base access",
				Required:    true,
			},
			{
				Name:        "file_ids",
				Description: "Optional comma-separated file IDs to limit search scope",
			},
		},
	}
	handler := func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		question := request.Params.Arguments["question"]
		if question == "" {
			question = "Unknown question"
		}
		userID := parseUserID(request.Params.Arguments["user_id"])

		var fileIDs []string
		if fids, ok := request.Params.Arguments["file_ids"]; ok && fids != "" {
			for _, id := range splitAndTrim(fids) {
				fileIDs = append(fileIDs, id)
			}
		}

		contextBlock := p.retrieveContext(ctx, question, userID, fileIDs)

		return &mcp.GetPromptResult{
			Description: "RAG QA with auto-retrieved context",
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: mcp.TextContent{
						Type: "text",
						Text: contextBlock,
					},
				},
			},
		}, nil
	}
	return NewBasePrompt(def, handler)
}

func (p *RAGPromptProvider) comparePrompt() Prompt {
	def := mcp.Prompt{
		Name:        "RAG_Compare",
		Description: "Compare two items using knowledge from your RAG knowledge base.",
		Arguments: []mcp.PromptArgument{
			{
				Name:        "item_a",
				Description: "First item to compare",
				Required:    true,
			},
			{
				Name:        "item_b",
				Description: "Second item to compare",
				Required:    true,
			},
			{
				Name:        "user_id",
				Description: "Your user ID for knowledge base access",
				Required:    true,
			},
		},
	}
	handler := func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		itemA := request.Params.Arguments["item_a"]
		itemB := request.Params.Arguments["item_b"]
		userID := parseUserID(request.Params.Arguments["user_id"])

		query := fmt.Sprintf("Compare %s and %s", itemA, itemB)
		contextBlock := p.retrieveContext(ctx, query, userID, nil)

		promptText := fmt.Sprintf("Please compare the following two items based on the provided context:\n\nItem A: %s\nItem B: %s\n\n%s", itemA, itemB, contextBlock)

		return &mcp.GetPromptResult{
			Description: "RAG Compare with auto-retrieved context",
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: mcp.TextContent{
						Type: "text",
						Text: promptText,
					},
				},
			},
		}, nil
	}
	return NewBasePrompt(def, handler)
}

// splitAndTrim 按逗号分割并去除空白
func splitAndTrim(s string) []string {
	var result []string
	for _, part := range splitByComma(s) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitByComma(s string) []string {
	result := make([]string, 0)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
