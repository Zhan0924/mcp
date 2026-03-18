package tests

import (
	"context"
	"fmt"
	"mcp_rag_server/rag"
	"testing"
	"time"
)

func getRerankConfig() rag.RerankConfig {
	return rag.RerankConfig{
		Enabled:    true,
		Provider:   "dashscope",
		BaseURL:    "",
		APIKey:     "sk-4c99e8a441694ad3ad441be9b8460f6d",
		Model:      "qwen3-rerank",
		TopN:       3,
		RecallTopK: 20,
		Timeout:    15 * time.Second,
		Instruct:   "Given a web search query, retrieve relevant passages that answer the query.",
	}
}

func buildTestDocuments() []rag.RetrievalResult {
	return []rag.RetrievalResult{
		{ChunkID: "c1", FileID: "f1", FileName: "go_concurrency.md", ChunkIndex: 0, Content: "Go 语言的并发模型基于 CSP 理论，Goroutine 是轻量级线程，由 Go 运行时调度器管理", RelevanceScore: 0.80},
		{ChunkID: "c2", FileID: "f1", FileName: "go_concurrency.md", ChunkIndex: 1, Content: "Channel 是 Go 中 Goroutine 之间通信的核心机制，遵循不要通过共享内存来通信，而要通过通信来共享内存的原则", RelevanceScore: 0.75},
		{ChunkID: "c3", FileID: "f2", FileName: "distributed_systems.md", ChunkIndex: 0, Content: "CAP 定理指出分布式系统中一致性、可用性和分区容错性三者最多只能同时满足两个", RelevanceScore: 0.70},
		{ChunkID: "c4", FileID: "f2", FileName: "distributed_systems.md", ChunkIndex: 1, Content: "Raft 算法是比 Paxos 更易理解的一致性算法，将一致性问题分解为领导者选举、日志复制和安全性三个子问题", RelevanceScore: 0.65},
		{ChunkID: "c5", FileID: "f3", FileName: "kubernetes.md", ChunkIndex: 0, Content: "Kubernetes Pod 是最小部署单元，包含一个或多个容器，同一 Pod 内容器共享网络命名空间和存储卷", RelevanceScore: 0.60},
		{ChunkID: "c6", FileID: "f3", FileName: "kubernetes.md", ChunkIndex: 1, Content: "量子计算是计算科学的一个前沿领域，利用量子力学原理进行计算", RelevanceScore: 0.55},
	}
}

func TestDashScope_Qwen3Rerank(t *testing.T) {
	cfg := getRerankConfig()
	reranker := rag.NewDashScopeReranker(cfg)
	docs := buildTestDocuments()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	query := "什么是 Go 语言的并发模型和 Channel 通信机制"
	results, err := reranker.Rerank(ctx, query, docs, 3)
	if err != nil {
		t.Fatalf("qwen3-rerank failed: %v", err)
	}

	fmt.Println("=== qwen3-rerank 测试结果 ===")
	fmt.Printf("Query: %s\n", query)
	fmt.Printf("输入文档数: %d, 返回结果数: %d\n\n", len(docs), len(results))

	for i, r := range results {
		fmt.Printf("  [%d] score=%.4f file=%s chunk=%d\n      content: %s\n\n",
			i, r.RelevanceScore, r.FileName, r.ChunkIndex, truncContent(r.Content, 80))
	}

	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	if len(results) > 3 {
		t.Fatalf("expected at most 3 results, got %d", len(results))
	}

	// 最相关的结果应该包含 Go 并发或 Channel 相关内容
	topContent := results[0].Content
	if !containsAny(topContent, "Go", "Goroutine", "Channel", "CSP", "并发") {
		t.Errorf("top result should be related to Go concurrency, got: %s", truncContent(topContent, 100))
	}

	// 量子计算应该排在最后或被过滤掉
	for _, r := range results {
		if containsAny(r.Content, "量子计算") {
			t.Errorf("quantum computing doc should not rank high, found at score=%.4f", r.RelevanceScore)
		}
	}
}

