# RAG Pipeline — 核心流程技术文档

## 1. 文档索引流程 (IndexDocument)

### 1.1 完整数据流

```
用户上传文档 "k8s_guide.md" (8000字符)
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  Step 1: 删除旧数据 (Upsert 语义)                       │
│  FT.SEARCH mcp_rag_user_1:idx @file_id:{k8s001} RETURN 0│
│  → 找到 5 个旧 chunk → Pipeline DEL 全部删除             │
└─────────────────────────┬───────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Step 2: 智能分块 (4 级策略选择)                         │
│                                                         │
│  ┌─ 优先级1: 代码感知分块 (CodeChunking)                │
│  │   检测: DetectCodeLanguage(content, fileName)        │
│  │   支持: .go/.py/.js/.ts/.java/.rs/.cpp 等            │
│  │   策略: 按函数/类/方法边界切分                        │
│  │                                                      │
│  ├─ 优先级2: 语义分块 (SemanticChunking)                │
│  │   原理: 相邻句子 Embedding 余弦相似度 < 阈值时切分    │
│  │   参数: similarity_threshold=0.5, min=100, max=1500  │
│  │                                                      │
│  ├─ 优先级3: 结构感知分块 (StructureAwareChunk)         │
│  │   ParseDocument → 识别 Markdown 标题层次 (#/##/###)  │
│  │   按 Section 边界切分，保留层次上下文                  │
│  │                                                      │
│  └─ 优先级4: 固定窗口分块 (ChunkDocument)               │
│      参数: max=1000字符, min=100, overlap=200           │
│      按段落/句子边界对齐，避免切断完整句子               │
└─────────────────────────┬───────────────────────────────┘
                          │
                          │ 输出: []Chunk (8个chunk)
                          │ 示例: [{ChunkID:"chunk-a1b2c3",
                          │         Content:"## Service类型\nKubernetes...",
                          │         ChunkIndex:0, TokenCount:245,
                          │         ParentChunkID:"parent-x1"}]
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Step 3: 缓存去重 + 分批向量化                           │
│                                                         │
│  texts = ["## Service类型...", "## Pod生命周期...", ...]  │
│  batchSize = 10                                         │
│                                                         │
│  for batch in texts[0:10], texts[10:20]... {            │
│    ┌─ cache.GetBatch(batch)                             │
│    │    L1 LRU: SHA256(text) → []float64 (内存)         │
│    │    L2 Redis: GET emb_cache:{sha256} (持久化)       │
│    │    → 命中5个, 未命中3个                             │
│    │                                                    │
│    ├─ embedWithoutCache(missedTexts)  // 仅未命中的     │
│    │    Manager.EmbedStrings(ctx, 3个文本)               │
│    │    → 选择 Provider: priority=1, circuit=closed     │
│    │    → POST dashscope API                            │
│    │    → 返回 3×[]float64 (dim=1024)                   │
│    │                                                    │
│    └─ cache.Put(text, vector) // 回填缓存               │
│         L1: LRU.Add(sha256, vector)                     │
│         L2: SET emb_cache:{sha256} {vector} EX 86400    │
│  }                                                      │
│                                                         │
│  结果: allVectors = 8×[]float64 (每个1024维)            │
└─────────────────────────┬───────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Step 4: 惰性建索引 (EnsureIndex)                       │
│                                                         │
│  FT.INFO mcp_rag_user_1:idx                             │
│  → 若不存在:                                            │
│  FT.CREATE mcp_rag_user_1:idx ON HASH                   │
│    PREFIX 1 mcp_rag_user_1:                              │
│    SCHEMA                                                │
│      content TEXT                                        │
│      file_id TAG                                        │
│      file_name TAG                                      │
│      chunk_id TAG                                       │
│      chunk_index NUMERIC SORTABLE                        │
│      parent_chunk_id TAG                                │
│      vector VECTOR FLAT 6 TYPE FLOAT32 DIM 1024         │
│                   DISTANCE_METRIC COSINE                 │
└─────────────────────────┬───────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Step 5: Pipeline 批量写入                               │
│                                                         │
│  Pipeline.HSET mcp_rag_user_1:chunk-a1b2c3              │
│    content  "## Service类型\nKubernetes提供..."          │
│    file_id  "k8s001"                                    │
│    file_name "k8s_guide.md"                             │
│    chunk_id "chunk-a1b2c3"                              │
│    chunk_index 0                                        │
│    parent_chunk_id "parent-x1"                          │
│    vector   <4096 bytes: 1024×float32 小端序>           │
│                                                         │
│  Pipeline.HSET mcp_rag_user_1:chunk-d4e5f6 ...          │
│  ... (共 8 条)                                          │
│  Pipeline.Exec() → 批量执行                              │
└─────────────────────────┬───────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Step 6: Graph RAG 实体提取 (异步, fire-and-forget)      │
│                                                         │
│  go func() {                                            │
│    entities, relations := extractor.Extract(content)     │
│    graphStore.AddEntities(entities)                      │
│    graphStore.AddRelations(relations)                    │
│  }()                                                    │
│                                                         │
│  提取示例:                                               │
│  Entity: {Name:"Kubernetes", Type:"Technology"}          │
│  Entity: {Name:"Service", Type:"Concept"}                │
│  Relation: {Source:"Service", Target:"Kubernetes",       │
│             Type:"BELONGS_TO"}                           │
└─────────────────────────────────────────────────────────┘

返回: IndexResult{FileID:"k8s001", TotalChunks:8,
                   Indexed:8, Failed:0, Cached:5}
```

