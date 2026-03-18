package tests

import (
	"context"
	"fmt"
	"mcp_rag_server/rag"
	"strings"
	"testing"
	"time"
)

func TestGitNexusDoc_ParseAndAnalyze(t *testing.T) {
	content := loadTestFile(t, "GitNexus—代码知识图谱.md")
	t.Logf("Document size: %d bytes", len(content))

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	t.Logf("Format: %s", doc.Format)
	t.Logf("Title: %s", doc.Metadata.Title)
	t.Logf("Sections: %d", len(doc.Sections))
	t.Logf("Tables: %d", doc.Metadata.TableCount)
	t.Logf("Images: %d", doc.Metadata.ImageCount)
	t.Logf("Words: %d, Chars: %d", doc.Metadata.WordCount, doc.Metadata.CharCount)

	// 验证表格提取
	if doc.Metadata.TableCount < 3 {
		t.Errorf("Expected at least 3 tables, got %d", doc.Metadata.TableCount)
	}

	for i, table := range doc.Tables {
		t.Logf("\n--- Table %d ---", i)
		t.Logf("  Context: %s", table.Context)
		t.Logf("  Headers: %v", table.Headers)
		t.Logf("  Rows: %d", len(table.Rows))
		t.Logf("  Linearized (first 200 chars):\n    %s", truncate(table.Linearized, 200))
	}

	// 验证第一个表格(核心特性)
	found := false
	for _, table := range doc.Tables {
		if containsInSlice(table.Headers, "特性") {
			found = true
			if len(table.Rows) < 2 {
				t.Errorf("核心特性 table should have >= 2 rows, got %d", len(table.Rows))
			}
			if !strings.Contains(table.Linearized, "CLI + MCP") {
				t.Error("Linearized should contain 'CLI + MCP'")
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find '核心特性' table")
	}

	// 验证常见问题排查表格
	found = false
	for _, table := range doc.Tables {
		if containsInSlice(table.Headers, "问题") && containsInSlice(table.Headers, "解决方案") {
			found = true
			if len(table.Rows) < 4 {
				t.Errorf("问题排查 table should have >= 4 rows, got %d", len(table.Rows))
			}
			break
		}
	}
	if !found {
		t.Error("Expected to find '问题/解决方案' table")
	}
}

func TestGitNexusDoc_StructureAwareChunking(t *testing.T) {
	content := loadTestFile(t, "GitNexus—代码知识图谱.md")

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	cfg := rag.ChunkingConfig{
		MaxChunkSize:   1000,
		MinChunkSize:   100,
		OverlapSize:    200,
		StructureAware: true,
	}

	chunks := rag.StructureAwareChunk(doc, cfg)
	t.Logf("Total chunks: %d", len(chunks))

	// 统计包含表格内容的 chunk
	tableChunks := 0
	for i, c := range chunks {
		if strings.Contains(c.Content, "[表格:") || strings.Contains(c.Content, "列: ") {
			tableChunks++
			t.Logf("  Table chunk %d (idx=%d, %d chars): %s",
				tableChunks, i, len(c.Content), truncate(c.Content, 120))
		}
	}

	t.Logf("Chunks containing tables: %d", tableChunks)

	if tableChunks == 0 {
		t.Error("Expected at least 1 chunk with linearized table content")
	}

	// 验证表格没有被拆分: 线性化表格的 key:value 行应该在同一个 chunk
	for _, c := range chunks {
		if strings.Contains(c.Content, "特性: **用途") {
			if !strings.Contains(c.Content, "特性: **场景") {
				t.Error("Table rows should stay together in one chunk")
			}
		}
	}
}

func TestGitNexusDoc_EnhancedContent(t *testing.T) {
	content := loadTestFile(t, "GitNexus—代码知识图谱.md")

	doc, err := rag.ParseDocument(content, rag.FormatMarkdown)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	enhanced := rag.EnhanceContentForEmbedding(doc)

	// 表格应被线性化
	if strings.Contains(enhanced, "|------") || strings.Contains(enhanced, "| -------- |") {
		t.Error("Enhanced content should not contain Markdown table separators")
	}

	// 线性化格式应存在
	if !strings.Contains(enhanced, "问题:") && !strings.Contains(enhanced, "特性:") {
		t.Error("Enhanced content should contain linearized table key:value pairs")
	}

	t.Logf("Enhanced content length: %d (original: %d)", len(enhanced), len(content))
}

func TestGitNexusDoc_IndexAndSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupRedis(t)
	defer client.Close()
	setupEmbeddingManager(t)

	cleanupTestIndex(t, client)
	t.Cleanup(func() { cleanupTestIndex(t, client) })

	ctx := context.Background()
	retCfg := getTestRetrieverConfig()
	chunkCfg := getTestChunkingConfig()
	store := rag.NewRedisVectorStore(client)

	retriever, err := rag.NewMultiFileRetriever(ctx, store, nil, retCfg, chunkCfg, testUserID)
	if err != nil {
		t.Fatalf("Failed to create retriever: %v", err)
	}

	content := loadTestFile(t, "GitNexus—代码知识图谱.md")
	result, err := retriever.IndexDocument(ctx, "gitnexus-doc", "GitNexus—代码知识图谱.md", content)
	if err != nil {
		t.Fatalf("IndexDocument failed: %v", err)
	}

	t.Logf("Indexed: chunks=%d, indexed=%d, failed=%d", result.TotalChunks, result.Indexed, result.Failed)
	if result.Indexed == 0 {
		t.Fatal("No chunks were indexed")
	}

	time.Sleep(time.Second)

	// 初始化 Reranker
	rerankCfg := rag.RerankConfig{
		Enabled:  true,
		Provider: "dashscope",
		APIKey:   "sk-4c99e8a441694ad3ad441be9b8460f6d",
		Model:    "qwen3-rerank",
		TopN:     5,
		Timeout:  15 * time.Second,
		Instruct: "Given a web search query, retrieve relevant passages that answer the query.",
	}
	rag.InitGlobalReranker(rerankCfg)

	queries := []struct {
		name  string
		query string
	}{
		{"表格相关: 核心特性", "GitNexus 的 CLI 和 Web UI 在用途和扩展性上有什么区别"},
		{"表格相关: 常见问题", "MCP 服务器未连接怎么办"},
		{"表格相关: 快速选择", "我要修复 Bug 应该用什么工具"},
		{"一般问题: 安装", "如何在 Cursor 中安装和配置 GitNexus"},
		{"一般问题: 工作原理", "GitNexus 的知识图谱索引是怎么工作的"},
		{"一般问题: 重构", "如何使用 GitNexus 安全地重构代码"},
	}

	for _, q := range queries {
		t.Run(q.name, func(t *testing.T) {
			// 纯向量检索
			retriever.SetTopK(10)
			vectorResults, err := retriever.Retrieve(ctx, q.query, nil)
			if err != nil {
				t.Fatalf("Vector retrieve failed: %v", err)
			}

			// Rerank
			reranked, err := rag.RerankResults(ctx, q.query, vectorResults, 3)
			if err != nil {
				t.Fatalf("Rerank failed: %v", err)
			}

			fmt.Printf("\n=== %s ===\n", q.name)
			fmt.Printf("Query: %s\n\n", q.query)

			fmt.Printf("向量检索 Top-3:\n")
			for i, r := range vectorResults {
				if i >= 3 {
					break
				}
				fmt.Printf("  [%d] score=%.4f  %s\n", i, r.RelevanceScore, truncate(r.Content, 80))
			}

			fmt.Printf("\nRerank Top-3:\n")
			for i, r := range reranked {
				fmt.Printf("  [%d] score=%.4f  %s\n", i, r.RelevanceScore, truncate(r.Content, 80))
			}

			if len(reranked) == 0 {
				t.Error("Expected reranked results")
			}
		})
	}
}

func containsInSlice(slice []string, target string) bool {
	for _, s := range slice {
		if strings.Contains(s, target) {
			return true
		}
	}
	return false
}
