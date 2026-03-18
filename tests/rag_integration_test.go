package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcp_rag_server/rag"

	redisCli "github.com/redis/go-redis/v9"
)

const (
	testUserID        = uint(99999)
	testRedisAddr     = "localhost:6379"
	testRedisPassword = "123456"
	testRedisDB       = 0
	testIndexPrefix   = "mcp_rag_test_user_%d:"
	testIndexName     = "mcp_rag_test_user_%d:idx"
)

func getTestRetrieverConfig() *rag.RetrieverConfig {
	return &rag.RetrieverConfig{
		UserIndexNameTemplate:   testIndexName,
		UserIndexPrefixTemplate: testIndexPrefix,
		VectorFieldName:         "vector",
		ReturnFields:            []string{"content", "file_id", "file_name", "chunk_id", "chunk_index", "distance"},
		SearchDialect:           2,
		DefaultTopK:             5,
		MaxTopK:                 20,
		MinScore:                0.0,
		IndexAlgorithm:          "FLAT",
		EmbeddingBatchSize:      10,
		PipelineBatchSize:       500,
	}
}

func getTestChunkingConfig() *rag.ChunkingConfig {
	return &rag.ChunkingConfig{
		MaxChunkSize:   1000,
		MinChunkSize:   100,
		OverlapSize:    200,
		StructureAware: true,
	}
}

func setupRedis(t *testing.T) redisCli.UniversalClient {
	t.Helper()
	client := redisCli.NewClient(&redisCli.Options{
		Addr:     testRedisAddr,
		Password: testRedisPassword,
		DB:       testRedisDB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available at %s: %v", testRedisAddr, err)
	}
	return client
}

func cleanupTestIndex(t *testing.T, client redisCli.UniversalClient) {
	t.Helper()
	ctx := context.Background()
	indexName := fmt.Sprintf(testIndexName, testUserID)
	prefix := fmt.Sprintf(testIndexPrefix, testUserID)

	client.Do(ctx, "FT.DROPINDEX", indexName, "DD").Err()

	iter := client.Scan(ctx, 0, prefix+"*", 1000).Iterator()
	for iter.Next(ctx) {
		client.Del(ctx, iter.Val())
	}
}

func setupEmbeddingManager(t *testing.T) {
	t.Helper()

	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		apiKey = "sk-4c99e8a441694ad3ad441be9b8460f6d"
	}

	managerCfg := rag.DefaultManagerConfig()
	manager := rag.InitGlobalManager(managerCfg)

	providerCfg := rag.ProviderConfig{
		Name:      "test-dashscope",
		Type:      "openai",
		BaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
		APIKey:    apiKey,
		Model:     "text-embedding-v4",
		Dimension: 1024,
		Priority:  1,
		Weight:    100,
		Timeout:   30 * time.Second,
		Enabled:   true,
	}

	if err := manager.AddProvider(context.Background(), providerCfg); err != nil {
		t.Fatalf("Failed to add embedding provider: %v", err)
	}
	manager.Start()

	t.Cleanup(func() {
		manager.Stop()
	})
}

func loadTestFile(t *testing.T, filename string) string {
	t.Helper()
	path := filepath.Join("..", "testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read test file %s: %v", filename, err)
	}
	return string(data)
}

// --- 测试 1: 分块功能（不依赖 Redis / Embedding） ---

func TestChunkDocument_SmallFile(t *testing.T) {
	content := loadTestFile(t, "small_intro.txt")
	cfg := *getTestChunkingConfig()

	chunks := rag.ChunkDocument(content, cfg)

	t.Logf("Small file: %d bytes -> %d chunks", len(content), len(chunks))
	if len(chunks) != 1 {
		t.Errorf("Expected 1 chunk for small file, got %d", len(chunks))
	}
	if chunks[0].Content == "" {
		t.Error("Chunk content is empty")
	}
}

