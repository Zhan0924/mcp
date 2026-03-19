/*
┌──────────────────────────────────────────────────────────────────────────────┐
│ prompt.go — RAG Prompt 构建器                                                │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标:                                                                       │
│  - 将检索结果组织成可直接输入 LLM 的上下文提示词                             │
│  - 按文件分组，避免模型误以为同一文件的多个 chunk 来自不同来源              │
│                                                                              │
│ 结构:                                                                       │
│  - BuildMultiFileRAGPrompt(): 输入 query + 检索结果 → 输出 prompt 文本        │
└──────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"fmt"
	"strings"
)

// BuildMultiFileRAGPrompt 构建多文件 RAG 提示词
// 按文件分组展示，避免同一文件的多个分块被误解为多个文件
func BuildMultiFileRAGPrompt(query string, results []RetrievalResult) string {
	if len(results) == 0 {
		return query
	}

	// 以 file_id 分组，保证上下文展示与真实文件边界一致
	fileGroups := make(map[string][]RetrievalResult)
	fileOrder := make([]string, 0)

	for _, r := range results {
		key := r.FileID
		if _, exists := fileGroups[key]; !exists {
			fileOrder = append(fileOrder, key)
		}
		fileGroups[key] = append(fileGroups[key], r)
	}

	var sb strings.Builder
	sb.WriteString("基于以下参考文档回答用户的问题。如果文档中没有相关信息，请说明无法找到相关信息。\n\n")

	for i, fileID := range fileOrder {
		chunks := fileGroups[fileID]
		fileName := chunks[0].FileName
		if fileName == "" {
			fileName = fileID
		}

		sb.WriteString(fmt.Sprintf("=== 文件 %d: %s ===\n", i+1, fileName))

		for _, chunk := range chunks {
			sb.WriteString(fmt.Sprintf("[片段 %d] (相关度: %.1f%%)\n%s\n\n",
				chunk.ChunkIndex+1,
				chunk.RelevanceScore*100,
				chunk.Content))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("用户问题：%s\n\n请提供准确、完整的回答：", query))

	return sb.String()
}
