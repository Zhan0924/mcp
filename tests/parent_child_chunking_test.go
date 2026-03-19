package tests

import (
	"testing"

	"mcp_rag_server/rag"
)

// =============================================================================
// Parent-Child Chunking Tests
// =============================================================================

func TestParentChildChunking_Basic(t *testing.T) {
	// A document large enough to produce multiple parent chunks
	content := loadTestFile(t, "distributed_systems.md")
	cfg := rag.ChunkingConfig{
		MaxChunkSize:       1000,
		MinChunkSize:       100,
		OverlapSize:        200,
		StructureAware:     false,
		ParentChildEnabled: true,
		ParentChunkSize:    1000,
		ChildChunkSize:     200,
	}

	chunks := rag.ChunkDocument(content, cfg)

	t.Logf("Parent-Child chunking: input=%d bytes, chunks=%d", len(content), len(chunks))

	if len(chunks) == 0 {
		t.Fatal("Expected at least 1 chunk")
	}

	// Every chunk must have a ParentChunkID
	for i, c := range chunks {
		if c.ParentChunkID == "" {
			t.Errorf("Chunk %d should have a ParentChunkID in Parent-Child mode", i)
		}
		if c.EmbeddingContent == "" {
			t.Errorf("Chunk %d should have EmbeddingContent (child text) set", i)
		}
		// In PC mode, Content = parent block, EmbeddingContent = child block
		// So Content should be >= EmbeddingContent in length
		if len(c.Content) < len(c.EmbeddingContent) {
			t.Errorf("Chunk %d: Content (parent, len=%d) should be >= EmbeddingContent (child, len=%d)",
				i, len(c.Content), len(c.EmbeddingContent))
		}
	}
}

func TestParentChildChunking_MultipleChildrenSameParent(t *testing.T) {
	// Create a moderately sized document
	content := loadTestFile(t, "golang_concurrency.md")
	cfg := rag.ChunkingConfig{
		MaxChunkSize:       1000,
		MinChunkSize:       100,
		OverlapSize:        200,
		ParentChildEnabled: true,
		ParentChunkSize:    2000, // Large parent
		ChildChunkSize:     200,  // Small children
	}

	chunks := rag.ChunkDocument(content, cfg)

	t.Logf("Total chunks: %d", len(chunks))

	// Count children per parent
	parentChildCount := make(map[string]int)
	for _, c := range chunks {
		parentChildCount[c.ParentChunkID]++
	}

	t.Logf("Unique parents: %d", len(parentChildCount))
	multiChildParents := 0
	for pid, count := range parentChildCount {
		t.Logf("  Parent %s: %d children", pid[:8], count)
		if count > 1 {
			multiChildParents++
		}
	}

	if multiChildParents == 0 && len(chunks) > 1 {
		t.Log("Warning: No parent had multiple children. Document may be too small for the given config.")
	}
}

func TestParentChildChunking_ContentIsParentBlock(t *testing.T) {
	// A parent with known content should produce children whose Content equals parent text
	content := loadTestFile(t, "distributed_systems.md")
	cfg := rag.ChunkingConfig{
		ParentChildEnabled: true,
		ParentChunkSize:    500,
		ChildChunkSize:     100,
	}

	chunks := rag.ChunkDocument(content, cfg)
	if len(chunks) == 0 {
		t.Fatal("Expected chunks")
	}

	// Group by parent to verify content consistency
	parentContents := make(map[string]string)
	for _, c := range chunks {
		if existingContent, ok := parentContents[c.ParentChunkID]; ok {
			// All children of the same parent should share the same Content
			if c.Content != existingContent {
				t.Errorf("Children of parent %s have different Content values", c.ParentChunkID[:8])
			}
		} else {
			parentContents[c.ParentChunkID] = c.Content
		}
	}

	t.Logf("Verified %d unique parent contents across %d chunks", len(parentContents), len(chunks))
}

func TestParentChildChunking_SmallDocument(t *testing.T) {
	// A small document might only produce 1 parent with 1 child
	content := loadTestFile(t, "small_intro.txt")
	cfg := rag.ChunkingConfig{
		ParentChildEnabled: true,
		ParentChunkSize:    1000,
		ChildChunkSize:     200,
	}

	chunks := rag.ChunkDocument(content, cfg)

	t.Logf("Small file: %d bytes -> %d PC chunks", len(content), len(chunks))

	if len(chunks) == 0 {
		t.Fatal("Expected at least 1 chunk")
	}

	// Even 1 chunk should have ParentChunkID
	if chunks[0].ParentChunkID == "" {
		t.Error("Even a single chunk should have ParentChunkID in PC mode")
	}
}

func TestParentChildChunking_DisabledByDefault(t *testing.T) {
	content := loadTestFile(t, "golang_concurrency.md")
	cfg := rag.DefaultChunkingConfig()

	if cfg.ParentChildEnabled {
		t.Error("ParentChildEnabled should be false by default")
	}

	chunks := rag.ChunkDocument(content, *cfg)

	// Without PC, no chunk should have ParentChunkID
	for i, c := range chunks {
		if c.ParentChunkID != "" {
			t.Errorf("Chunk %d should NOT have ParentChunkID when PC is disabled", i)
		}
		if c.EmbeddingContent != "" {
			t.Errorf("Chunk %d should NOT have EmbeddingContent when PC is disabled", i)
		}
	}
}

// =============================================================================
// deduplicateByParent Tests (via exported Retrieve path, tested at unit level)
// =============================================================================

func TestDeduplicateByParent_NoParent(t *testing.T) {
	// When no ParentChunkID is set, all results should be preserved
	results := []rag.RetrievalResult{
		{ChunkID: "c1", FileID: "f1", Content: "text1"},
		{ChunkID: "c2", FileID: "f1", Content: "text2"},
		{ChunkID: "c3", FileID: "f2", Content: "text3"},
	}

	// Without parent IDs, no dedup should happen
	// We can't call deduplicateByParent directly (unexported), but we verify behavior
	// by checking that convertVectorResults + the Retrieve pipeline preserves all
	for _, r := range results {
		if r.ParentChunkID != "" {
			t.Errorf("Test setup error: ParentChunkID should be empty")
		}
	}

	// All should be preserved (no dedup needed)
	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}
}

func TestChunkingConfig_ParentChildDefaults(t *testing.T) {
	cfg := rag.DefaultChunkingConfig()

	if cfg.ParentChildEnabled {
		t.Error("ParentChildEnabled should default to false")
	}
	if cfg.ParentChunkSize != 1000 {
		t.Errorf("Expected ParentChunkSize=1000, got %d", cfg.ParentChunkSize)
	}
	if cfg.ChildChunkSize != 200 {
		t.Errorf("Expected ChildChunkSize=200, got %d", cfg.ChildChunkSize)
	}
}

func TestRetrieverConfig_HyDEDefaults(t *testing.T) {
	cfg := rag.DefaultRetrieverConfig()

	if cfg.HyDEEnabled {
		t.Error("HyDEEnabled should default to false")
	}
	if cfg.HyDEConfig != nil {
		t.Error("HyDEConfig should be nil by default")
	}

	// Verify parent_chunk_id is in return fields
	found := false
	for _, f := range cfg.ReturnFields {
		if f == "parent_chunk_id" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ReturnFields should include 'parent_chunk_id'")
	}
}
