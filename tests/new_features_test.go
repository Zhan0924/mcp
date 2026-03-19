package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"mcp_rag_server/rag"
)

// =============================================================================
// HyDEConfig Tests
// =============================================================================

func TestHyDEConfig_Struct(t *testing.T) {
	cfg := rag.HyDEConfig{
		BaseURL:     "https://api.example.com/v1",
		APIKey:      "test-key",
		Model:       "qwen-turbo",
		MaxTokens:   512,
		Temperature: 0.5,
	}

	if cfg.BaseURL != "https://api.example.com/v1" {
		t.Errorf("Unexpected BaseURL: %s", cfg.BaseURL)
	}
	if cfg.APIKey != "test-key" {
		t.Errorf("Unexpected APIKey: %s", cfg.APIKey)
	}
	if cfg.Model != "qwen-turbo" {
		t.Errorf("Unexpected Model: %s", cfg.Model)
	}
	if cfg.MaxTokens != 512 {
		t.Errorf("Unexpected MaxTokens: %d", cfg.MaxTokens)
	}
	if cfg.Temperature != 0.5 {
		t.Errorf("Unexpected Temperature: %f", cfg.Temperature)
	}
}

func TestHyDETransformer_DefaultValues(t *testing.T) {
	// Test that NewHyDETransformer applies defaults for empty config
	cfg := rag.HyDEConfig{
		APIKey: "test-key",
		// Everything else is zero/empty
	}

	transformer := rag.NewHyDETransformer(cfg)
	if transformer == nil {
		t.Fatal("NewHyDETransformer should not return nil")
	}
}

func TestHyDETransformer_WithFullConfig(t *testing.T) {
	cfg := rag.HyDEConfig{
		BaseURL:     "https://custom.api.com/v1",
		APIKey:      "custom-key",
		Model:       "custom-model",
		MaxTokens:   1024,
		Temperature: 0.7,
	}

	transformer := rag.NewHyDETransformer(cfg)
	if transformer == nil {
		t.Fatal("NewHyDETransformer should not return nil")
	}
}

func TestHyDETransformer_URLCleanup(t *testing.T) {
	// BaseURL with trailing /embeddings should be cleaned up
	cfg := rag.HyDEConfig{
		BaseURL: "https://api.example.com/v1/embeddings",
		APIKey:  "test-key",
	}

	transformer := rag.NewHyDETransformer(cfg)
	if transformer == nil {
		t.Fatal("NewHyDETransformer should not return nil")
	}
	// No direct way to verify the internal field, but it shouldn't crash
}

// =============================================================================
// VectorStoreConfig Tests
// =============================================================================

func TestVectorStoreConfig_Struct(t *testing.T) {
	cfg := rag.VectorStoreConfig{
		Type: "redis",
	}
	if cfg.Type != "redis" {
		t.Errorf("Expected type 'redis', got '%s'", cfg.Type)
	}

	cfg2 := rag.VectorStoreConfig{
		Type: "milvus",
	}
	if cfg2.Type != "milvus" {
		t.Errorf("Expected type 'milvus', got '%s'", cfg2.Type)
	}
}

// =============================================================================
// DocumentMeta Tests
// =============================================================================

func TestDocumentMeta_Struct(t *testing.T) {
	meta := rag.DocumentMeta{
		FileID:     "file-001",
		FileName:   "test.md",
		ChunkCount: 10,
	}

	if meta.FileID != "file-001" {
		t.Errorf("Expected FileID 'file-001', got '%s'", meta.FileID)
	}
	if meta.FileName != "test.md" {
		t.Errorf("Expected FileName 'test.md', got '%s'", meta.FileName)
	}
	if meta.ChunkCount != 10 {
		t.Errorf("Expected ChunkCount 10, got %d", meta.ChunkCount)
	}
}

// =============================================================================
// RetrievalResult ParentChunkID Tests
// =============================================================================

func TestRetrievalResult_ParentChunkID(t *testing.T) {
	result := rag.RetrievalResult{
		ChunkID:       "child-001",
		ParentChunkID: "parent-001",
		FileID:        "file-001",
		FileName:      "test.md",
		Content:       "Test content",
	}

	if result.ParentChunkID != "parent-001" {
		t.Errorf("Expected ParentChunkID 'parent-001', got '%s'", result.ParentChunkID)
	}
}

func TestRetrievalResult_ParentChunkID_Empty(t *testing.T) {
	// Non-PC mode result should have empty ParentChunkID
	result := rag.RetrievalResult{
		ChunkID:  "chunk-001",
		FileID:   "file-001",
		FileName: "test.md",
		Content:  "Test content",
	}

	if result.ParentChunkID != "" {
		t.Errorf("Expected empty ParentChunkID for non-PC result, got '%s'", result.ParentChunkID)
	}
}

// =============================================================================
// Migration Schema Tests — parent_chunk_id in Schema
// =============================================================================

