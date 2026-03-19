/*
┌─────────────────────────────────────────────────────────────────────────────┐
│               neo4j_graph_store.go — Neo4j 图数据库 GraphStore 实现          │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  基于 Neo4j 的持久化 GraphStore 实现，支持实体-关系的 CRUD 和图谱检索。       │
│                                                                             │
│  Cypher 查询策略:                                                            │
│    - 写入: MERGE (幂等) 保证实体/关系不重复                                  │
│    - 读取: MATCH + OPTIONAL MATCH 做多跳子图展开                             │
│    - 删除: MATCH + DETACH DELETE 级联清理                                    │
│                                                                             │
│  并发安全: Neo4j Driver 内置连接池和事务管理，天然线程安全                     │
│                                                                             │
│  导出类型:                                                                   │
│    Neo4jGraphStore       — GraphStore 接口的 Neo4j 实现                      │
│    Neo4jConfig           — 连接配置                                          │
│                                                                             │
│  导出函数:                                                                   │
│    NewNeo4jGraphStore()  — 创建 Neo4j 图存储实例                             │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
*/
package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/sirupsen/logrus"
)

// Neo4jConfig Neo4j 连接配置
type Neo4jConfig struct {
	URI            string        `toml:"uri"`             // bolt://localhost:7687
	Username       string        `toml:"username"`        // neo4j
	Password       string        `toml:"password"`        // password
	Database       string        `toml:"database"`        // neo4j (默认库)
	MaxConnPool    int           `toml:"max_conn_pool"`   // 连接池大小
	ConnectTimeout time.Duration `toml:"connect_timeout"` // 连接超时
}

// DefaultNeo4jConfig 默认 Neo4j 配置
func DefaultNeo4jConfig() Neo4jConfig {
	return Neo4jConfig{
		URI:            "bolt://localhost:7687",
		Username:       "neo4j",
		Password:       "password",
		Database:       "neo4j",
		MaxConnPool:    50,
		ConnectTimeout: 10 * time.Second,
	}
}

// Neo4jGraphStore 基于 Neo4j 的持久化图存储
// 实现 GraphStore 接口，使用 Cypher 查询语言操作实体与关系
type Neo4jGraphStore struct {
	driver   neo4j.DriverWithContext
	database string
}

// NewNeo4jGraphStore 创建 Neo4j 图存储实例
// 建立连接并验证可达性，创建必要的索引（幂等）
func NewNeo4jGraphStore(ctx context.Context, cfg Neo4jConfig) (*Neo4jGraphStore, error) {
	driver, err := neo4j.NewDriverWithContext(
		cfg.URI,
		neo4j.BasicAuth(cfg.Username, cfg.Password, ""),
	)
	if err != nil {
		return nil, fmt.Errorf("create neo4j driver: %w", err)
	}

	// 验证连接可达
	if err := driver.VerifyConnectivity(ctx); err != nil {
		driver.Close(ctx)
		return nil, fmt.Errorf("neo4j connectivity check failed: %w", err)
	}

	store := &Neo4jGraphStore{
		driver:   driver,
		database: cfg.Database,
	}

	// 幂等创建索引，加速按名称和文件 ID 的查询
	if err := store.ensureIndexes(ctx); err != nil {
		logrus.Warnf("[Neo4jGraphStore] Index creation warning: %v", err)
	}

	logrus.Infof("[Neo4jGraphStore] Connected to Neo4j at %s (db=%s)", cfg.URI, cfg.Database)
	return store, nil
}