### 1.2 Parent-Child 分块模式

```
原始文档 (3000字符)
         │
         ▼
┌───────────────────────────────┐
│ 父块 (Parent Chunk)           │
│ ID: parent-x1                 │
│ Content: 完整的第一节          │
│ (1500字符, 包含完整上下文)     │
│                               │
│  ┌─────────────────────────┐  │
│  │子块1 (Child Chunk)      │  │
│  │ID: chunk-a1b2c3         │  │
│  │ParentChunkID: parent-x1 │  │
│  │Content: 前500字符        │  │
│  │EmbeddingContent: 前500字 │  │ ← 用子块文本做 Embedding
│  └─────────────────────────┘  │
│  ┌─────────────────────────┐  │
│  │子块2 (Child Chunk)      │  │
│  │ID: chunk-d4e5f6         │  │
│  │ParentChunkID: parent-x1 │  │
│  │Content: 后500字符        │  │
│  │EmbeddingContent: 后500字 │  │
│  └─────────────────────────┘  │
└───────────────────────────────┘

检索时: 子块命中 → deduplicateByParent()
→ 返回父块 Content (完整上下文) + MatchedChildContent (命中片段)
```

## 2. 文档检索流程 (Retrieve)

### 2.1 完整检索链

```
用户查询: "Kubernetes Service有哪些类型？"
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  Step 0: 查询预校验                                      │
│  isValidQuery("Kubernetes Service有哪些类型？")          │
│  → 非空 ✓ | ≥2字符 ✓ | 非占位符 ✓ | 非纯符号 ✓         │
│  isKnownProbeQuery() → false (非 Cursor 探测)           │
└─────────────────────────┬───────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Step 1: Multi-Query 多查询检索                          │
│  (条件: MultiQueryEnabled && 剩余时间 > 15s)             │
│                                                         │
│  LLM 生成查询变体:                                       │
│  原始: "Kubernetes Service有哪些类型？"                   │
│  变体1: "K8s Service types: ClusterIP NodePort LoadBalancer"│
│  变体2: "Kubernetes 服务发现机制和类型"                   │
│  变体3: "K8s 中 Service 的不同暴露方式"                   │
│                                                         │
│  并发检索 4 个查询 (原始 + 3 变体)                       │
│  → RRF (Reciprocal Rank Fusion) 融合排序                 │
│  score(d) = Σ 1/(k + rank_i(d))  其中 k=60             │
│                                                         │
│  ┌─ 若成功: 直接返回融合结果 (跳过后续 HyDE)            │
│  └─ 若失败: 降级到单查询模式                             │
└─────────────────────────┬───────────────────────────────┘
                          ▼ (降级时)
┌─────────────────────────────────────────────────────────┐
│  Step 2: HyDE 查询扩展                                  │
│  (条件: HyDEEnabled && 剩余时间 > 10s)                   │
│                                                         │
│  System: "You are a helpful expert..."                   │
│  User: "Kubernetes Service有哪些类型？"                  │
│                                                         │
│  LLM 生成假想文档:                                       │
│  "Kubernetes Service 主要有四种类型：                     │
│   1. ClusterIP - 集群内部访问                            │
│   2. NodePort - 通过节点端口暴露                         │
│   3. LoadBalancer - 外部负载均衡器                        │
│   4. ExternalName - DNS CNAME 映射"                      │
│                                                         │
│  扩展查询 = 原始查询 + "\n" + 假想文档                   │
│  (假想文档在 Embedding 空间更接近真实答案文档)            │
└─────────────────────────┬───────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Step 3: 查询向量化                                      │
│                                                         │
│  embedTexts(ctx, ["扩展后的查询文本"])                    │
│  三级降级:                                               │
│    1. CachedEmbedStrings → L1 LRU / L2 Redis            │
│    2. EmbedStrings → Manager (多Provider故障转移)        │
│    3. embedding.EmbedStrings → 直连 Embedder             │
│                                                         │
│  → queryVector: []float64 (1024维)                      │
│  → float64→float32→[]byte (4096 bytes, 小端序)          │
└─────────────────────────┬───────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Step 4A: 纯向量检索 (默认)                              │
│                                                         │
│  FT.SEARCH mcp_rag_user_1:idx                           │
│    "*=>[KNN 5 @vector $vec AS distance]"                │
│    PARAMS 2 vec <4096 bytes>                            │
│    SORTBY distance ASC                                   │
│    RETURN 7 content file_id file_name chunk_id          │
│             chunk_index parent_chunk_id distance          │
│    DIALECT 2                                             │
│    LIMIT 0 5                                             │
│                                                         │
│  OR                                                      │
│                                                         │
│  Step 4B: 混合检索 (HybridSearchEnabled)                │
│                                                         │
│  第1路: 向量检索 TopK×3 = 15 (过采样)                    │
│  第2路: BM25 全文检索                                    │
│    FT.SEARCH mcp_rag_user_1:idx                         │
│      "@content:(Kubernetes Service 类型)"               │
│      SORTBY __score ASC LIMIT 0 15                      │
│                                                         │
│  RRF 融合: score = w_vec × rank_vec + w_kw × rank_kw    │
│  默认权重: vector=0.7, keyword=0.3                       │
│  截断到 TopK=5                                           │
└─────────────────────────┬───────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Step 5: 后处理                                          │
│                                                         │
│  5a. deduplicateByParent()                               │
│      多个子块命中同一父块 → 保留最高分的                  │
│      填充 MatchedChildContent                            │
│                                                         │
│  5b. filterByMinScore(MinScore=0.3)                      │
│      distance → score: score = 1 - distance/2           │
│      过滤 score < 0.3 的低质量结果                       │
│                                                         │
│  5c. Context Compressor (条件: 剩余时间 > 5s)            │
│      LLM 压缩: 提取与查询最相关的段落                    │
│      或 Embedding 相似度过滤                             │
└─────────────────────────┬───────────────────────────────┘
                          ▼
返回: []RetrievalResult
  [{Content:"## Service类型...", FileID:"k8s001",
    FileName:"k8s_guide.md", RelevanceScore:0.87,
    ChunkID:"chunk-a1b2c3", ChunkIndex:0}]
```