func TestChunkDocument_MediumFile(t *testing.T) {
	content := loadTestFile(t, "golang_concurrency.md")
	cfg := *getTestChunkingConfig()

	chunks := rag.ChunkDocument(content, cfg)

	t.Logf("Medium file: %d bytes -> %d chunks", len(content), len(chunks))
	if len(chunks) < 2 {
		t.Errorf("Expected multiple chunks for medium file (%d bytes), got %d", len(content), len(chunks))
	}

	for i, c := range chunks {
		t.Logf("  Chunk %d: %d chars, tokens~%d, pos=[%d:%d]",
			i, len(c.Content), c.TokenCount, c.StartPos, c.EndPos)
		if c.ChunkID == "" {
			t.Errorf("Chunk %d has empty ChunkID", i)
		}
		if c.Content == "" {
			t.Errorf("Chunk %d has empty content", i)
		}
	}
}

func TestChunkDocument_LargeFile(t *testing.T) {
	content := loadTestFile(t, "distributed_systems.md")
	cfg := *getTestChunkingConfig()

	chunks := rag.ChunkDocument(content, cfg)

	t.Logf("Large file: %d bytes -> %d chunks", len(content), len(chunks))
	if len(chunks) < 3 {
		t.Errorf("Expected many chunks for large file (%d bytes), got %d", len(content), len(chunks))
	}

	for i, c := range chunks {
		if len([]rune(c.Content)) > cfg.MaxChunkSize+cfg.OverlapSize+100 {
			t.Errorf("Chunk %d exceeds expected max size: %d runes", i, len([]rune(c.Content)))
		}
	}
}

func TestChunkDocument_AllTestFiles(t *testing.T) {
	files := []string{"small_intro.txt", "golang_concurrency.md", "distributed_systems.md", "kubernetes_guide.md"}
	cfg := *getTestChunkingConfig()

	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			content := loadTestFile(t, f)
			chunks := rag.ChunkDocument(content, cfg)
			t.Logf("File=%s  Size=%d bytes  Chunks=%d", f, len(content), len(chunks))
			if len(chunks) == 0 {
				t.Error("Got 0 chunks")
			}
		})
	}
}

// --- 测试 2: Prompt 构建 ---

func TestBuildPrompt_WithResults(t *testing.T) {
	results := []rag.RetrievalResult{
		{ChunkID: "c1", FileID: "f1", FileName: "doc1.md", ChunkIndex: 0, Content: "Go 语言支持 goroutine 并发", RelevanceScore: 0.95},
		{ChunkID: "c2", FileID: "f1", FileName: "doc1.md", ChunkIndex: 1, Content: "Channel 用于 goroutine 间通信", RelevanceScore: 0.88},
		{ChunkID: "c3", FileID: "f2", FileName: "doc2.md", ChunkIndex: 0, Content: "Kubernetes 使用 Pod 作为最小部署单元", RelevanceScore: 0.72},
	}

	prompt := rag.BuildMultiFileRAGPrompt("Go 并发编程", results)

	t.Logf("Prompt length: %d chars", len(prompt))
	t.Logf("Prompt preview:\n%s", truncate(prompt, 500))

	if !strings.Contains(prompt, "Go 并发编程") {
		t.Error("Prompt should contain the query")
	}
	if !strings.Contains(prompt, "goroutine") {
		t.Error("Prompt should contain retrieved content")
	}
	if !strings.Contains(prompt, "doc1.md") {
		t.Error("Prompt should contain file names")
	}
}

func TestBuildPrompt_Empty(t *testing.T) {
	prompt := rag.BuildMultiFileRAGPrompt("test query", nil)
	if prompt != "test query" {
		t.Errorf("Empty results should return raw query, got: %s", prompt)
	}
}

// --- 测试 3: 端到端集成（需要 Redis + Embedding API） ---

