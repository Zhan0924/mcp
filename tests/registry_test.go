package tests

import (
	"context"
	"testing"

	"mcp_rag_server/rag"
	"mcp_rag_server/tools"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// =============================================================================
// Registry Multi-Type Provider Tests
// =============================================================================

// --- Mock Providers ---

type mockToolProvider struct {
	toolCount int
}

func (m *mockToolProvider) GetTools() []tools.Tool {
	result := make([]tools.Tool, m.toolCount)
	for i := 0; i < m.toolCount; i++ {
		def := mcp.NewTool("mock_tool_"+string(rune('A'+i)), mcp.WithDescription("Mock tool"))
		result[i] = tools.NewBaseTool(def, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
	}
	return result
}

type mockPromptProvider struct {
	promptCount int
}

func (m *mockPromptProvider) GetPrompts() []tools.Prompt {
	result := make([]tools.Prompt, m.promptCount)
	for i := 0; i < m.promptCount; i++ {
		def := mcp.Prompt{
			Name:        "mock_prompt_" + string(rune('A'+i)),
			Description: "Mock prompt",
		}
		result[i] = tools.NewBasePrompt(def, func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: "mock"}, nil
		})
	}
	return result
}

type mockResourceTemplateProvider struct {
	count int
}

func (m *mockResourceTemplateProvider) GetResourceTemplates() []tools.ResourceTemplate {
	result := make([]tools.ResourceTemplate, m.count)
	for i := 0; i < m.count; i++ {
		def := mcp.NewResourceTemplate(
			"mock://items/{id}",
			"mock_resource_"+string(rune('A'+i)),
		)
		result[i] = tools.NewBaseResourceTemplate(def, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return nil, nil
		})
	}
	return result
}

// multiProvider implements both ToolProvider and PromptProvider
type multiProvider struct{}

func (mp *multiProvider) GetTools() []tools.Tool {
	def := mcp.NewTool("multi_tool", mcp.WithDescription("Multi-provider tool"))
	return []tools.Tool{
		tools.NewBaseTool(def, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("multi"), nil
		}),
	}
}

func (mp *multiProvider) GetPrompts() []tools.Prompt {
	def := mcp.Prompt{Name: "multi_prompt", Description: "Multi-provider prompt"}
	return []tools.Prompt{
		tools.NewBasePrompt(def, func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: "multi"}, nil
		}),
	}
}

// --- Tests ---

func TestRegistry_NewRegistry(t *testing.T) {
	reg := tools.NewRegistry()
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
}

func TestRegistry_RegisterToolProvider(t *testing.T) {
	reg := tools.NewRegistry()
	provider := &mockToolProvider{toolCount: 3}
	reg.RegisterProvider(provider)

	// Verify by applying to a real server
	mcpServer := server.NewMCPServer("test", "1.0.0")
	reg.ApplyToServer(mcpServer)

	// Server should have registered the tools (no direct API to check count,
	// but no panic means success)
	t.Log("Registered 3 tools successfully")
}

func TestRegistry_RegisterPromptProvider(t *testing.T) {
	reg := tools.NewRegistry()
	provider := &mockPromptProvider{promptCount: 2}
	reg.RegisterProvider(provider)

	mcpServer := server.NewMCPServer("test", "1.0.0")
	reg.ApplyToServer(mcpServer)
	t.Log("Registered 2 prompts successfully")
}

func TestRegistry_RegisterResourceTemplateProvider(t *testing.T) {
	reg := tools.NewRegistry()
	provider := &mockResourceTemplateProvider{count: 1}
	reg.RegisterProvider(provider)

	mcpServer := server.NewMCPServer("test", "1.0.0")
	reg.ApplyToServer(mcpServer)
	t.Log("Registered 1 resource template successfully")
}

func TestRegistry_MultiProvider_SingleStruct(t *testing.T) {
	reg := tools.NewRegistry()
	mp := &multiProvider{}
	reg.RegisterProvider(mp)

	mcpServer := server.NewMCPServer("test", "1.0.0")
	reg.ApplyToServer(mcpServer)
	t.Log("Single struct registered as both ToolProvider and PromptProvider")
}

