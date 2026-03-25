/*
┌─────────────────────────────────────────────────────────────────────────────┐
│                    graph_rag.go — Graph RAG 知识图谱接口                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  核心思想: 在传统向量语义检索之上叠加基于实体-关系的图谱检索，               │
│  通过多路召回融合提升 RAG 在多跳推理、关系查询等场景下的性能。               │
│                                                                             │
│  导出类型:                                                                   │
│    Entity           — 知识图谱中的实体节点                                   │
│    Relation          — 实体间的关系边                                         │
│    GraphSearchResult — 图谱子图检索结果                                       │
│    GraphStore        — 图存储接口（可对接 Neo4j、Redis Graph 等）             │
│    EntityExtractor   — 实体与关系提取器接口                                   │
│    SimpleEntityExtractor — 基于规则的轻量级实体提取器实现                     │
│                                                                             │
│  导出函数:                                                                   │
│    MergeGraphAndVectorResults — 融合图谱与向量检索结果                        │
│                                                                             │
│  当前状态: 接口骨架阶段，提供 In-Memory 实现供开发测试，                      │
│            生产环境应替换为持久化的 GraphStore 实现                            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"strings"
	"sync"
	"unicode"

	"github.com/sirupsen/logrus"
)

// Entity 知识图谱中的实体节点
type Entity struct {
	Name       string            `json:"name"`        // 实体名称
	Type       string            `json:"type"`        // 实体类型（如 Person, Function, Class, Concept）
	Properties map[string]string `json:"properties"`  // 额外属性
	SourceFile string            `json:"source_file"` // 来源文件 ID
}

// Relation 实体间的关系边
type Relation struct {
	Source     string            `json:"source"`     // 源实体名称
	Target     string            `json:"target"`     // 目标实体名称
	Type       string            `json:"type"`       // 关系类型（如 calls, imports, extends）
	Properties map[string]string `json:"properties"` // 额外属性
	SourceFile string            `json:"source_file"`
}

// GraphSearchResult 图谱子图检索结果
type GraphSearchResult struct {
	Entities    []Entity   `json:"entities"`
	Relations   []Relation `json:"relations"`
	ContextText string     `json:"context_text"` // 从子图提取的自然语言上下文
}

// GraphStore 图存储接口
// 生产环境可对接 Neo4j / RedisGraph / NebulaGraph 等图数据库
type GraphStore interface {
	// AddEntities 批量添加实体
	AddEntities(ctx context.Context, entities []Entity) error
	// AddRelations 批量添加关系
	AddRelations(ctx context.Context, relations []Relation) error
	// SearchByEntity 根据实体名称搜索相关子图
	SearchByEntity(ctx context.Context, entityName string, depth int) (*GraphSearchResult, error)
	// SearchByQuery 使用自然语言查询搜索相关子图
	SearchByQuery(ctx context.Context, query string, topK int) (*GraphSearchResult, error)
	// DeleteByFileID 删除某个文件关联的所有实体和关系
	DeleteByFileID(ctx context.Context, fileID string) error
	// Close 关闭连接
	Close() error
}

// EntityExtractor 实体与关系提取器接口
type EntityExtractor interface {
	// Extract 从文本中提取实体和关系
	Extract(ctx context.Context, content string, fileID string) ([]Entity, []Relation, error)
}

// --- In-Memory 实现（开发测试用） ---

// InMemoryGraphStore 基于内存的图存储，用于开发和测试
// 并发安全：所有读写操作均受 RWMutex 保护
// entityIndex 和 relationIndex 用于去重，防止重复索引同一文档时图谱膨胀
type InMemoryGraphStore struct {
	entities      []Entity
	relations     []Relation
	entityIndex   map[string]bool // key: "name|type|sourceFile" → 实体去重
	relationIndex map[string]bool // key: "source|type|target|sourceFile" → 关系去重
	mu            sync.RWMutex
}

func NewInMemoryGraphStore() *InMemoryGraphStore {
	return &InMemoryGraphStore{
		entities:      make([]Entity, 0),
		relations:     make([]Relation, 0),
		entityIndex:   make(map[string]bool),
		relationIndex: make(map[string]bool),
	}
}

func entityDedupeKey(e Entity) string {
	return e.Name + "|" + e.Type + "|" + e.SourceFile
}

func relationDedupeKey(r Relation) string {
	return r.Source + "|" + r.Type + "|" + r.Target + "|" + r.SourceFile
}

func (s *InMemoryGraphStore) AddEntities(ctx context.Context, entities []Entity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	for _, e := range entities {
		key := entityDedupeKey(e)
		if !s.entityIndex[key] {
			s.entityIndex[key] = true
			s.entities = append(s.entities, e)
			added++
		}
	}
	logrus.Infof("[GraphRAG] Added %d new entities (skipped %d duplicates), total: %d",
		added, len(entities)-added, len(s.entities))
	return nil
}

func (s *InMemoryGraphStore) AddRelations(ctx context.Context, relations []Relation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	for _, r := range relations {
		key := relationDedupeKey(r)
		if !s.relationIndex[key] {
			s.relationIndex[key] = true
			s.relations = append(s.relations, r)
			added++
		}
	}
	logrus.Infof("[GraphRAG] Added %d new relations (skipped %d duplicates), total: %d",
		added, len(relations)-added, len(s.relations))
	return nil
}

func (s *InMemoryGraphStore) SearchByEntity(ctx context.Context, entityName string, depth int) (*GraphSearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := &GraphSearchResult{}
	entityNameLower := strings.ToLower(entityName)

	// 收集匹配的实体
	for _, e := range s.entities {
		if strings.ToLower(e.Name) == entityNameLower ||
			strings.Contains(strings.ToLower(e.Name), entityNameLower) {
			result.Entities = append(result.Entities, e)
		}
	}

	// 收集相关的关系边
	for _, r := range s.relations {
		if strings.ToLower(r.Source) == entityNameLower ||
			strings.ToLower(r.Target) == entityNameLower {
			result.Relations = append(result.Relations, r)
		}
	}

	result.ContextText = buildContextFromGraph(result)
	return result, nil
}

func (s *InMemoryGraphStore) SearchByQuery(ctx context.Context, query string, topK int) (*GraphSearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := &GraphSearchResult{}
	queryLower := strings.ToLower(query)
	words := strings.Fields(queryLower)

	// 简单的关键词匹配：查询词命中实体名称
	matchCount := 0
	for _, e := range s.entities {
		if matchCount >= topK {
			break
		}
		nameLower := strings.ToLower(e.Name)
		for _, w := range words {
			if len(w) >= 2 && strings.Contains(nameLower, w) {
				result.Entities = append(result.Entities, e)
				matchCount++
				break
			}
		}
	}

	// 收集匹配实体相关的关系
	entitySet := make(map[string]bool)
	for _, e := range result.Entities {
		entitySet[strings.ToLower(e.Name)] = true
	}
	for _, r := range s.relations {
		if entitySet[strings.ToLower(r.Source)] || entitySet[strings.ToLower(r.Target)] {
			result.Relations = append(result.Relations, r)
		}
	}

	result.ContextText = buildContextFromGraph(result)
	return result, nil
}

func (s *InMemoryGraphStore) DeleteByFileID(ctx context.Context, fileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 过滤掉该文件的实体
	var filteredEntities []Entity
	for _, e := range s.entities {
		if e.SourceFile != fileID {
			filteredEntities = append(filteredEntities, e)
		}
	}
	removed := len(s.entities) - len(filteredEntities)
	s.entities = filteredEntities

	// 重建 entityIndex
	s.entityIndex = make(map[string]bool, len(s.entities))
	for _, e := range s.entities {
		s.entityIndex[entityDedupeKey(e)] = true
	}

	// 过滤掉该文件的关系
	var filteredRelations []Relation
	for _, r := range s.relations {
		if r.SourceFile != fileID {
			filteredRelations = append(filteredRelations, r)
		}
	}
	s.relations = filteredRelations

	// 重建 relationIndex
	s.relationIndex = make(map[string]bool, len(s.relations))
	for _, r := range s.relations {
		s.relationIndex[relationDedupeKey(r)] = true
	}

	logrus.Infof("[GraphRAG] Deleted %d entities for file: %s", removed, fileID)
	return nil
}

func (s *InMemoryGraphStore) Close() error {
	return nil
}

// --- 基于规则的实体提取器 ---

// SimpleEntityExtractor 基于规则的轻量级实体提取器
// 适用于代码仓库文档场景，从 Markdown 标题和代码块中提取函数名、类名等
type SimpleEntityExtractor struct{}

func NewSimpleEntityExtractor() *SimpleEntityExtractor {
	return &SimpleEntityExtractor{}
}

func (e *SimpleEntityExtractor) Extract(ctx context.Context, content string, fileID string) ([]Entity, []Relation, error) {
	var entities []Entity
	var relations []Relation

	lines := strings.Split(content, "\n")
	var currentSection string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 提取 Markdown 标题作为 Concept 实体
		if strings.HasPrefix(trimmed, "#") {
			title := strings.TrimLeft(trimmed, "# ")
			title = strings.TrimSpace(title)
			if title != "" {
				entities = append(entities, Entity{
					Name:       title,
					Type:       "Concept",
					SourceFile: fileID,
				})

				// 如果有上级 section，建立 contains 关系
				if currentSection != "" {
					relations = append(relations, Relation{
						Source:     currentSection,
						Target:     title,
						Type:       "contains",
						SourceFile: fileID,
					})
				}
				currentSection = title
			}
		}

		// 提取代码中的函数定义 (Go: func XXX)
		if strings.HasPrefix(trimmed, "func ") {
			funcName := extractGoFuncName(trimmed)
			if funcName != "" {
				entities = append(entities, Entity{
					Name:       funcName,
					Type:       "Function",
					SourceFile: fileID,
				})
				if currentSection != "" {
					relations = append(relations, Relation{
						Source:     currentSection,
						Target:     funcName,
						Type:       "defines",
						SourceFile: fileID,
					})
				}
			}
		}

		// 提取代码中的类型定义 (Go: type XXX struct/interface)
		if strings.HasPrefix(trimmed, "type ") && (strings.Contains(trimmed, "struct") || strings.Contains(trimmed, "interface")) {
			typeName := extractGoTypeName(trimmed)
			if typeName != "" {
				entityType := "Class"
				if strings.Contains(trimmed, "interface") {
					entityType = "Interface"
				}
				entities = append(entities, Entity{
					Name:       typeName,
					Type:       entityType,
					SourceFile: fileID,
				})
				if currentSection != "" {
					relations = append(relations, Relation{
						Source:     currentSection,
						Target:     typeName,
						Type:       "defines",
						SourceFile: fileID,
					})
				}
			}
		}
	}

	logrus.Infof("[GraphRAG] Extracted %d entities and %d relations from file %s",
		len(entities), len(relations), fileID)
	return entities, relations, nil
}

// --- 融合函数 ---

// MergeGraphAndVectorResults 融合图谱检索结果与向量检索结果。
// 图谱的 ContextText 被包装成 RetrievalResult 插入到向量结果前面，
// 让 LLM 优先看到结构化的实体关系信息，再参考语义相似的文档片段。
func MergeGraphAndVectorResults(graphResult *GraphSearchResult, vectorResults []RetrievalResult) []RetrievalResult {
	if graphResult == nil || graphResult.ContextText == "" {
		return vectorResults
	}

	// 将图谱上下文作为高优先级结果插入
	graphEntry := RetrievalResult{
		ChunkID:        "graph-context",
		FileID:         "knowledge-graph",
		FileName:       "Knowledge Graph Context",
		Content:        graphResult.ContextText,
		RelevanceScore: 1.0, // 图谱结果给予最高置信度
	}

	merged := make([]RetrievalResult, 0, len(vectorResults)+1)
	merged = append(merged, graphEntry)
	merged = append(merged, vectorResults...)

	return merged
}

// --- 辅助函数 ---

// buildContextFromGraph 将图谱子图转换为 LLM 可读的自然语言描述
func buildContextFromGraph(result *GraphSearchResult) string {
	if result == nil || (len(result.Entities) == 0 && len(result.Relations) == 0) {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Knowledge Graph Context\n\n")

	if len(result.Entities) > 0 {
		sb.WriteString("### Entities\n")
		for _, e := range result.Entities {
			sb.WriteString("- **")
			sb.WriteString(e.Name)
			sb.WriteString("** (")
			sb.WriteString(e.Type)
			sb.WriteString(")")
			if e.SourceFile != "" {
				sb.WriteString(" [source: " + e.SourceFile + "]")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(result.Relations) > 0 {
		sb.WriteString("### Relations\n")
		for _, r := range result.Relations {
			sb.WriteString("- ")
			sb.WriteString(r.Source)
			sb.WriteString(" → [")
			sb.WriteString(r.Type)
			sb.WriteString("] → ")
			sb.WriteString(r.Target)
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// extractGoFuncName 从 Go func 声明行提取函数名
func extractGoFuncName(line string) string {
	line = strings.TrimPrefix(line, "func ")
	// 处理方法接收者 (r *Type) 的情况
	if strings.HasPrefix(line, "(") {
		idx := strings.Index(line, ")")
		if idx == -1 {
			return ""
		}
		line = strings.TrimSpace(line[idx+1:])
	}
	// 提取函数名（到第一个 '(' 之前）
	idx := strings.Index(line, "(")
	if idx == -1 {
		return ""
	}
	name := strings.TrimSpace(line[:idx])
	if name == "" || !unicode.IsUpper(rune(name[0])) {
		return "" // 仅提取导出的函数
	}
	return name
}

// extractGoTypeName 从 Go type 声明行提取类型名
func extractGoTypeName(line string) string {
	line = strings.TrimPrefix(line, "type ")
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return ""
	}
	name := parts[0]
	if name == "" || !unicode.IsUpper(rune(name[0])) {
		return "" // 仅提取导出的类型
	}
	return name
}