func TestIntegration_IndexAndSearch_SmallFile(t *testing.T) {
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

	content := loadTestFile(t, "small_intro.txt")
	result, err := retriever.IndexDocument(ctx, "small-001", "small_intro.txt", content)
	if err != nil {
		t.Fatalf("IndexDocument failed: %v", err)
	}
	t.Logf("Indexed: total=%d, indexed=%d, failed=%d", result.TotalChunks, result.Indexed, result.Failed)

	if result.TotalChunks == 0 || result.Indexed == 0 {
		t.Fatal("No chunks indexed")
	}
	if result.Failed > 0 {
		t.Errorf("Some chunks failed to index: %d", result.Failed)
	}

	time.Sleep(500 * time.Millisecond)

	retriever.SetTopK(3)
	results, err := retriever.Retrieve(ctx, "Go 语言特性", nil)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	t.Logf("Search results: %d", len(results))
	for i, r := range results {
		t.Logf("  [%d] file=%s score=%.4f content=%s", i, r.FileName, r.RelevanceScore, truncate(r.Content, 80))
	}

	if len(results) == 0 {
		t.Fatal("Expected at least 1 result")
	}
	if results[0].FileName != "small_intro.txt" {
		t.Errorf("Expected file_name=small_intro.txt, got %s", results[0].FileName)
	}
}

func TestIntegration_IndexAndSearch_LargeFile(t *testing.T) {
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

	content := loadTestFile(t, "distributed_systems.md")
	result, err := retriever.IndexDocument(ctx, "dist-001", "distributed_systems.md", content)
	if err != nil {
		t.Fatalf("IndexDocument failed: %v", err)
	}
	t.Logf("Indexed distributed_systems.md: total=%d, indexed=%d, failed=%d",
		result.TotalChunks, result.Indexed, result.Failed)

	if result.TotalChunks < 3 {
		t.Errorf("Expected multiple chunks for large file, got %d", result.TotalChunks)
	}
	if result.Failed > 0 {
		t.Errorf("Some chunks failed: %d", result.Failed)
	}

	time.Sleep(500 * time.Millisecond)

	testQueries := []struct {
		query    string
		expectIn string
	}{
		{"CAP 定理是什么", "一致性"},
		{"Raft 算法的原理", "Raft"},
		{"分布式事务有哪些方案", "事务"},
		{"限流算法对比", "限流"},
		{"消息队列的传递语义", "消息"},
	}

	for _, tc := range testQueries {
		t.Run(tc.query, func(t *testing.T) {
			retriever.SetTopK(3)
			results, err := retriever.Retrieve(ctx, tc.query, nil)
			if err != nil {
				t.Fatalf("Retrieve failed: %v", err)
			}

			t.Logf("Query: %s -> %d results", tc.query, len(results))
			for i, r := range results {
				t.Logf("  [%d] score=%.4f chunk=%d content=%s",
					i, r.RelevanceScore, r.ChunkIndex, truncate(r.Content, 100))
			}

			if len(results) == 0 {
				t.Error("Expected at least 1 result")
				return
			}

			found := false
			for _, r := range results {
				if strings.Contains(r.Content, tc.expectIn) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected results to contain '%s'", tc.expectIn)
			}
		})
	}
}