func TestDashScope_GteRerankV2(t *testing.T) {
	cfg := getRerankConfig()
	cfg.Model = "gte-rerank-v2"
	cfg.BaseURL = ""
	reranker := rag.NewDashScopeReranker(cfg)
	docs := buildTestDocuments()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	query := "分布式系统中的一致性算法"
	results, err := reranker.Rerank(ctx, query, docs, 3)
	if err != nil {
		t.Fatalf("gte-rerank-v2 failed: %v", err)
	}

	fmt.Println("=== gte-rerank-v2 测试结果 ===")
	fmt.Printf("Query: %s\n", query)
	fmt.Printf("输入文档数: %d, 返回结果数: %d\n\n", len(docs), len(results))

	for i, r := range results {
		fmt.Printf("  [%d] score=%.4f file=%s chunk=%d\n      content: %s\n\n",
			i, r.RelevanceScore, r.FileName, r.ChunkIndex, truncContent(r.Content, 80))
	}

	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}

	topContent := results[0].Content
	if !containsAny(topContent, "CAP", "Raft", "Paxos", "一致性", "分布式") {
		t.Errorf("top result should be about distributed systems, got: %s", truncContent(topContent, 100))
	}
}

func TestDashScope_Rerank_ScoreOrdering(t *testing.T) {
	cfg := getRerankConfig()
	reranker := rag.NewDashScopeReranker(cfg)
	docs := buildTestDocuments()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results, err := reranker.Rerank(ctx, "Kubernetes Pod 和容器编排", docs, 5)
	if err != nil {
		t.Fatalf("rerank failed: %v", err)
	}

	fmt.Println("=== 分数递减顺序验证 ===")
	for i, r := range results {
		fmt.Printf("  [%d] score=%.4f  %s\n", i, r.RelevanceScore, truncContent(r.Content, 60))
	}

	for i := 1; i < len(results); i++ {
		if results[i].RelevanceScore > results[i-1].RelevanceScore {
			t.Errorf("results not sorted: [%d].score=%.4f > [%d].score=%.4f",
				i, results[i].RelevanceScore, i-1, results[i-1].RelevanceScore)
		}
	}
}

func TestDashScope_Rerank_EmptyInput(t *testing.T) {
	cfg := getRerankConfig()
	reranker := rag.NewDashScopeReranker(cfg)

	ctx := context.Background()
	results, err := reranker.Rerank(ctx, "test", nil, 5)
	if err != nil {
		t.Fatalf("empty input should not error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for nil input, got %d", len(results))
	}

	results, err = reranker.Rerank(ctx, "test", []rag.RetrievalResult{}, 5)
	if err != nil {
		t.Fatalf("empty slice should not error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty slice, got %d", len(results))
	}
}

func TestDashScope_Rerank_SingleDoc(t *testing.T) {
	cfg := getRerankConfig()
	reranker := rag.NewDashScopeReranker(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	docs := []rag.RetrievalResult{
		{ChunkID: "c1", FileID: "f1", FileName: "test.md", Content: "Go 语言是一门编译型、静态类型的编程语言"},
	}

	results, err := reranker.Rerank(ctx, "Go 语言", docs, 5)
	if err != nil {
		t.Fatalf("single doc rerank failed: %v", err)
	}

	fmt.Printf("=== 单文档 Rerank: score=%.4f ===\n", results[0].RelevanceScore)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].RelevanceScore <= 0 {
		t.Errorf("expected positive score, got %.4f", results[0].RelevanceScore)
	}
}

func TestDashScope_GlobalReranker_Integration(t *testing.T) {
	cfg := getRerankConfig()
	rag.InitGlobalReranker(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	docs := buildTestDocuments()
	results, err := rag.RerankResults(ctx, "Go Channel 并发通信", docs, 2)
	if err != nil {
		t.Fatalf("global reranker failed: %v", err)
	}

	fmt.Println("=== 全局 Reranker 测试 ===")
	for i, r := range results {
		fmt.Printf("  [%d] score=%.4f  %s\n", i, r.RelevanceScore, truncContent(r.Content, 60))
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestValidateRerankInput(t *testing.T) {
	if err := rag.ValidateRerankInput("qwen3-rerank", 500); err != nil {
		t.Errorf("500 docs should be valid for qwen3-rerank: %v", err)
	}
	if err := rag.ValidateRerankInput("qwen3-rerank", 501); err == nil {
		t.Error("501 docs should exceed qwen3-rerank limit")
	}
	if err := rag.ValidateRerankInput("gte-rerank-v2", 30000); err != nil {
		t.Errorf("30000 docs should be valid for gte-rerank-v2: %v", err)
	}
	if err := rag.ValidateRerankInput("qwen3-vl-rerank", 101); err == nil {
		t.Error("101 docs should exceed qwen3-vl-rerank limit")
	}
}

// --- helpers ---

func truncContent(s string, maxLen int) string {
	r := []rune(s)
	if len(r) > maxLen {
		return string(r[:maxLen]) + "..."
	}
	return s
}

func containsAny(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if len(kw) > 0 && contains(s, kw) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