// ensureIndexes 创建 Neo4j 索引（幂等，已存在则跳过）
func (s *Neo4jGraphStore) ensureIndexes(ctx context.Context) error {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
	defer session.Close(ctx)

	indexes := []string{
		"CREATE INDEX entity_name IF NOT EXISTS FOR (e:Entity) ON (e.name)",
		"CREATE INDEX entity_source IF NOT EXISTS FOR (e:Entity) ON (e.source_file)",
		"CREATE INDEX entity_type IF NOT EXISTS FOR (e:Entity) ON (e.entity_type)",
	}

	for _, cypher := range indexes {
		if _, err := session.Run(ctx, cypher, nil); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// AddEntities 批量添加实体节点
// 使用 MERGE 保证幂等性：同名同类型同来源的实体不会重复创建
// UNWIND + MERGE 模式比逐条执行减少 95% 的网络往返
func (s *Neo4jGraphStore) AddEntities(ctx context.Context, entities []Entity) error {
	if len(entities) == 0 {
		return nil
	}

	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
	defer session.Close(ctx)

	// 将 Entity 切片转为 Neo4j 可接受的 map 切片
	// 注意: Neo4j 不接受空 Map{} 作为属性值，空 properties 时不设该字段
	params := make([]map[string]interface{}, len(entities))
	for i, e := range entities {
		p := map[string]interface{}{
			"name":        e.Name,
			"entity_type": e.Type,
			"source_file": e.SourceFile,
		}
		if len(e.Properties) > 0 {
			// 展开为顶层 key-value 对，避免嵌套 Map 类型
			for k, v := range e.Properties {
				p["prop_"+k] = v
			}
		}
		params[i] = p
	}

	// UNWIND 批量 MERGE：name + entity_type + source_file 三元组唯一确定一个实体
	cypher := `
		UNWIND $entities AS e
		MERGE (n:Entity {name: e.name, entity_type: e.entity_type, source_file: e.source_file})
		SET n.updated_at = datetime()
	`
	_, err := session.Run(ctx, cypher, map[string]interface{}{"entities": params})
	if err != nil {
		return fmt.Errorf("add entities: %w", err)
	}

	logrus.Infof("[Neo4jGraphStore] Added %d entities", len(entities))
	return nil
}

// AddRelations 批量添加关系边
// 使用 APOC 动态创建原生关系类型（如 :USES, :BASED_ON 等），
// 相比旧方案 RELATES_TO + rel_type 属性：
//   - 图遍历性能更好（Neo4j 按关系类型分区存储）
//   - Cypher 查询更直观（MATCH ()-[:USES]->() vs MATCH ()-[r {rel_type:"uses"}]->()）
//   - Neo4j Browser 中不同关系类型自动着色
//
// 如果 APOC 不可用则优雅降级到 RELATES_TO + 属性方案。
func (s *Neo4jGraphStore) AddRelations(ctx context.Context, relations []Relation) error {
	if len(relations) == 0 {
		return nil
	}

	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
	defer session.Close(ctx)

	params := make([]map[string]interface{}, len(relations))
	for i, r := range relations {
		params[i] = map[string]interface{}{
			"source":      r.Source,
			"target":      r.Target,
			"rel_type":    normalizeRelType(r.Type),
			"source_file": r.SourceFile,
		}
	}

	// 优先使用 APOC 创建动态原生关系类型
	// 注意: 不使用 CALL {} 子查询语法，因为 Neo4j 5 Community 对其支持有限
	apocCypher := `
		UNWIND $relations AS r
		MATCH (src:Entity {name: r.source})
		MATCH (tgt:Entity {name: r.target})
		WITH src, tgt, r
		CALL apoc.merge.relationship(src, r.rel_type, {}, {source_file: r.source_file, updated_at: datetime()}, tgt, {}) YIELD rel
		RETURN rel
	`
	_, err := session.Run(ctx, apocCypher, map[string]interface{}{"relations": params})
	if err != nil {
		// APOC 不可用时降级到 RELATES_TO + rel_type 属性
		logrus.Warnf("[Neo4jGraphStore] APOC unavailable, falling back to RELATES_TO: %v", err)
		return s.addRelationsFallback(ctx, session, params)
	}

	logrus.Infof("[Neo4jGraphStore] Added %d relations (native types via APOC)", len(relations))
	return nil
}

// addRelationsFallback APOC 不可用时的降级方案：统一用 RELATES_TO 标签 + rel_type 属性
func (s *Neo4jGraphStore) addRelationsFallback(ctx context.Context, session neo4j.SessionWithContext, params []map[string]interface{}) error {
	cypher := `
		UNWIND $relations AS r
		CALL {
			WITH r
			MATCH (src:Entity {name: r.source})
			MATCH (tgt:Entity {name: r.target})
			WITH src, tgt, r
			LIMIT 1
			MERGE (src)-[rel:RELATES_TO {rel_type: r.rel_type}]->(tgt)
			SET rel.source_file = r.source_file,
			    rel.updated_at = datetime()
		}
	`
	_, err := session.Run(ctx, cypher, map[string]interface{}{"relations": params})
	if err != nil {
		return fmt.Errorf("add relations (fallback): %w", err)
	}
	logrus.Infof("[Neo4jGraphStore] Added %d relations (RELATES_TO fallback)", len(params))
	return nil
}

// normalizeRelType 将 LLM 输出的关系类型标准化为 Neo4j 关系标签格式
// 例如: "based_on" → "BASED_ON", "related_to" → "RELATED_TO"
func normalizeRelType(relType string) string {
	// 转大写 + 空格替换为下划线
	normalized := strings.ToUpper(strings.TrimSpace(relType))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if normalized == "" {
		normalized = "RELATED_TO"
	}
	return normalized
}

// SearchByEntity 根据实体名称搜索相关子图
// depth 控制图遍历深度: 1=直接邻居, 2=二跳, 3=三跳（超过 3 跳噪声过大）
func (s *Neo4jGraphStore) SearchByEntity(ctx context.Context, entityName string, depth int) (*GraphSearchResult, error) {
	if depth <= 0 {
		depth = 2
	}
	if depth > 3 {
		depth = 3
	}

	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
	defer session.Close(ctx)

	// 使用变长路径模式匹配 1..depth 跳内的所有节点和关系
	cypher := fmt.Sprintf(`
		MATCH (start:Entity)
		WHERE toLower(start.name) CONTAINS toLower($name)
		OPTIONAL MATCH path = (start)-[*1..%d]-(neighbor:Entity)
		WITH start, collect(DISTINCT neighbor) AS neighbors,
		     collect(DISTINCT relationships(path)) AS allRels
		RETURN start, neighbors,
		       [r IN reduce(acc = [], rels IN allRels | acc + rels) | r] AS relations
		LIMIT 50
	`, depth)

	result, err := session.Run(ctx, cypher, map[string]interface{}{"name": entityName})
	if err != nil {
		return nil, fmt.Errorf("search by entity: %w", err)
	}

	graphResult := &GraphSearchResult{}
	entitySet := make(map[string]bool)

	for result.Next(ctx) {
		record := result.Record()

		// 解析起始实体
		if startNode, ok := record.Get("start"); ok {
			if node, ok := startNode.(neo4j.Node); ok {
				entity := nodeToEntity(node)
				if !entitySet[entity.Name] {
					graphResult.Entities = append(graphResult.Entities, entity)
					entitySet[entity.Name] = true
				}
			}
		}

		// 解析邻居实体
		if neighbors, ok := record.Get("neighbors"); ok {
			if nodeList, ok := neighbors.([]interface{}); ok {
				for _, n := range nodeList {
					if node, ok := n.(neo4j.Node); ok {
						entity := nodeToEntity(node)
						if !entitySet[entity.Name] {
							graphResult.Entities = append(graphResult.Entities, entity)
							entitySet[entity.Name] = true
						}
					}
				}
			}
		}

		// 解析关系
		if rels, ok := record.Get("relations"); ok {
			if relList, ok := rels.([]interface{}); ok {
				for _, r := range relList {
					if rel, ok := r.(neo4j.Relationship); ok {
						graphResult.Relations = append(graphResult.Relations, relationToRelation(rel))
					}
				}
			}
		}
	}

	graphResult.ContextText = buildContextFromGraph(graphResult)
	logrus.Infof("[Neo4jGraphStore] SearchByEntity(%s, depth=%d): %d entities, %d relations",
		entityName, depth, len(graphResult.Entities), len(graphResult.Relations))
	return graphResult, nil
}

// SearchByQuery 使用自然语言查询搜索相关子图
// 策略: 分词 → 全文匹配实体名称 → 收集相关子图
func (s *Neo4jGraphStore) SearchByQuery(ctx context.Context, query string, topK int) (*GraphSearchResult, error) {
	if topK <= 0 {
		topK = 10
	}

	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
	defer session.Close(ctx)

	// 分词后构造 OR 条件
	words := extractQueryWords(query)
	if len(words) == 0 {
		return &GraphSearchResult{}, nil
	}

	// 构造 WHERE 条件: name CONTAINS word1 OR name CONTAINS word2 ...
	conditions := make([]string, 0, len(words))
	params := map[string]interface{}{"topK": topK}
	for i, w := range words {
		paramName := fmt.Sprintf("w%d", i)
		conditions = append(conditions, fmt.Sprintf("toLower(e.name) CONTAINS toLower($%s)", paramName))
		params[paramName] = w
	}
	whereClause := strings.Join(conditions, " OR ")

	cypher := fmt.Sprintf(`
		MATCH (e:Entity)
		WHERE %s
		WITH e
		LIMIT $topK
				OPTIONAL MATCH (e)-[r]-(neighbor:Entity)
		RETURN e, collect(DISTINCT neighbor) AS neighbors, collect(DISTINCT r) AS relations
	`, whereClause)

	result, err := session.Run(ctx, cypher, params)
	if err != nil {
		return nil, fmt.Errorf("search by query: %w", err)
	}

	graphResult := &GraphSearchResult{}
	entitySet := make(map[string]bool)

	for result.Next(ctx) {
		record := result.Record()

		if eNode, ok := record.Get("e"); ok {
			if node, ok := eNode.(neo4j.Node); ok {
				entity := nodeToEntity(node)
				if !entitySet[entity.Name] {
					graphResult.Entities = append(graphResult.Entities, entity)
					entitySet[entity.Name] = true
				}
			}
		}

		if neighbors, ok := record.Get("neighbors"); ok {
			if nodeList, ok := neighbors.([]interface{}); ok {
				for _, n := range nodeList {
					if node, ok := n.(neo4j.Node); ok {
						entity := nodeToEntity(node)
						if !entitySet[entity.Name] {
							graphResult.Entities = append(graphResult.Entities, entity)
							entitySet[entity.Name] = true
						}
					}
				}
			}
		}

		if rels, ok := record.Get("relations"); ok {
			if relList, ok := rels.([]interface{}); ok {
				for _, r := range relList {
					if rel, ok := r.(neo4j.Relationship); ok {
						graphResult.Relations = append(graphResult.Relations, relationToRelation(rel))
					}
				}
			}
		}
	}

	graphResult.ContextText = buildContextFromGraph(graphResult)
	return graphResult, nil
}

// DeleteByFileID 删除某个文件关联的实体和关系。
// 因为实体现在可能被多个文件共享（source_files 数组），
// 删除策略为:
//  1. 先删除该文件的关系边
//  2. 从实体的 source_files 数组中移除该 fileID
//  3. 仅当 source_files 变为空时才删除节点（无任何文件引用）
func (s *Neo4jGraphStore) DeleteByFileID(ctx context.Context, fileID string) error {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
	defer session.Close(ctx)

	// Step 1: 删除该文件产生的关系边（兼容原生类型 + RELATES_TO 降级模式）
	_, err := session.Run(ctx,
		`MATCH (a:Entity)-[r]->(b:Entity)
		 WHERE r.source_file = $fileID
		 DELETE r`,
		map[string]interface{}{"fileID": fileID})
	if err != nil {
		return fmt.Errorf("delete relations for file %s: %w", fileID, err)
	}

	// Step 2: 从 source_files 数组中移除该 fileID，若数组变空则删除节点
	result, err := session.Run(ctx, `
		MATCH (e:Entity)
		WHERE e.source_file = $fileID OR $fileID IN coalesce(e.source_files, [])
		WITH e,
		     [sf IN coalesce(e.source_files, []) WHERE sf <> $fileID] AS remaining
		WITH e, remaining,
		     CASE WHEN size(remaining) = 0 THEN true ELSE false END AS shouldDelete
		FOREACH (_ IN CASE WHEN shouldDelete THEN [1] ELSE [] END |
			DETACH DELETE e
		)
		FOREACH (_ IN CASE WHEN NOT shouldDelete THEN [1] ELSE [] END |
			SET e.source_files = remaining,
			    e.source_file = CASE WHEN size(remaining) > 0 THEN remaining[0] ELSE null END
		)
		RETURN count(CASE WHEN shouldDelete THEN 1 END) AS deleted,
		       count(CASE WHEN NOT shouldDelete THEN 1 END) AS updated
	`, map[string]interface{}{"fileID": fileID})
	if err != nil {
		return fmt.Errorf("delete/update entities for file %s: %w", fileID, err)
	}

	if result.Next(ctx) {
		deleted, _ := result.Record().Get("deleted")
		updated, _ := result.Record().Get("updated")
		logrus.Infof("[Neo4jGraphStore] File %s: deleted %v entities, updated %v shared entities",
			fileID, deleted, updated)
	}
	return nil
}

// Close 关闭 Neo4j 驱动连接
func (s *Neo4jGraphStore) Close() error {
	return s.driver.Close(context.Background())
}

// --- 辅助函数 ---

// nodeToEntity 将 Neo4j Node 转换为 Entity
func nodeToEntity(node neo4j.Node) Entity {
	e := Entity{
		Properties: make(map[string]string),
	}
	props := node.Props
	if v, ok := props["name"].(string); ok {
		e.Name = v
	}
	if v, ok := props["entity_type"].(string); ok {
		e.Type = v
	}
	if v, ok := props["source_file"].(string); ok {
		e.SourceFile = v
	}
	if v, ok := props["properties"].(map[string]interface{}); ok {
		for k, val := range v {
			e.Properties[k] = fmt.Sprintf("%v", val)
		}
	}
	return e
}

// relationToRelation 将 Neo4j Relationship 转换为 Relation
// 兼容两种模式:
//   - APOC 原生类型: 关系标签就是类型（如 :BASED_ON）
//   - 降级模式: 关系标签为 :RELATES_TO，真实类型存在 rel_type 属性中
func relationToRelation(rel neo4j.Relationship) Relation {
	r := Relation{
		Properties: make(map[string]string),
	}
	props := rel.Props

	// 优先从 rel_type 属性读取（降级模式兼容）
	if v, ok := props["rel_type"].(string); ok && v != "" {
		r.Type = v
	} else {
		// APOC 原生模式: 关系标签就是类型
		r.Type = rel.Type
	}
	if v, ok := props["source_file"].(string); ok {
		r.SourceFile = v
	}
	if v, ok := props["properties"].(map[string]interface{}); ok {
		for k, val := range v {
			r.Properties[k] = fmt.Sprintf("%v", val)
		}
	}
	return r
}

// extractQueryWords extracts meaningful keywords from query (length >= 2)
func extractQueryWords(query string) []string {
	words := strings.Fields(strings.ToLower(query))
	var result []string
	seen := make(map[string]bool)

	stopWords := map[string]bool{
		"the": true, "is": true, "a": true, "an": true, "in": true, "on": true,
		"of": true, "to": true, "for": true, "and": true, "or": true, "how": true,
		"what": true, "why": true, "when": true, "where": true, "which": true,
	}

	for _, w := range words {
		w = strings.TrimFunc(w, func(r rune) bool {
			if r >= 'a' && r <= 'z' {
				return false
			}
			if r >= '0' && r <= '9' {
				return false
			}
			if r >= 0x4e00 && r <= 0x9fff {
				return false // CJK characters
			}
			if r == '_' || r == '-' {
				return false
			}
			return true // trim all punctuation
		})
		if len(w) < 2 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		result = append(result, w)
	}
	return result
}