func TestIntegration_MultiFile_CrossSearch(t *testing.T) {
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

	files := []struct {
		id       string
		name     string
		filename string
	}{
		{"go-concurrency", "golang_concurrency.md", "golang_concurrency.md"},
		{"dist-systems", "distributed_systems.md", "distributed_systems.md"},
		{"k8s-guide", "kubernetes_guide.md", "kubernetes_guide.md"},
	}

	for _, f := range files {
		content := loadTestFile(t, f.filename)
		result, err := retriever.IndexDocument(ctx, f.id, f.name, content)
		if err != nil {
			t.Fatalf("Failed to index %s: %v", f.name, err)
		}
		t.Logf("Indexed %s: chunks=%d, indexed=%d, failed=%d",
			f.name, result.TotalChunks, result.Indexed, result.Failed)
	}

	time.Sleep(time.Second)

	t.Run("Cross-file: goroutine", func(t *testing.T) {
		retriever.SetTopK(5)
		results, err := retriever.Retrieve(ctx, "goroutine 并发和 channel 通信", nil)
		if err != nil {
			t.Fatalf("Retrieve failed: %v", err)
		}
		t.Logf("Found %d results for 'goroutine'", len(results))
		logResults(t, results)

		if len(results) == 0 {
			t.Error("Expected results for goroutine query")
		}
	})

	t.Run("Cross-file: Kubernetes Pod", func(t *testing.T) {
		retriever.SetTopK(5)
		results, err := retriever.Retrieve(ctx, "Kubernetes Pod 调度策略", nil)
		if err != nil {
			t.Fatalf("Retrieve failed: %v", err)
		}
		t.Logf("Found %d results for 'K8s Pod'", len(results))
		logResults(t, results)
	})

	t.Run("FileFilter: only k8s", func(t *testing.T) {
		retriever.SetTopK(5)
		results, err := retriever.Retrieve(ctx, "容器和 Pod", []string{"k8s-guide"})
		if err != nil {
			t.Fatalf("Retrieve failed: %v", err)
		}
		t.Logf("Found %d results (filtered to k8s-guide)", len(results))
		logResults(t, results)

		for _, r := range results {
			if r.FileID != "k8s-guide" {
				t.Errorf("Expected file_id=k8s-guide, got %s", r.FileID)
			}
		}
	})

	t.Run("BuildPrompt", func(t *testing.T) {
		retriever.SetTopK(5)
		results, err := retriever.Retrieve(ctx, "分布式系统中的一致性算法", nil)
		if err != nil {
			t.Fatalf("Retrieve failed: %v", err)
		}

		prompt := rag.BuildMultiFileRAGPrompt("分布式系统中的一致性算法", results)
		t.Logf("Prompt length: %d chars", len(prompt))
		t.Logf("Prompt:\n%s", truncate(prompt, 1000))

		if !strings.Contains(prompt, "一致性") {
			t.Error("Prompt should reference consistency")
		}
	})
}