### 2.2 超时预算三级降级

```
总超时: 60s (defaultToolTimeout)
         │
         ├── 剩余 > 15s → 执行 Multi-Query (LLM生成变体)
         │                 消耗: ~5-10s
         │
         ├── 剩余 > 10s → 执行 HyDE (LLM生成假想文档)
         │                 消耗: ~3-5s
         │
         ├── 剩余 > 5s  → 执行 Context Compressor
         │                 消耗: ~2-5s
         │
         └── 剩余 < 5s  → 仅执行向量检索 + 基础过滤
                          消耗: ~0.5-1s
```

## 3. Embedding Manager 多 Provider 故障转移

```
EmbedStrings(ctx, texts)
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  Manager.EmbedStrings()                                  │
│                                                         │
│  按 (Priority ASC, Weight DESC) 排序 Providers:         │
│    [0] primary-dashscope  P=1  W=100  Circuit=CLOSED    │
│    [1] backup-openai      P=2  W=50   Circuit=CLOSED    │
│    [2] local-ollama       P=3  W=30   Circuit=OPEN      │
│                                                         │
│  ┌─ 尝试 Provider[0]: primary-dashscope                 │
│  │   Circuit: CLOSED (正常)                             │
│  │   POST https://dashscope.aliyuncs.com/...            │
│  │   → 200 OK, 返回向量                                │
│  │   → 更新统计: success++, avgLatency=120ms            │
│  │   └─ 成功! 返回结果                                  │
│  │                                                      │
│  │  (若失败 ↓)                                          │
│  │                                                      │
│  ├─ 尝试 Provider[1]: backup-openai                     │
│  │   Circuit: CLOSED                                    │
│  │   POST https://api.openai.com/v1/embeddings          │
│  │   → 自动故障转移                                     │
│  │                                                      │
│  └─ Provider[2]: local-ollama                           │
│      Circuit: OPEN (已熔断, 跳过)                       │
│      → 等待 HalfOpenAfter (60s) 后探针请求              │
└─────────────────────────────────────────────────────────┘
```