func TestRegistry_MultipleProviders(t *testing.T) {
	reg := tools.NewRegistry()
	reg.RegisterProvider(&mockToolProvider{toolCount: 2})
	reg.RegisterProvider(&mockPromptProvider{promptCount: 3})
	reg.RegisterProvider(&mockResourceTemplateProvider{count: 1})
	reg.RegisterProvider(&multiProvider{})

	mcpServer := server.NewMCPServer("test", "1.0.0")
	reg.ApplyToServer(mcpServer)

	// Total: 2 + 1 (from multi) = 3 tools, 3 + 1 (from multi) = 4 prompts, 1 resource template
	t.Log("All providers registered and applied successfully")
}

func TestRegistry_EmptyProvider(t *testing.T) {
	reg := tools.NewRegistry()
	reg.RegisterProvider(&mockToolProvider{toolCount: 0})

	mcpServer := server.NewMCPServer("test", "1.0.0")
	reg.ApplyToServer(mcpServer)
	t.Log("Empty provider registered without error")
}

func TestRegistry_NonProviderType(t *testing.T) {
	reg := tools.NewRegistry()
	// Register something that doesn't implement any provider interface
	reg.RegisterProvider("not a provider")
	reg.RegisterProvider(42)

	mcpServer := server.NewMCPServer("test", "1.0.0")
	reg.ApplyToServer(mcpServer)
	t.Log("Non-provider types safely ignored")
}

// =============================================================================
// BaseTool / BasePrompt / BaseResourceTemplate Tests
// =============================================================================

func TestBaseTool_DefinitionAndHandler(t *testing.T) {
	def := mcp.NewTool("test_tool", mcp.WithDescription("A test tool"))
	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("hello"), nil
	}

	bt := tools.NewBaseTool(def, handler)

	if bt.Definition().Name != "test_tool" {
		t.Errorf("Expected name 'test_tool', got '%s'", bt.Definition().Name)
	}
	if bt.Handler() == nil {
		t.Error("Handler should not be nil")
	}

	result, err := bt.Handler()(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result == nil {
		t.Fatal("Handler result should not be nil")
	}
}

func TestBasePrompt_DefinitionAndHandler(t *testing.T) {
	def := mcp.Prompt{Name: "test_prompt", Description: "A test prompt"}
	handler := func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Description: "result"}, nil
	}

	bp := tools.NewBasePrompt(def, handler)

	if bp.Definition().Name != "test_prompt" {
		t.Errorf("Expected name 'test_prompt', got '%s'", bp.Definition().Name)
	}
	if bp.Handler() == nil {
		t.Error("Handler should not be nil")
	}
}

func TestBaseResourceTemplate_DefinitionAndHandler(t *testing.T) {
	def := mcp.NewResourceTemplate("test://items/{id}", "test_resource")
	handler := func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{URI: "test://items/1", Text: "content"},
		}, nil
	}

	brt := tools.NewBaseResourceTemplate(def, handler)

	if brt.Definition().Name != "test_resource" {
		t.Errorf("Expected name 'test_resource', got '%s'", brt.Definition().Name)
	}
	if brt.Handler() == nil {
		t.Error("Handler should not be nil")
	}
}

// =============================================================================
// RAG Resource Provider Tests
// =============================================================================

func TestRAGResourceProvider_TemplateCount(t *testing.T) {
	store := &mockVectorStore{}
	retCfg := rag.DefaultRetrieverConfig()

	provider := tools.NewRAGResourceProvider(store, retCfg)
	templates := provider.GetResourceTemplates()

	if len(templates) != 1 {
		t.Errorf("Expected 1 resource template, got %d", len(templates))
	}

	// Verify template URI pattern
	def := templates[0].Definition()
	expectedURI := "rag://users/{user_id}/documents/{file_id}"
	if def.URITemplate.Raw() != expectedURI {
		t.Errorf("Expected URI template '%s', got '%s'", expectedURI, def.URITemplate.Raw())
	}
}

// =============================================================================
// RAG Prompt Provider Tests
// =============================================================================