func TestIntegration_IndexResult_JSON(t *testing.T) {
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

	content := loadTestFile(t, "kubernetes_guide.md")
	result, err := retriever.IndexDocument(ctx, "k8s-test", "kubernetes_guide.md", content)
	if err != nil {
		t.Fatalf("IndexDocument failed: %v", err)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	t.Logf("IndexResult JSON:\n%s", string(data))

	if result.FileID != "k8s-test" {
		t.Errorf("Expected file_id=k8s-test, got %s", result.FileID)
	}
	if result.FileName != "kubernetes_guide.md" {
		t.Errorf("Expected file_name=kubernetes_guide.md, got %s", result.FileName)
	}
	if result.TotalChunks == 0 {
		t.Error("Expected non-zero total_chunks")
	}
}

// --- Test: Rerank 端到端验证 ---

func TestIntegration_Rerank_EndToEnd(t *testing.T) {
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

	// 索引三份不同主题的文档
	files := []struct {
		id, name, filename string
	}{
		{"go-concurrency", "golang_concurrency.md", "golang_concurrency.md"},
		{"dist-systems", "distributed_systems.md", "distributed_systems.md"},
		{"k8s-guide", "kubernetes_guide.md", "kubernetes_guide.md"},
	}
	for _, f := range files {
		content := loadTestFile(t, f.filename)
		result, indexErr := retriever.IndexDocument(ctx, f.id, f.name, content)
		if indexErr != nil {
			t.Fatalf("Failed to index %s: %v", f.name, indexErr)
		}
		t.Logf("Indexed %s: %d chunks", f.name, result.TotalChunks)
	}
	time.Sleep(time.Second)

	// 初始化 DashScope Reranker
	rerankCfg := rag.RerankConfig{
		Enabled:    true,
		Provider:   "dashscope",
		APIKey:     "sk-4c99e8a441694ad3ad441be9b8460f6d",
		Model:      "qwen3-rerank",
		TopN:       5,
		RecallTopK: 20,
		Timeout:    15 * time.Second,
		Instruct:   "Given a web search query, retrieve relevant passages that answer the query.",
	}
	rag.InitGlobalReranker(rerankCfg)

	query := "Go 语言中 Goroutine 和 Channel 是如何实现并发通信的"

	// Step 1: 纯向量检索（多召回）
	recallTopK := 15
	retriever.SetTopK(recallTopK)
	vectorResults, err := retriever.Retrieve(ctx, query, nil)
	if err != nil {
		t.Fatalf("Vector retrieve failed: %v", err)
	}

	fmt.Println("====================================================")
	fmt.Printf("Query: %s\n", query)
	fmt.Printf("====================================================\n\n")

	fmt.Printf("--- 阶段 1: 纯向量检索 (top_%d) --- 共 %d 条结果\n\n", recallTopK, len(vectorResults))
	for i, r := range vectorResults {
		fmt.Printf("  [%d] score=%.4f  file=%-30s  chunk=%d\n      %s\n\n",
			i, r.RelevanceScore, r.FileName, r.ChunkIndex, truncate(r.Content, 80))
	}

	// Step 2: Rerank 重排序
	finalTopN := 5
	rerankedResults, err := rag.RerankResults(ctx, query, vectorResults, finalTopN)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	fmt.Printf("--- 阶段 2: Rerank 精排 (%d -> %d) ---\n\n", len(vectorResults), len(rerankedResults))
	for i, r := range rerankedResults {
		fmt.Printf("  [%d] rerank_score=%.4f  file=%-30s  chunk=%d\n      %s\n\n",
			i, r.RelevanceScore, r.FileName, r.ChunkIndex, truncate(r.Content, 80))
	}

	// Step 3: 对比验证
	fmt.Println("--- 阶段 3: 对比分析 ---")
	fmt.Println()

	if len(rerankedResults) == 0 {
		t.Fatal("Rerank returned no results")
	}
	if len(rerankedResults) > finalTopN {
		t.Errorf("Rerank should return at most %d results, got %d", finalTopN, len(rerankedResults))
	}

	// 验证 rerank 分数降序
	for i := 1; i < len(rerankedResults); i++ {
		if rerankedResults[i].RelevanceScore > rerankedResults[i-1].RelevanceScore {
			t.Errorf("Reranked results not sorted: [%d].score=%.4f > [%d].score=%.4f",
				i, rerankedResults[i].RelevanceScore, i-1, rerankedResults[i-1].RelevanceScore)
		}
	}

	// 验证 top 结果确实与 Go 并发相关
	goRelatedCount := 0
	for _, r := range rerankedResults {
		if r.FileID == "go-concurrency" {
			goRelatedCount++
		}
	}
	fmt.Printf("  Rerank Top-%d 中来自 go_concurrency.md 的结果: %d/%d\n", finalTopN, goRelatedCount, len(rerankedResults))

	if goRelatedCount == 0 {
		t.Error("Expected at least one result from go_concurrency.md in reranked top results")
	}

	// 验证排序变化 — rerank 是否改变了原始顺序
	orderChanged := false
	vectorTop5 := vectorResults
	if len(vectorTop5) > finalTopN {
		vectorTop5 = vectorTop5[:finalTopN]
	}
	for i := 0; i < len(rerankedResults) && i < len(vectorTop5); i++ {
		if rerankedResults[i].ChunkID != vectorTop5[i].ChunkID {
			orderChanged = true
			break
		}
	}

	if orderChanged {
		fmt.Println("  Rerank 改变了原始向量检索的排序顺序 (符合预期)")
	} else {
		fmt.Println("  Rerank 未改变排序顺序 (可能查询已经足够精准)")
	}

	fmt.Println("\n  Rerank 模型验证通过")
	fmt.Println("====================================================")
}

// --- Helper functions ---

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func logResults(t *testing.T, results []rag.RetrievalResult) {
	t.Helper()
	for i, r := range results {
		t.Logf("  [%d] file=%s(%s) chunk=%d score=%.4f content=%s",
			i, r.FileID, r.FileName, r.ChunkIndex, r.RelevanceScore, truncate(r.Content, 100))
	}
}