## 4. Embedding 缓存 (L1 + L2)

```
CachedEmbedStrings(ctx, ["Kubernetes Service", "Pod生命周期"])
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  L1: 内存 LRU 缓存                                      │
│  容量: 10000 条 | TTL: 30min                             │
│  Key: SHA256("Kubernetes Service") = "a3f2b1..."        │
│                                                         │
│  ┌─ LRU.Get("a3f2b1...") → HIT! 返回 []float64         │
│  └─ LRU.Get("e7c8d9...") → MISS                         │
└─────────────────────────┬───────────────────────────────┘
                          │ (L1 MISS)
                          ▼
┌─────────────────────────────────────────────────────────┐
│  L2: Redis 持久化缓存                                    │
│  TTL: 24h | Key: "emb_cache:{sha256}"                   │
│  Value: msgpack 编码的 []float64                         │
│                                                         │
│  GET emb_cache:e7c8d9... → HIT!                          │
│  → 反序列化 → 回填 L1 → 返回                            │
│                                                         │
│  (若也 MISS → 调用 Embedding API → 回填 L1 + L2)        │
└─────────────────────────────────────────────────────────┘
```

## 5. Graph RAG 知识图谱

### 5.1 实体提取流程

```
文档内容 → LLM EntityExtractor
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  LLM Prompt (System):                                    │
│  "你是一个知识图谱构建专家。从给定文本中提取实体和关系。  │
│   输出JSON: {entities:[{name,type,properties}],          │
│              relations:[{source,target,relation,          │
│                         properties}]}"                   │
│                                                         │
│  输入: "Kubernetes 的 Service 通过 Label Selector        │
│        将流量路由到对应的 Pod..."                         │
│                                                         │
│  输出:                                                   │
│  {                                                      │
│    "entities": [                                        │
│      {"name":"Kubernetes", "type":"Technology"},         │
│      {"name":"Service", "type":"Concept"},               │
│      {"name":"Pod", "type":"Concept"},                   │
│      {"name":"Label Selector", "type":"Mechanism"}       │
│    ],                                                   │
│    "relations": [                                       │
│      {"source":"Service", "target":"Pod",               │
│       "relation":"ROUTES_TO"},                          │
│      {"source":"Service", "target":"Label Selector",    │
│       "relation":"USES"},                               │
│      {"source":"Service", "target":"Kubernetes",        │
│       "relation":"BELONGS_TO"}                          │
│    ]                                                    │
│  }                                                      │
└─────────────────────────┬───────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────┐
│  Neo4j 存储:                                             │
│                                                         │
│  MERGE (n:Entity {name:$name})                           │
│  SET n.type=$type, n.source_file=$fileID                 │
│                                                         │
│  MATCH (a:Entity {name:$source}), (b:Entity {name:$target})│
│  MERGE (a)-[r:ROUTES_TO]->(b)                            │
│  SET r.source_file=$fileID                               │
│                                                         │
│  结果图谱:                                               │
│  (Kubernetes)──BELONGS_TO──>(Service)──ROUTES_TO──>(Pod) │
│                                     └──USES──>(LabelSelector)│
└─────────────────────────────────────────────────────────┘
```

