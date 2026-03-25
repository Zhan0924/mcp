package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"mcp_rag_server/rag"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sirupsen/logrus"
)

type RAGResourceProvider struct {
	store  rag.VectorStore
	retCfg *rag.RetrieverConfig
}

func NewRAGResourceProvider(store rag.VectorStore, retCfg *rag.RetrieverConfig) *RAGResourceProvider {
	return &RAGResourceProvider{
		store:  store,
		retCfg: retCfg,
	}
}

// GetResources 返回空的静态资源列表。
// 必须实现此方法（即使返回空数组），否则 mcp-go SDK 的 handleListResources
// 在 resources map 为空时会返回 JSON "resources": null 而非 []，
// 导致 Cursor 等 MCP 客户端的 JSON Schema 校验报错：
//
//	"expected array, received null"
func (p *RAGResourceProvider) GetResources() []Resource {
	return []Resource{}
}

func (p *RAGResourceProvider) GetResourceTemplates() []ResourceTemplate {
	return []ResourceTemplate{
		p.documentTemplate(),
	}
}

// documentTemplate 注册文档资源模板，URI 包含 user_id 支持多租户
// URI 格式: rag://users/{user_id}/documents/{file_id}
func (p *RAGResourceProvider) documentTemplate() ResourceTemplate {
	def := mcp.NewResourceTemplate(
		"rag://users/{user_id}/documents/{file_id}",
		"rag_document",
	)
	def.Description = "Read the full contents of an indexed document by user_id and file_id"
	def.MIMEType = "text/plain"

	handler := func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		uri := request.Params.URI

		userID, fileID, err := parseDocumentURI(uri)
		if err != nil {
			return nil, err
		}

		indexName := fmt.Sprintf(p.retCfg.UserIndexNameTemplate, userID)
		indexPrefix := fmt.Sprintf(p.retCfg.UserIndexPrefixTemplate, userID)

		chunks, err := p.store.GetDocumentChunks(ctx, indexName, indexPrefix, fileID)
		if err != nil {
			logrus.Errorf("[ResourceProvider] Failed to get document chunks for %s: %v", fileID, err)
			return nil, fmt.Errorf("failed to retrieve document: %v", err)
		}

		if len(chunks) == 0 {
			return nil, fmt.Errorf("document not found or empty: %s", fileID)
		}

		// 将所有的 chunk 按顺序拼接还原出完整的文档内容
		fullText := strings.Join(chunks, "\n\n")

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      uri,
				MIMEType: "text/plain",
				Text:     fullText,
			},
		}, nil
	}

	return NewBaseResourceTemplate(def, handler)
}

// parseDocumentURI 解析文档资源 URI，提取 user_id 和 file_id
// 支持格式: rag://users/{user_id}/documents/{file_id}
// 向后兼容: rag://documents/{file_id} (默认 userID=1)
func parseDocumentURI(uri string) (uint, string, error) {
	// 新格式: rag://users/{user_id}/documents/{file_id}
	if strings.HasPrefix(uri, "rag://users/") {
		rest := strings.TrimPrefix(uri, "rag://users/")
		parts := strings.SplitN(rest, "/documents/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return 0, "", fmt.Errorf("invalid URI format: %s, expected rag://users/{user_id}/documents/{file_id}", uri)
		}
		uid, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return 0, "", fmt.Errorf("invalid user_id in URI: %s", parts[0])
		}
		return uint(uid), parts[1], nil
	}

	// 向后兼容旧格式: rag://documents/{file_id}
	if strings.HasPrefix(uri, "rag://documents/") {
		fileID := strings.TrimPrefix(uri, "rag://documents/")
		if fileID == "" {
			return 0, "", fmt.Errorf("missing file_id in URI")
		}
		return 1, fileID, nil
	}

	return 0, "", fmt.Errorf("invalid URI scheme: %s", uri)
}