func TestMigrationSchema_HasParentChunkID(t *testing.T) {
	retCfg := rag.DefaultRetrieverConfig()
	migCfg := rag.DefaultMigrationConfig()

	// We need a minimal setup to build schema
	// Create migrator with nil store/redis since we only need BuildDesiredSchema
	migrator := rag.NewMigrator(nil, nil, retCfg, migCfg)

	schema := migrator.BuildDesiredSchema()

	found := false
	for _, field := range schema.Fields {
		if field.Name == "parent_chunk_id" && field.Type == "TAG" {
			found = true
			break
		}
	}

	if !found {
		t.Error("BuildDesiredSchema should include 'parent_chunk_id TAG' field")
	}

	// Verify all expected fields
	expectedFields := map[string]string{
		"content":         "TEXT",
		"file_id":         "TAG",
		"file_name":       "TEXT",
		"chunk_id":        "TAG",
		"chunk_index":     "NUMERIC",
		"parent_chunk_id": "TAG",
		"vector":          "VECTOR",
	}

	for _, field := range schema.Fields {
		if expectedType, ok := expectedFields[field.Name]; ok {
			if field.Type != expectedType {
				t.Errorf("Field %s: expected type %s, got %s", field.Name, expectedType, field.Type)
			}
			delete(expectedFields, field.Name)
		}
	}

	for name := range expectedFields {
		t.Errorf("Missing expected field in schema: %s", name)
	}
}

func TestMigrationSchema_Version(t *testing.T) {
	if rag.SchemaVersion < 2 {
		t.Errorf("SchemaVersion should be >= 2, got %d", rag.SchemaVersion)
	}
}

// =============================================================================
// JSON Serialization Tests for New Types
// =============================================================================

func TestDocumentMeta_JSON(t *testing.T) {
	meta := rag.DocumentMeta{
		FileID:     "doc-001",
		FileName:   "architecture.md",
		ChunkCount: 15,
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	jsonStr := string(data)
	t.Logf("DocumentMeta JSON: %s", jsonStr)

	if !strings.Contains(jsonStr, `"file_id":"doc-001"`) {
		t.Error("JSON should contain file_id")
	}
	if !strings.Contains(jsonStr, `"file_name":"architecture.md"`) {
		t.Error("JSON should contain file_name")
	}
	if !strings.Contains(jsonStr, `"chunk_count":15`) {
		t.Error("JSON should contain chunk_count")
	}

	// Unmarshal back
	var decoded rag.DocumentMeta
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if decoded.FileID != meta.FileID || decoded.FileName != meta.FileName || decoded.ChunkCount != meta.ChunkCount {
		t.Error("Round-trip JSON serialization mismatch")
	}
}

func TestRetrievalResult_JSON_WithParentChunkID(t *testing.T) {
	result := rag.RetrievalResult{
		ChunkID:        "child-001",
		ParentChunkID:  "parent-001",
		FileID:         "file-001",
		FileName:       "test.md",
		ChunkIndex:     3,
		Content:        "Some content",
		RelevanceScore: 0.95,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	jsonStr := string(data)
	t.Logf("RetrievalResult JSON: %s", jsonStr)

	if !strings.Contains(jsonStr, `"parent_chunk_id":"parent-001"`) {
		t.Error("JSON should contain parent_chunk_id")
	}
}

func TestRetrievalResult_JSON_OmitParentChunkID(t *testing.T) {
	// ParentChunkID should be omitempty
	result := rag.RetrievalResult{
		ChunkID:        "chunk-001",
		FileID:         "file-001",
		FileName:       "test.md",
		Content:        "Some content",
		RelevanceScore: 0.85,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	jsonStr := string(data)
	if strings.Contains(jsonStr, "parent_chunk_id") {
		t.Errorf("JSON should omit parent_chunk_id when empty, got: %s", jsonStr)
	}
}

// =============================================================================
// Chunk Struct New Fields Tests
// =============================================================================

func TestChunkStruct_NewFields(t *testing.T) {
	chunk := rag.Chunk{
		ChunkID:          "child-123",
		ParentChunkID:    "parent-456",
		Content:          "This is the parent content block",
		EmbeddingContent: "This is child text for embedding",
		ChunkIndex:       0,
		StartPos:         0,
		EndPos:           100,
		TokenCount:       25,
	}

	if chunk.ParentChunkID != "parent-456" {
		t.Errorf("Expected ParentChunkID 'parent-456', got '%s'", chunk.ParentChunkID)
	}
	if chunk.EmbeddingContent != "This is child text for embedding" {
		t.Errorf("EmbeddingContent mismatch")
	}
	if chunk.Content != "This is the parent content block" {
		t.Errorf("Content mismatch")
	}
}

// =============================================================================
// RetrieverConfig HyDE Fields Tests
// =============================================================================

func TestRetrieverConfig_HyDEFields(t *testing.T) {
	hydeConfig := &rag.HyDEConfig{
		BaseURL:     "https://api.openai.com/v1",
		APIKey:      "sk-test",
		Model:       "gpt-3.5-turbo",
		MaxTokens:   256,
		Temperature: 0.3,
	}

	retCfg := rag.DefaultRetrieverConfig()
	retCfg.HyDEEnabled = true
	retCfg.HyDEConfig = hydeConfig

	if !retCfg.HyDEEnabled {
		t.Error("HyDEEnabled should be true")
	}
	if retCfg.HyDEConfig == nil {
		t.Fatal("HyDEConfig should not be nil")
	}
	if retCfg.HyDEConfig.Model != "gpt-3.5-turbo" {
		t.Errorf("Expected model 'gpt-3.5-turbo', got '%s'", retCfg.HyDEConfig.Model)
	}
}