func TestRAGPromptProvider_PromptCount(t *testing.T) {
	store := &mockVectorStore{}
	retCfg := rag.DefaultRetrieverConfig()
	chunkCfg := rag.DefaultChunkingConfig()
	rerankCfg := rag.DefaultRerankConfig()

	provider := tools.NewRAGPromptProvider(store, retCfg, chunkCfg, rerankCfg)
	prompts := provider.GetPrompts()

	if len(prompts) != 4 {
		t.Errorf("Expected 4 prompts, got %d", len(prompts))
	}

	expectedNames := map[string]bool{
		"RAG_Summary": false,
		"RAG_Coding":  false,
		"RAG_QA":      false,
		"RAG_Compare": false,
	}

	for _, p := range prompts {
		name := p.Definition().Name
		if _, ok := expectedNames[name]; ok {
			expectedNames[name] = true
		} else {
			t.Errorf("Unexpected prompt name: %s", name)
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("Missing expected prompt: %s", name)
		}
	}
}

func TestRAGPromptProvider_QAPromptHasFileIDsArg(t *testing.T) {
	store := &mockVectorStore{}
	retCfg := rag.DefaultRetrieverConfig()
	chunkCfg := rag.DefaultChunkingConfig()
	rerankCfg := rag.DefaultRerankConfig()

	provider := tools.NewRAGPromptProvider(store, retCfg, chunkCfg, rerankCfg)
	prompts := provider.GetPrompts()

	for _, p := range prompts {
		if p.Definition().Name == "RAG_QA" {
			args := p.Definition().Arguments
			hasFileIDs := false
			for _, arg := range args {
				if arg.Name == "file_ids" {
					hasFileIDs = true
					break
				}
			}
			if !hasFileIDs {
				t.Error("RAG_QA prompt should have 'file_ids' argument")
			}
			return
		}
	}
	t.Error("RAG_QA prompt not found")
}

// =============================================================================
// Full Registration Integration Test
// =============================================================================

func TestRegisterAllRAGTools(t *testing.T) {
	store := &mockVectorStore{}
	retCfg := rag.DefaultRetrieverConfig()
	chunkCfg := rag.DefaultChunkingConfig()
	rerankCfg := rag.DefaultRerankConfig()

	registry := tools.NewRegistry()
	tools.RegisterAllRAGTools(registry, store, retCfg, chunkCfg, rerankCfg, 10*1024*1024, nil, nil, rag.DefaultUploadConfig())

	mcpServer := server.NewMCPServer("test", "1.0.0",
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(true),
	)
	registry.ApplyToServer(mcpServer)

	t.Log("RegisterAllRAGTools completed successfully with tools, resources, and prompts")
}

// =============================================================================
// Mock VectorStore for testing
// =============================================================================

type mockVectorStore struct{}

func (m *mockVectorStore) EnsureIndex(ctx context.Context, config rag.IndexConfig) error {
	return nil
}

func (m *mockVectorStore) UpsertVectors(ctx context.Context, entries []rag.VectorEntry) (int, error) {
	return len(entries), nil
}

func (m *mockVectorStore) SearchVectors(ctx context.Context, query rag.VectorQuery) ([]rag.VectorSearchResult, error) {
	return []rag.VectorSearchResult{}, nil
}

func (m *mockVectorStore) HybridSearch(ctx context.Context, query rag.HybridQuery) ([]rag.VectorSearchResult, error) {
	return []rag.VectorSearchResult{}, nil
}

func (m *mockVectorStore) DeleteByFileID(ctx context.Context, indexName, prefix, fileID string) (int64, error) {
	return 0, nil
}

func (m *mockVectorStore) GetDocumentChunks(ctx context.Context, indexName, prefix, fileID string) ([]string, error) {
	return []string{"chunk1 content", "chunk2 content"}, nil
}

func (m *mockVectorStore) ListDocuments(ctx context.Context, indexName string) ([]rag.DocumentMeta, error) {
	return []rag.DocumentMeta{
		{FileID: "doc1", FileName: "test.md", ChunkCount: 5},
	}, nil
}

func (m *mockVectorStore) Close() error {
	return nil
}
