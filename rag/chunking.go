/*
┌─────────────────────────────────────────────────────────────────┐
│                   chunking.go — 文档分块引擎                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  核心思想：滑动窗口 + 重叠区域，保证检索时不丢失块边界处的上下文        │
│                                                                 │
│  导出类型:                                                       │
│    Chunk           — 分块结果，包含内容、位置、Token 近似计数         │
│                                                                 │
│  导出函数:                                                       │
│    ChunkDocument(content, config) []Chunk                       │
│        对原始文本执行「递归字符分割 → 小块合并 → 重叠拼接」三阶段流水线  │
│                                                                 │
│  内部函数:                                                       │
│    splitText         — 递归分割：按分隔符优先级逐级尝试              │
│    splitByLength     — 硬切：当所有分隔符均不存在时按 rune 长度切割    │
│    mergeSmallChunks  — 合并过小碎片，防止产生低质量检索单元            │
│    addOverlap        — 在相邻块间注入重叠文本，保证检索连续性          │
│    estimateTokenCount— 基于 rune 的 Token 近似（中文/英文分别估算）   │
│                                                                 │
│  分块 ID 策略: UUID v4，在多租户环境下保证全局唯一                    │
│                                                                 │
│  下游关联:                                                       │
│    parser.go 的 StructureAwareChunk 利用标题层级作为自然切分点，     │
│    对超长章节再调用本模块的 ChunkDocument 进行二次分块                │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Chunk 文档分块结构
type Chunk struct {
	ChunkID          string // UUID v4，多租户环境下保证全局唯一
	ParentChunkID    string // UUID v4，仅在 ParentChildEnabled 时有值
	Content          string // 分块内容（存入DB的真实数据）
	EmbeddingContent string // 用于计算向量的内容（仅在 Parent-Child 模式下，等于子块内容）
	ChunkIndex       int    // 分块序号（从0开始）
	StartPos         int    // 在原文中的起始位置（字符偏移）
	EndPos           int    // 在原文中的结束位置（字符偏移）
	TokenCount       int    // Token 近似数量，用于下游 embedding 截断预判
}

// defaultSeparators 分隔符优先级列表（从粗粒度到细粒度）
// 递归分割时从前往后尝试，优先在段落边界切分以保留语义完整性
var defaultSeparators = []string{
	"\n\n", // 段落分隔（最优切分点，语义最完整）
	"\n",   // 换行
	"。",    // 中文句号
	".",    // 英文句号
	"！",    // 中文感叹号
	"!",    // 英文感叹号
	"？",    // 中文问号
	"?",    // 英文问号
	"；",    // 中文分号
	";",    // 英文分号
	" ",    // 空格（最后手段，仅在无其他分隔符时使用）
}

// ChunkDocument 对文档执行三阶段分块流水线:
//  1. splitText     — 递归字符分割，按分隔符优先级逐级拆分
//  2. mergeSmallChunks — 合并过小碎片，避免产生低于 MinChunkSize 的片段（太小会降低检索相关性）
//  3. addOverlap    — 在相邻块间注入重叠文本，保证块边界处的内容不会在检索中丢失
func ChunkDocument(content string, config ChunkingConfig) []Chunk {
	// 默认值选择依据：
	//   MaxChunkSize=1000 字符 ≈ 300~700 token，在主流 embedding 模型（8192 token 上限）内留有充足余量
	//   MinChunkSize=100：低于此阈值的碎片缺少足够上下文，会严重降低向量检索的语义区分度
	//   OverlapSize=200 ≈ MaxChunkSize 的 20%，经验上在上下文保持与存储开销之间取得较好平衡
	if config.MaxChunkSize <= 0 {
		config.MaxChunkSize = 1000
	}
	if config.MinChunkSize <= 0 {
		config.MinChunkSize = 100
	}
	if config.OverlapSize <= 0 {
		config.OverlapSize = 200
	}

	if config.ParentChildEnabled {
		return parentChildChunking(content, config)
	}

	// 短文本直接返回单块，避免不必要的分割开销
	if utf8.RuneCountInString(content) <= config.MaxChunkSize {
		return []Chunk{
			{
				ChunkID:    uuid.New().String(),
				Content:    content,
				ChunkIndex: 0,
				StartPos:   0,
				EndPos:     utf8.RuneCountInString(content),
				TokenCount: estimateTokenCount(content),
			},
		}
	}

	// 三阶段流水线
	rawChunks := splitText(content, defaultSeparators, config.MaxChunkSize)
	mergedChunks := mergeSmallChunks(rawChunks, config.MinChunkSize, config.MaxChunkSize)
	overlappedChunks := addOverlap(mergedChunks, config.OverlapSize)

	var chunks []Chunk
	currentPos := 0

	for i, text := range overlappedChunks {
		if currentPos >= len(content) {
			currentPos = len(content)
		}

		// 用块文本的前50字符首行作为搜索键，在原文中定位起始位置
		// 因为 addOverlap 会修改块内容，无法直接用偏移量推算位置
		// 截断到50字符: 足够唯一定位且避免在大文档中做长字符串搜索
		// 取首行: 重叠前缀可能引入前一块的换行内容，首行更可能命中原文连续片段
		searchKey := text
		if len(searchKey) > 50 {
			searchKey = searchKey[:50]
		}
		searchKey = strings.Split(searchKey, "\n")[0]

		var startPos int
		if searchKey == "" {
			startPos = currentPos
		} else {
			idx := strings.Index(content[currentPos:], searchKey)
			if idx == -1 {
				startPos = currentPos
			} else {
				startPos = currentPos + idx
			}
		}

		endPos := startPos + len(text)
		if endPos > len(content) {
			endPos = len(content)
		}

		chunk := Chunk{
			ChunkID:    uuid.New().String(),
			Content:    text,
			ChunkIndex: i,
			StartPos:   startPos,
			EndPos:     endPos,
			TokenCount: estimateTokenCount(text),
		}
		chunks = append(chunks, chunk)

		// 因为块间有重叠，下一块的搜索起点取当前块中点而非末尾
		// 这样避免重叠部分导致的位置定位偏移
		if i < len(overlappedChunks)-1 {
			step := len(text) / 2
			currentPos = startPos + step
		}
	}

	return chunks
}

// parentChildChunking 实现父子块分片策略
// 1. 根据 ParentChunkSize 进行大块切割（保留上下文）
// 2. 对每个父块再根据 ChildChunkSize 切割为小块
// 3. 将小块的 Content 设为父块的 Content，这样向量化针对子块（粒度细），但取出的文本是父块（上下文全）
func parentChildChunking(content string, config ChunkingConfig) []Chunk {
	parentConfig := config
	parentConfig.MaxChunkSize = config.ParentChunkSize
	parentConfig.MinChunkSize = config.ParentChunkSize / 5
	parentConfig.OverlapSize = config.ParentChunkSize / 5
	parentConfig.ParentChildEnabled = false // 防止无限递归

	parentChunks := ChunkDocument(content, parentConfig)

	childConfig := config
	childConfig.MaxChunkSize = config.ChildChunkSize
	childConfig.MinChunkSize = config.ChildChunkSize / 5
	childConfig.OverlapSize = config.ChildChunkSize / 5
	childConfig.ParentChildEnabled = false

	var finalChunks []Chunk
	childIdx := 0

	for _, pChunk := range parentChunks {
		children := ChunkDocument(pChunk.Content, childConfig)
		for _, cChunk := range children {
			// CRITICAL: 对于每个子块，它的 Embedding 计算文本是自己的短小内容，
			// 但我们希望最终存入 Redis 并通过 VectorSearch 返回的文本是父块的完整内容。
			cChunk.ParentChunkID = pChunk.ChunkID
			cChunk.ChunkIndex = childIdx
			cChunk.EmbeddingContent = cChunk.Content
			cChunk.Content = pChunk.Content

			finalChunks = append(finalChunks, cChunk)
			childIdx++
		}
	}

	return finalChunks
}

// splitText 递归字符分割：按分隔符优先级从粗到细逐级尝试
// 当某级分隔符存在时，先用它拆分，对超长子片段递归使用下一级分隔符
// 当所有分隔符都不存在时，退化为按 rune 长度硬切（splitByLength）
func splitText(text string, separators []string, maxSize int) []string {
	if utf8.RuneCountInString(text) <= maxSize {
		return []string{text}
	}

	for _, sep := range separators {
		if strings.Contains(text, sep) {
			parts := strings.Split(text, sep)
			var result []string

			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}

				if utf8.RuneCountInString(part) <= maxSize {
					result = append(result, part)
				} else {
					// 当前分隔符切出的片段仍超长，用更细粒度的分隔符继续递归
					subParts := splitText(part, separators[1:], maxSize)
					result = append(result, subParts...)
				}
			}

			return result
		}
	}

	// 所有分隔符均不存在（如连续无标点长文本），按 rune 长度硬切
	return splitByLength(text, maxSize)
}

// splitByLength 按 rune 长度硬切，保证不在多字节字符中间断开
func splitByLength(text string, maxSize int) []string {
	runes := []rune(text)
	var result []string

	for i := 0; i < len(runes); i += maxSize {
		end := i + maxSize
		if end > len(runes) {
			end = len(runes)
		}
		result = append(result, string(runes[i:end]))
	}

	return result
}

// mergeSmallChunks 合并过小碎片
// 过小的分块（< MinChunkSize）包含的上下文太少，会严重降低检索相关性
// 将相邻小块合并，直到合并后大小逼近 MaxChunkSize 或已达到最小阈值
func mergeSmallChunks(chunks []string, minSize, maxSize int) []string {
	if len(chunks) == 0 {
		return chunks
	}

	var result []string
	current := chunks[0]

	for i := 1; i < len(chunks); i++ {
		// 当前累积块尚未达到最小阈值，尝试与下一块合并
		if utf8.RuneCountInString(current) < minSize {
			merged := current + "\n" + chunks[i]
			if utf8.RuneCountInString(merged) <= maxSize {
				current = merged
				continue
			}
		}

		result = append(result, current)
		current = chunks[i]
	}

	// 处理尾部碎片：如果最后一块太小，尝试并入倒数第二块
	if current != "" {
		if len(result) > 0 && utf8.RuneCountInString(current) < minSize {
			lastIdx := len(result) - 1
			merged := result[lastIdx] + "\n" + current
			if utf8.RuneCountInString(merged) <= maxSize {
				result[lastIdx] = merged
			} else {
				result = append(result, current)
			}
		} else {
			result = append(result, current)
		}
	}

	return result
}

// addOverlap 在相邻块间注入重叠区域（滑动窗口核心机制）
// 重叠确保检索时不会丢失恰好落在块边界处的内容：
// 如果用户查询的关键信息被切割在两个块的交界处，重叠区域保证至少有一个块包含完整信息
func addOverlap(chunks []string, overlapSize int) []string {
	if len(chunks) <= 1 || overlapSize <= 0 {
		return chunks
	}

	var result []string

	for i, chunk := range chunks {
		if i == 0 {
			result = append(result, chunk)
			continue
		}

		// 从前一块尾部截取 overlapSize 个 rune 作为当前块的前缀
		prevChunk := chunks[i-1]
		prevRunes := []rune(prevChunk)

		var overlap string
		if len(prevRunes) > overlapSize {
			overlap = string(prevRunes[len(prevRunes)-overlapSize:])
		} else {
			overlap = prevChunk
		}

		result = append(result, overlap+"\n"+chunk)
	}

	return result
}

// estimateTokenCount 基于 rune 类型的 Token 近似估算
// 不调用 tokenizer 是因为分块阶段只需粗略值来预判 embedding 模型的输入上限，
// 精确 token 计数由 embedding 阶段完成。
// 近似规则：中文 ≈ 1.5 字符/token，英文 ≈ 4 字符/token（与 GPT 系列 tokenizer 统计接近）
func estimateTokenCount(text string) int {
	chineseCount := 0
	englishCount := 0

	for _, r := range text {
		// CJK 统一表意文字范围 (U+4E00 ~ U+9FFF)
		if r >= 0x4e00 && r <= 0x9fff {
			chineseCount++
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			englishCount++
		}
	}

	// +1 防止空文本返回 0（下游可能用作除数）
	return int(float64(chineseCount)/1.5) + int(float64(englishCount)/4) + 1
}