### 5.2 图谱搜索

```
SearchByQuery("微服务架构和负载均衡的关系")
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  中文 2-gram 分词:                                       │
│  "微服务架构和负载均衡的关系"                             │
│  → 去停用词("和","的") → ["微服","服务","务架","架构",   │
│     "负载","载均","均衡","关系"]                          │
│                                                         │
│  Neo4j CONTAINS 查询:                                    │
│  MATCH (n:Entity)                                       │
│  WHERE toLower(n.name) CONTAINS "微服"                   │
│     OR toLower(n.name) CONTAINS "服务"                   │
│     OR toLower(n.name) CONTAINS "架构" ...              │
│  RETURN n LIMIT 10                                       │
│                                                         │
│  匹配实体: [微服务架构, 负载均衡, API网关, 消息队列]     │
│                                                         │
│  扩展关系 (depth=2):                                     │
│  MATCH (n)-[r]-(m) WHERE n.name IN [matched]            │
│  → 返回 entities + relations 子图                        │
└─────────────────────────────────────────────────────────┘
```

## 6. 异步索引流程 (TaskQueue + WorkerPool)

```
rag_index_document(async=true, content="大文档...")
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  TaskQueue.Submit()                                      │
│                                                         │
│  1. 生成 TaskID: "task-uuid-12345"                       │
│  2. 创建 IndexTask:                                      │
│     {TaskID, UserID:1, FileID:"doc001",                  │
│      Content:"...", Status:"pending",                    │
│      SubmittedAt: now()}                                 │
│                                                         │
│  3. SET rag:task:task-uuid-12345 {JSON} EX 86400        │
│  4. XADD rag:index:tasks * task_id "task-uuid-12345"    │
│                                                         │
│  → 立即返回 {task_id: "task-uuid-12345", status: "pending"}│
└─────────────────────────┬───────────────────────────────┘
                          │ (后台异步)
                          ▼
┌─────────────────────────────────────────────────────────┐
│  WorkerPool (N个goroutine)                               │
│                                                         │
│  Worker[0].Run():                                        │
│    loop {                                               │
│      XREADGROUP GROUP rag-workers worker-0               │
│        COUNT 1 BLOCK 5000 STREAMS rag:index:tasks >     │
│                                                         │
│      → 收到 task-uuid-12345                              │
│      → 更新状态: "processing"                            │
│      → 执行 IndexDocument(ctx, fileID, fileName, content)│
│                                                         │
│      ┌─ 成功:                                           │
│      │  更新状态: "completed", Result: IndexResult       │
│      │  NotifyWebhook(EventIndexComplete)                │
│      │  XACK rag:index:tasks rag-workers msgID          │
│      │                                                  │
│      └─ 失败:                                           │
│         更新状态: "failed", Error: errMsg                │
│         NotifyWebhook(EventIndexFailed)                  │
│         XACK ...                                        │
│    }                                                    │
└─────────────────────────────────────────────────────────┘

任务状态机:
  pending → processing → completed
                      → failed
```

## 7. 熔断器状态机

```
                  连续失败 ≥ threshold(5)
    ┌───────────┐ ────────────────────────> ┌──────────┐
    │  CLOSED   │                           │  OPEN    │
    │ (正常放行) │ <──────────────────────── │(全部拒绝)│
    └───────────┘    探针成功                └──────┬───┘
         ▲                                         │
         │          HalfOpenAfter(60s) 到期         │
         │           ┌──────────────┐              │
         └───────────│ HALF_OPEN    │◄─────────────┘
           探针成功   │(放行1个探针) │
                     └──────────────┘
                       探针失败 → 回到 OPEN

数据示例:
CircuitBreaker {
  State: CLOSED,
  FailureCount: 2,
  FailureThreshold: 5,
  HalfOpenAfter: 60s,
  LastFailureTime: 2026-04-02T10:30:00Z
}
```
