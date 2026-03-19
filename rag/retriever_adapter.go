/*
┌──────────────────────────────────────────────────────────────────────────┐
│                        adapter.go 结构总览                               │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  JSONRetrieverAdapter         — 适配器：将 MultiFileRetriever 的         │
│                                  []RetrievalResult 转为 JSON 字符串，    │
│                                  供 MCP Tool 层以文本协议返回给 LLM      │
│                                                                          │
│  方法                                                                    │
│    NewJSONRetrieverAdapter()  — 构造适配器                               │
│    RetrieveJSON()             — 检索 → JSON 序列化                       │
│    Rerank()                   — 对已有 JSON 结果做重排序                  │
│    RetrieveAndRerank()        — 检索 + 重排序一体化（先扩大召回再精排）   │
│                                                                          │
│  设计要点                                                                │
│    适配器模式：MCP Tool 层只看到 JSON 字符串接口，与底层 Retriever        │
│    的具体实现解耦。Rerank 和 RetrieveAndRerank 在失败时优雅降级，        │
│    返回原始结果而非报错，保证调用方始终有结果可用。                        │
│                                                                          │
│    RetrieveAndRerank 采用「召回扩大」策略：先用 recallK > topK 多召回，  │
│    再通过 Reranker 精排筛选 topN，兼顾召回率和精确度。                   │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"encoding/json"

	"github.com/sirupsen/logrus"
)

// JSONRetrieverAdapter 适配器模式：将 MultiFileRetriever 的结构化检索结果
// 转换为 JSON 字符串格式，供 MCP Tool 层以文本协议返回给 LLM
type JSONRetrieverAdapter struct {
	r *MultiFileRetriever
}

// NewJSONRetrieverAdapter 构造适配器
func NewJSONRetrieverAdapter(r *MultiFileRetriever) *JSONRetrieverAdapter {
	return &JSONRetrieverAdapter{r: r}
}

// RetrieveJSON 检索并将结果序列化为 JSON 字符串
func (a *JSONRetrieverAdapter) RetrieveJSON(ctx context.Context, query string, topK int) (string, error) {
	a.r.SetTopK(topK)
	results, err := a.r.Retrieve(ctx, query, nil)
	if err != nil {
		return "", err
	}
	bytes, _ := json.MarshalIndent(results, "", "  ")
	return string(bytes), nil
}

// Rerank 对已有的 JSON 检索结果做重排序
// 优雅降级：解析失败或 Rerank 失败时返回原始 documents，而非报错
func (a *JSONRetrieverAdapter) Rerank(ctx context.Context, query string, documents string) (string, error) {
	var results []RetrievalResult
	if err := json.Unmarshal([]byte(documents), &results); err != nil {
		logrus.Warnf("[Adapter] Failed to parse documents for rerank, returning original: %v", err)
		return documents, nil
	}

	reranked, err := RerankResults(ctx, query, results, 0)
	if err != nil {
		logrus.Warnf("[Adapter] Rerank failed, returning original: %v", err)
		return documents, nil
	}

	out, _ := json.MarshalIndent(reranked, "", "  ")
	return string(out), nil
}

// RetrieveAndRerank 检索 + 重排序一体化
// 「召回扩大」策略：recallK = topK * 扩大系数（由 RerankConfig 控制），
// 先多召回以提高覆盖率，再用 Reranker 按语义相关性精排截取 topN
func (a *JSONRetrieverAdapter) RetrieveAndRerank(ctx context.Context, query string, topK int, rerankTopN int) ([]RetrievalResult, error) {
	recallK := GetEffectiveRecallTopK(DefaultRerankConfig(), topK)
	a.r.SetTopK(recallK)

	results, err := a.r.Retrieve(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	if rerankTopN <= 0 {
		rerankTopN = topK
	}

	return RerankResults(ctx, query, results, rerankTopN)
}
