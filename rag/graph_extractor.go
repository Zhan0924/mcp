/*
┌─────────────────────────────────────────────────────────────────────────────┐
│            llm_entity_extractor.go — 基于 LLM 的实体关系提取器               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  利用 LLM 的 Structured Output / Function Calling 能力，                     │
│  从非结构化文本中提取实体 (Entity) 和关系 (Relation) 三元组。                │
│                                                                             │
│  相比 SimpleEntityExtractor (正则规则):                                      │
│    - 支持任意语言、任意领域的实体识别                                         │
│    - 能理解隐含的语义关系（如 "Go 通过 goroutine 实现并发"                    │
│      → Entity(Go, Language), Entity(goroutine, Concept),                     │
│        Relation(Go, has_feature, goroutine))                                 │
│    - 准确率依赖 LLM 能力，成本较高，适合索引阶段离线调用                     │
│                                                                             │
│  导出类型:                                                                   │
│    LLMEntityExtractor         — EntityExtractor 接口的 LLM 实现             │
│    LLMExtractorConfig         — 提取器配置                                   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// LLMExtractorConfig LLM 实体提取器配置
type LLMExtractorConfig struct {
	BaseURL     string        `toml:"base_url"`
	APIKey      string        `toml:"api_key"`
	Model       string        `toml:"model"`
	MaxTokens   int           `toml:"max_tokens"`
	Temperature float64       `toml:"temperature"`
	Timeout     time.Duration `toml:"timeout"`
	MaxChunk    int           `toml:"max_chunk"` // 单次提取的最大文本长度
}

// DefaultLLMExtractorConfig 默认配置
func DefaultLLMExtractorConfig() LLMExtractorConfig {
	return LLMExtractorConfig{
		BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Model:       "qwen-turbo",
		MaxTokens:   2048,
		Temperature: 0.1, // 低温度保证输出稳定性
		Timeout:     30 * time.Second,
		MaxChunk:    4000, // 单次最多处理 4000 字符
	}
}

// LLMEntityExtractor 基于 LLM 的实体关系提取器
// 通过 Chat Completions API 调用 LLM，使用 JSON 模式提取结构化三元组
type LLMEntityExtractor struct {
	config     LLMExtractorConfig
	httpClient *http.Client
}

// NewLLMEntityExtractor 创建 LLM 实体提取器
func NewLLMEntityExtractor(cfg LLMExtractorConfig) *LLMEntityExtractor {
	if cfg.Model == "" {
		cfg.Model = "qwen-turbo"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 2048
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.3 // 默认 0.3，太低会导致模型过于保守不产出关系
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxChunk == 0 {
		cfg.MaxChunk = 4000
	}
	return &LLMEntityExtractor{
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

// extractionPrompt 实体关系提取的 System Prompt
// 设计要点:
//  1. 强制要求同时提取 entities 和 relations（旧版 prompt 过于保守导致 relations 常为空）
//  2. 提供 few-shot 示例降低 LLM 输出格式错误率
//  3. 放宽 "do not infer" 限制为 "extract explicitly stated or strongly implied"，
//     因为自然语言中很多关系是隐含的（如 "Go 通过 goroutine 实现并发" 隐含 uses 关系）
const extractionPrompt = `You are a knowledge graph extraction expert. Your task is to extract BOTH entities AND relationships from the given text. You MUST extract relationships — do not return an empty relations array if the text describes connections between concepts.

Entity types: Concept, Technology, Function, Class, Interface, Tool, Person, Organization, Protocol, Algorithm

Relationship types: uses, implements, extends, contains, depends_on, related_to, defines, calls, part_of, compared_with, based_on, created_by, provides, manages

Rules:
1. Extract entities that are key technical terms, named concepts, tools, people, or organizations
2. Extract relationships that are explicitly stated or strongly implied in the text
3. Entity names should be concise (1-4 words), use the most common/canonical form
4. Every relationship MUST reference entities that appear in your entities list
5. For each pair of related entities, choose the most specific relationship type
6. Return valid JSON only

Example input: "Go uses goroutine for lightweight concurrency. Channel provides communication between goroutines."
Example output:
{
  "entities": [
    {"name": "Go", "type": "Technology"},
    {"name": "goroutine", "type": "Concept"},
    {"name": "Channel", "type": "Concept"}
  ],
  "relations": [
    {"source": "Go", "target": "goroutine", "type": "uses"},
    {"source": "Channel", "target": "goroutine", "type": "related_to"}
  ]
}

Output format:
{
  "entities": [{"name": "...", "type": "..."}],
  "relations": [{"source": "...", "target": "...", "type": "..."}]
}`

// llmExtractionResult LLM 返回的提取结果
type llmExtractionResult struct {
	Entities  []llmEntity   `json:"entities"`
	Relations []llmRelation `json:"relations"`
}

type llmEntity struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type llmRelation struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// Extract 从文本中提取实体和关系
// 长文本自动分片处理，每片独立提取后合并去重。
// 片间传递上下文: 将前一片提取到的实体名列表注入到下一片的 user prompt 中，
// 使 LLM 能够识别跨片段的实体引用并建立关系。
func (e *LLMEntityExtractor) Extract(ctx context.Context, content string, fileID string) ([]Entity, []Relation, error) {
	// 将长文本分片，每片不超过 MaxChunk 字节
	chunks := splitForExtraction(content, e.config.MaxChunk)
	logrus.Infof("[LLMExtractor] Content length: %d bytes, MaxChunk: %d, split into %d chunks",
		len(content), e.config.MaxChunk, len(chunks))

	var allEntities []Entity
	var allRelations []Relation
	entitySet := make(map[string]bool)   // name+type 去重
	relationSet := make(map[string]bool) // source+target+type 去重
	var knownEntityNames []string        // 已提取的实体名，传递给后续片段

	for i, chunk := range chunks {
		entities, relations, err := e.extractChunkWithContext(ctx, chunk, fileID, knownEntityNames)
		if err != nil {
			logrus.Warnf("[LLMExtractor] Chunk %d/%d extraction failed: %v", i+1, len(chunks), err)
			continue
		}

		// 去重合并实体
		for _, ent := range entities {
			key := ent.Name + "|" + ent.Type
			if !entitySet[key] {
				entitySet[key] = true
				allEntities = append(allEntities, ent)
				knownEntityNames = append(knownEntityNames, ent.Name)
			}
		}
		// 去重合并关系
		for _, rel := range relations {
			key := rel.Source + "|" + rel.Target + "|" + rel.Type
			if !relationSet[key] {
				relationSet[key] = true
				allRelations = append(allRelations, rel)
			}
		}
	}

	logrus.Infof("[LLMExtractor] Extracted %d entities, %d relations from file %s (%d chunks)",
		len(allEntities), len(allRelations), fileID, len(chunks))
	return allEntities, allRelations, nil
}

// extractChunkWithContext 对单个文本片段调用 LLM 提取，同时注入已知实体上下文。
// 采用两步提取策略:
//
//	Step 1: 提取 entities
//	Step 2: 基于 entities 列表提取 relations
//
// 原因: 部分 LLM (Gemini 等) 在 json_object 模式下会忽略 relations，
// 拆分为两次调用可显著提升关系提取率。
func (e *LLMEntityExtractor) extractChunkWithContext(ctx context.Context, chunk string, fileID string, knownEntities []string) ([]Entity, []Relation, error) {
	url := strings.TrimSuffix(e.config.BaseURL, "/") + "/chat/completions"

	// ========== Step 1: 提取 Entities ==========
	var entityPrompt string
	if len(knownEntities) > 0 {
		entityPrompt = fmt.Sprintf(
			"Previously extracted entities: [%s]\n\n"+
				"Extract all key entities from the following text. Include any previously known entities if they appear.\n\n%s",
			strings.Join(knownEntities, ", "), chunk)
	} else {
		entityPrompt = fmt.Sprintf("Extract all key entities from the following text:\n\n%s", chunk)
	}

	entitySystemPrompt := `Extract entities from the text. Return JSON with an "entities" array.
Entity types: Concept, Technology, Function, Class, Interface, Tool, Person, Organization, Protocol, Algorithm
Rules: names should be concise (1-4 words). Return valid JSON only.
Format: {"entities": [{"name": "...", "type": "..."}]}`

	entityResult, err := e.callLLM(ctx, url, entitySystemPrompt, entityPrompt)
	if err != nil {
		return nil, nil, fmt.Errorf("entity extraction: %w", err)
	}

	var entityResp struct {
		Entities []llmEntity `json:"entities"`
	}
	if err := json.Unmarshal([]byte(entityResult), &entityResp); err != nil {
		return nil, nil, fmt.Errorf("parse entity JSON: %w (content: %s)", err, truncateStr(entityResult, 300))
	}
	logrus.Infof("[LLMExtractor] Step 1 - entities: %d", len(entityResp.Entities))

	// 构建实体名列表
	entityNames := make([]string, 0, len(entityResp.Entities))
	for _, ent := range entityResp.Entities {
		if ent.Name != "" {
			entityNames = append(entityNames, ent.Name)
		}
	}

	// ========== Step 2: 提取 Relations ==========
	var relations []Relation
	if len(entityNames) >= 2 {
		relationSystemPrompt := `You are a knowledge graph expert. Given a list of entities and a text, extract relationships between the entities.
Relationship types: uses, implements, extends, contains, depends_on, related_to, defines, calls, part_of, compared_with, based_on, created_by, provides, manages
Rules:
1. ONLY use entity names from the provided list as source and target
2. Extract relationships that are explicitly stated or strongly implied
3. Return valid JSON only
Format: {"relations": [{"source": "...", "target": "...", "type": "..."}]}`

		relationPrompt := fmt.Sprintf(
			"Entities: [%s]\n\nExtract relationships between these entities from the following text:\n\n%s",
			strings.Join(entityNames, ", "), chunk)

		relResult, err := e.callLLM(ctx, url, relationSystemPrompt, relationPrompt)
		if err != nil {
			logrus.Warnf("[LLMExtractor] Relation extraction failed: %v", err)
		} else {
			var relResp struct {
				Relations []llmRelation `json:"relations"`
			}
			if err := json.Unmarshal([]byte(relResult), &relResp); err != nil {
				logrus.Warnf("[LLMExtractor] Parse relation JSON failed: %v (content: %s)", err, truncateStr(relResult, 300))
			} else {
				logrus.Infof("[LLMExtractor] Step 2 - relations: %d", len(relResp.Relations))
				for _, r := range relResp.Relations {
					if r.Source != "" && r.Target != "" && r.Type != "" {
						relations = append(relations, Relation{
							Source:     r.Source,
							Target:     r.Target,
							Type:       r.Type,
							SourceFile: fileID,
							Properties: make(map[string]string),
						})
					}
				}
			}
		}
	}

	// 转换 entities
	entities := make([]Entity, 0, len(entityResp.Entities))
	for _, ent := range entityResp.Entities {
		if ent.Name == "" || ent.Type == "" {
			continue
		}
		entities = append(entities, Entity{
			Name:       ent.Name,
			Type:       ent.Type,
			SourceFile: fileID,
			Properties: make(map[string]string),
		})
	}

	return entities, relations, nil
}

// callLLM 封装单次 Chat Completions API 调用
func (e *LLMEntityExtractor) callLLM(ctx context.Context, url, systemPrompt, userPrompt string) (string, error) {
	reqBody := map[string]interface{}{
		"model": e.config.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature":     e.config.Temperature,
		"max_tokens":      e.config.MaxTokens,
		"response_format": map[string]string{"type": "json_object"},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.config.APIKey)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM API returned %d: %s", resp.StatusCode, truncateStr(string(respBody), 500))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal chat response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("empty LLM response")
	}

	result := chatResp.Choices[0].Message.Content
	result = strings.TrimPrefix(result, "```json")
	result = strings.TrimPrefix(result, "```")
	result = strings.TrimSuffix(result, "```")
	result = strings.TrimSpace(result)

	return result, nil
}

// splitForExtraction 将长文本按段落边界分片，确保每片不超过 maxChunk 字节。
// 修复: 当单个段落就超过 maxChunk 时，强制按句子边界切分，避免产出超大 chunk。
func splitForExtraction(content string, maxChunk int) []string {
	if len(content) <= maxChunk {
		return []string{content}
	}

	var chunks []string
	paragraphs := strings.Split(content, "\n\n")
	var current strings.Builder

	for _, para := range paragraphs {
		// 单个段落就超过 maxChunk，强制按句子边界切分
		if len(para) > maxChunk {
			// 先将已有内容存入
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			// 按句子切分超长段落
			sentences := strings.Split(para, "\n")
			for _, sent := range sentences {
				if current.Len()+len(sent)+1 > maxChunk && current.Len() > 0 {
					chunks = append(chunks, current.String())
					current.Reset()
				}
				if current.Len() > 0 {
					current.WriteString("\n")
				}
				current.WriteString(sent)
			}
			continue
		}

		if current.Len()+len(para)+2 > maxChunk && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

// truncateStr 截断字符串用于日志输出
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
