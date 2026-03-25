# RAG MCP Server — 面试问答集

> 基于项目实际代码与架构整理，按模块分类。每个问题包含：技术分析、流程图/代码要点、面试官直答版。

---

## 目录

1. [项目整体架构](#一项目整体架构)
2. [文档分块引擎](#二文档分块引擎)
3. [Embedding 管理器与高可用](#三embedding-管理器与高可用)
4. [向量存储与检索](#四向量存储与检索)
5. [混合检索与 Rerank 精排](#五混合检索与-rerank-精排)
6. [分布式异步索引](#六分布式异步索引)
7. [缓存设计](#七缓存设计)
8. [Graph RAG 知识图谱](#八graph-rag-知识图谱)
9. [MCP 协议与工具设计](#九mcp-协议与工具设计)
10. [工程质量与部署](#十工程质量与部署)

---

## 一、项目整体架构

### Q1: 请介绍一下这个项目的整体架构和数据流

**技术分析：**

项目采用分层架构，自顶向下分为三层：

```
┌──────────────────────────────────────────────────────────────┐
│                   MCP Client (Cursor / Claude)               │
│                    Streamable HTTP / JSON-RPC 2.0            │
└──────────────────────┬───────────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────────┐
│  Transport Layer:  server.go (MCP Server + Streamable HTTP)  │
├──────────────────────────────────────────────────────────────┤
│  Tools Layer:      tools/rag_tools.go (12 个 MCP Tool)       │
│                    tools/registry.go  (Registry 模式注册)     │
│                    tools/prompts.go   (Prompt 模板)          │
│                    tools/resources.go (Resource 资源)         │
├──────────────────────────────────────────────────────────────┤
│  Domain Layer:     rag/retriever.go       (检索编排)         │
│                    rag/chunking*.go       (4种分块策略)       │
│                    rag/embedding_manager  (多Provider调度)    │
│                    rag/embedding_cache    (L1+L2二级缓存)     │
│                    rag/store_redis.go     (向量存储)          │
│                    rag/retriever_reranker (Rerank精排)        │
│                    rag/worker_*.go        (异步任务队列)       │
│                    rag/graph_*.go         (知识图谱)          │
├──────────────────────────────────────────────────────────────┤
│  Infrastructure:   Redis Stack / Neo4j / Milvus / Qdrant     │
└──────────────────────────────────────────────────────────────┘
```

**核心数据流（索引 + 检索）：**

```
索引流程:
  文档 → ParseDocument(格式检测) → 分块策略选择(代码/语义/结构/固定)
       → 缓存去重 → 分批Embedding → EnsureIndex(惰性建索引)
       → Pipeline BatchUpsert → Redis Hash

检索流程:
  Query → [Multi-Query变体生成] → [HyDE假想文档] → Embedding
        → 向量/混合检索 → [上下文压缩] → [Rerank精排]
        → RetrievalResult[]
```

**配置层采用"两层配置"架构：** TOML 层（ServerConfig）直接映射文件结构；领域层（rag.XxxConfig）是运行时配置。两层之间通过 To*Config() 方法桥接，TOML 变更不侵入业务逻辑。

**面试直答：**

> 这个项目是一个生产级的 RAG MCP Server，整体采用三层架构：传输层通过 Streamable HTTP 暴露 MCP 协议接口；工具层通过 Registry 模式注册了 12 个 MCP Tool，处理参数校验和响应序列化；领域层是核心，包含检索器、分块引擎、Embedding 管理器、向量存储等组件。配置采用两层设计——TOML 反序列化层和领域配置层通过转换方法桥接，解耦配置格式与业务逻辑。数据流上，索引时走"解析→分块→向量化→写入"，检索时走"查询扩展→向量化→混合检索→精排"。整个系统支持多租户隔离，通过索引名模板为每个用户创建独立的向量索引。

---

### Q2: 项目启动时的组件初始化顺序是怎样的？为什么这个顺序很重要？

**技术分析：**

```
启动顺序 (有严格依赖关系):
  1. LoadConfig          → 加载 TOML + 环境变量替换 + 校验
  2. createRedisClient   → 创建 Redis 客户端 (3种模式)
  3. InitEmbeddingManager → 多 Provider 管理器
  4. InitCache           → L1 LRU + L2 Redis (依赖 Redis 客户端)
  5. InitReranker        → Rerank 精排器
  6. InitGraphRAG        → Neo4j + 实体提取器 (可选)
  7. TaskQueue + Worker  → 异步索引 (依赖 Redis + Store)
  8. Migrator            → Schema 版本迁移检查
  9. StartServer         → 启动 MCP HTTP

关闭顺序 (与启动相反):
  1. IndexWorker.Stop()  → 等待 in-flight 任务完成
  2. Manager.Stop()      → 停止健康检查协程
  3. RedisClient.Close() → 最后关闭连接池
```

启动顺序的关键约束：Cache 依赖 Redis 客户端做 L2 缓存；Worker 依赖 Store 和 EmbeddingManager 做索引；Migrator 依赖 Store 做 Schema 检查。关闭时必须先停 Worker（等 in-flight 任务）再关 Redis，否则正在处理的任务会因连接断开而失败。

**面试直答：**

> 启动顺序严格按依赖关系编排：先加载配置，再创建 Redis 连接，然后初始化 Embedding Manager、缓存、Reranker 等有状态服务，接着启动异步 Worker 和 Schema 迁移检查，最后启动 HTTP 服务。这个顺序不能乱，比如缓存的 L2 层依赖 Redis 客户端，Worker 依赖 Store 和 Embedding Manager。关闭时反向执行：先停 Worker 等待正在处理的任务完成，再停 Manager 的健康检查协程，最后关闭 Redis 连接池，确保不会丢失正在处理的数据。

---

## 二、文档分块引擎

### Q3: 项目中实现了哪些分块策略？它们之间的选择逻辑是什么？

**技术分析：**

项目实现了 4 种分块策略，按优先级降级选择：

```
分块策略选择链（IndexDocument 中）:

  输入文档
    │
    ├──① 代码感知分块 (CodeChunking)
    │   条件: code_chunking_enabled && DetectCodeLanguage() != ""
    │   特点: 按函数/类边界切分，正则匹配定义行+花括号/缩进匹配函数体
    │   支持: Go/Python/JS/TS/Java/Rust/C/C++ (8种语言)
    │
    ├──② 语义分块 (SemanticChunking)
    │   条件: semantic_chunking.enabled && ①未命中
    │   特点: 句子分割→滑动窗口Embedding→余弦相似度→断点检测(mean-k*std)
    │   代价: 需额外 Embedding API 调用
    │
    ├──③ 结构感知分块 (StructureAwareChunk)
    │   条件: structure_aware && 检测为Markdown && 有标题层级
    │   特点: 按 # 标题层级切分，表格作为原子单元不拆分
    │
    └──④ 固定窗口分块 (ChunkDocument) ← 兜底
        流水线: splitText(递归字符分割) → mergeSmallChunks → addOverlap
```

**固定窗口分块的三阶段流水线细节：**
1. **递归字符分割**：按分隔符优先级（\n\n → \n → 。→ . → 空格）逐级尝试，在段落边界优先切分
2. **小块合并**：< MinChunkSize 的碎片合并到相邻块，避免产生低质量检索单元
3. **重叠注入**：相邻块间注入 OverlapSize 个字符的重叠，确保块边界处内容不丢失

**父子块模式（Parent-Child Chunking）：**

```
父块(1000字符) → 子块1(200字符) + 子块2(200字符) + ...

Embedding 使用子块文本（粒度细，匹配精确）
Redis 存储父块文本（上下文完整，返回给 LLM）
通过 parent_chunk_id 做去重：多个子块命中同一父块时只保留最佳匹配
```

**面试直答：**

> 项目实现了四种分块策略，按优先级降级选择。第一优先是代码感知分块，通过正则匹配函数定义行加花括号匹配找到函数体边界，支持 8 种编程语言。第二是语义分块，将文本分句后用滑动窗口计算相邻句子的 Embedding 余弦相似度，在相似度骤降处（低于均值减 k 倍标准差）切分，保证每个 chunk 内部语义高度一致。第三是 Markdown 结构感知分块，按标题层级切分，表格作为原子单元不拆分。最后兜底是固定窗口分块，走递归字符分割、小块合并、重叠注入三阶段流水线。此外还支持父子块模式——用子块做向量匹配保证精确度，但返回父块内容保证上下文完整。

---

### Q4: 语义分块的算法细节是什么？断点检测的原理？

**技术分析：**

```
语义分块算法流程:

  Step 1: 句子分割 (按中英文句号/问号/感叹号)
     ↓
  Step 2: 构造滑动窗口 (window_size=3 个句子拼接)
     "S1 S2 S3", "S2 S3 S4", "S3 S4 S5", ...
     ↓
  Step 3: 分批计算窗口 Embedding (batch_size=10, DashScope限制)
     ↓
  Step 4: 计算相邻窗口余弦相似度
     sim[i] = cosine(embed[i], embed[i+1])
     ↓
  Step 5: 断点检测 (均值-标准差法)
     threshold = mean(sim) - k * std(sim)
     断点 = {i | sim[i] < threshold}
     ↓
  Step 6: 按断点切分 → 大 chunk 二次切割 + 小 chunk 合并邻居
```

断点检测使用统计方法而非固定阈值，因为不同文档的语义变化幅度不同。k=1.0 表示相似度低于均值一个标准差的位置被视为语义转折点。窗口大小为 3 个句子，是因为单句太短语义稀疏，窗口聚合后信号更强。

**面试直答：**

> 语义分块的核心是在相邻句子语义骤降处切分。首先将文本按句号等标点分句，然后用滑动窗口（默认 3 个句子）拼接后计算 Embedding，得到每个窗口的向量表示。接着计算相邻窗口的余弦相似度序列，用均值减 k 倍标准差作为阈值，低于阈值的位置就是语义断点。这里用统计方法而非固定阈值，是因为不同文档的语义变化幅度不同，统计方法能自适应。最后按断点切分，过大的组二次切割，过小的组合并到邻居。窗口大小选 3 是因为单句语义信号太弱，3 句聚合后更稳定。

---

## 三、Embedding 管理器与高可用

### Q5: Embedding Manager 的熔断器是如何设计的？为什么每个 Provider 独立熔断？

**技术分析：**

```
熔断器状态机 (每个 Provider 独立):

  ┌─────────┐  连续失败 ≥ 阈值(5)   ┌──────────┐  冷却超时(30s)  ┌───────────┐
  │ Closed  │ ────────────────────→  │   Open   │ ──────────────→ │ HalfOpen  │
  │(正常放行)│                        │(快速拒绝) │                  │(限流探测)  │
  └────┬────┘                        └──────────┘                  └─────┬─────┘
       ↑                                    ↑                           │
       │              探测失败 → 回到 Open   │                           │
       │                                    └───────────────────────────┘
       │                                                                │
       └──────────────── 探测成功 → 恢复 Closed ────────────────────────┘

关键设计决策:
  - 成功一次即清零 consecutiveFails → 防止偶发失败误触发熔断
  - HalfOpen 限制探测数 ≤ 3 → 避免对刚恢复的后端造成瞬时压力
  - 后台健康检查只探测非 Closed 的 Provider → 正常运行时零 API 开销
```

每个 Provider 独立熔断而非全局熔断的原因：如果 A 后端故障，全局熔断会导致健康的 B 后端也无法服务。独立熔断确保 A 故障时流量自动转移到 B，实现真正的故障隔离。

**重试策略：指数退避 + 随机抖动**

```
delay(n) = min(base × multiplier^n, max_delay) + rand(0, delay/4)

示例: base=1s, multiplier=2.0
  attempt 0: 1s + jitter
  attempt 1: 2s + jitter
  attempt 2: 4s + jitter (若 max_delay=30s 则钳制)

抖动作用: 防止多客户端同时重试导致惊群效应
```

**并发安全模型：**
- Manager.mu (RWMutex) — 保护 providers 列表
- Provider.mu (RWMutex) — 保护每个 Provider 的熔断状态和统计
- rIndex (atomic) — RoundRobin 热路径，无锁递增

**面试直答：**

> 熔断器是经典的三态状态机：Closed 正常放行、Open 快速拒绝、HalfOpen 有限探测。每个 Provider 拥有独立的熔断器，互不影响。连续 5 次失败触发 Open，30 秒冷却后进入 HalfOpen 允许最多 3 个探测请求，成功一次即恢复 Closed。之所以独立熔断，是因为全局熔断会导致一个后端故障就中断所有服务，独立熔断确保故障隔离、流量自动转移。重试采用指数退避加随机抖动，避免多客户端同步重试的惊群效应。后台健康检查只对非 Closed 状态的 Provider 发探测请求，正常运行时零额外 API 开销。

---

### Q6: 四种负载均衡策略分别适用什么场景？Priority 策略的降级逻辑？

**技术分析：**

```
四种策略:
  ┌──────────────┬──────────────────────────────────────────────────┐
  │ Strategy     │ 适用场景                                         │
  ├──────────────┼──────────────────────────────────────────────────┤
  │ Priority     │ RAG 主备场景：主用高质量模型，故障时自动降级备用  │
  │ RoundRobin   │ 多个等价 Provider，均匀分摊流量                  │
  │ Weighted     │ 不同 Provider 性能/配额不同，按权重分流           │
  │ Random       │ 简单随机选择，适合无差异的多实例                  │
  └──────────────┴──────────────────────────────────────────────────┘

Priority 降级逻辑:
  providers 按 priority 升序排列 (数值小 = 优先级高)
  attempt=0 → 选 providers[0] (最高优先级，如 DashScope)
  attempt=1 → 选 providers[1] (次优先级，如 OpenAI Fallback)
  attempt=2 → 选 providers[2] (若存在)
  所有都试过 → 回到 providers[0] 重试
```

项目默认使用 Priority 策略，因为 RAG 场景通常有明确的主备关系：DashScope text-embedding-v4（1024 维、成本低）作为主 Provider，OpenAI text-embedding-3-small（1536 维）作为备用。

**面试直答：**

> 项目支持四种负载均衡策略。默认使用 Priority 策略，因为 RAG 场景通常有主备关系——主用 DashScope 模型成本低延迟低，OpenAI 作为备用。Priority 策略下，attempt=0 选最高优先级 Provider，失败后 attempt=1 自动降级到次优先级，实现逐级降级。RoundRobin 适合多个等价 Provider 均匀分摊流量；Weighted 适合 Provider 性能不同按权重分流；Random 最简单适合无差异场景。所有策略都配合熔断器工作，熔断中的 Provider 会被自动跳过。

---

## 四、向量存储与检索

### Q7: VectorStore 的抽象设计是怎样的？如何支持多种向量数据库后端？

**技术分析：**

```
设计模式: 依赖倒置 (DIP) + 工厂模式

              ┌──────────────┐
              │  VectorStore │ ← interface
              │  (interface) │
              └──────┬───────┘
         ┌───────────┼───────────────┐
         ▼           ▼               ▼
  ┌──────────┐ ┌──────────┐  ┌──────────┐
  │  Redis   │ │  Milvus  │  │  Qdrant  │
  │VectorStore│ │VectorStore│  │VectorStore│
  └──────────┘ └──────────┘  └──────────┘

VectorStore 接口方法:
  - EnsureIndex()    — 幂等创建索引
  - UpsertVectors()  — Pipeline 批量写入
  - SearchVectors()  — KNN 向量搜索
  - HybridSearch()   — 混合搜索 (向量 + BM25)
  - DeleteByFileID() — 按文件删除
  - ListDocuments()  — 文档聚合列表
  - GetDocumentChunks() — 导出文档分块
  - Close()
```

服务启动时通过 CreateVectorStore() 工厂方法根据 config.toml 的 ector_store.type 创建对应实现。上层的 tools 层和 retriever.go 只依赖 VectorStore 接口，切换后端无需修改任何上层代码。

**Redis 实现的关键设计：**
- 使用 UniversalClient 接口统一 Standalone/Sentinel/Cluster 三种模式
- EnsureIndex() 幂等设计：先 FT.INFO 探测，并发创建时捕获 "Index already exists" 错误
- UpsertVectors() 使用 Pipeline 批量写入，每批 500 条，减少网络 RTT
- 兼容 RESP2/RESP3 双协议格式解析

**面试直答：**

> 向量存储采用依赖倒置原则，定义了 VectorStore 接口，包含索引管理、批量写入、向量搜索、混合搜索、删除等核心方法。当前实现了 Redis、Milvus、Qdrant 三种后端。启动时通过配置驱动的工厂方法创建实例，上层代码只依赖接口，切换后端只需改配置。Redis 实现中，索引创建是幂等的，使用 Pipeline 批量写入减少网络开销，每批 500 条。搜索结果的解析兼容 RESP2 和 RESP3 两种协议格式，适配不同版本的 Redis。整个设计使得新增向量数据库后端只需实现接口并注册到工厂即可。

---

### Q8: FLAT 和 HNSW 两种索引算法的区别和选型依据？

**技术分析：**

```
  ┌──────────────┬────────────────────┬─────────────────────────┐
  │              │ FLAT (暴力搜索)     │ HNSW (近似最近邻)        │
  ├──────────────┼────────────────────┼─────────────────────────┤
  │ 时间复杂度    │ O(N) 线性扫描      │ O(log N) 图遍历         │
  │ 召回率       │ 100% 精确           │ 95-99% (可调)           │
  │ 额外内存     │ 0                  │ 图结构开销               │
  │ 构建时间     │ 0                  │ 较长 (需建图)            │
  │ 适用规模     │ < 10万向量          │ > 10万向量               │
  │ 关键参数     │ 无                 │ M=16, EF_CONSTRUCTION=200│
  └──────────────┴────────────────────┴─────────────────────────┘

HNSW 三个核心参数的权衡:
  M (每层最大连接数):      ↑ 提高召回率，↑ 内存开销
  EF_CONSTRUCTION (建图宽度): ↑ 提高索引质量，↑ 构建时间
  EF_RUNTIME (查询宽度):    ↑ 提高查询精度，↑ 查询延迟
```

项目默认使用 FLAT，因为大多数 RAG 场景的向量数量在万级到十万级，FLAT 够用且结果精确。当数据量超过百万时切换为 HNSW。HNSW 参数通过 etCfg.HNSWParams 指针传递，FLAT 时为 nil，下游据此判断索引类型。

**面试直答：**

> FLAT 是暴力搜索，O(N) 时间复杂度，结果 100% 精确，无额外内存开销，适合 10 万以下向量。HNSW 是层级可导航小世界图算法，O(log N) 查询，适合大规模数据但有 1-5% 的召回损失。项目默认用 FLAT，因为多数 RAG 场景数据量不大。HNSW 有三个关键参数：M 控制图连接度影响召回和内存，EF_CONSTRUCTION 控制建图质量影响构建时间，EF_RUNTIME 控制查询精度影响延迟。在代码中用指针类型 HNSWParams 区分——FLAT 时为 nil，HNSW 时填充具体参数。

---

## 五、混合检索与 Rerank 精排

### Q9: 混合检索是如何实现的？RRF 融合算法的原理？

**技术分析：**

```
混合检索流水线:

  用户查询 Query
    │
    ├──────────────────┐
    ▼                  ▼
  向量语义搜索       BM25 全文搜索
  (KNN TopK)        (TopK × 3 过采样)
    │                  │
    ▼                  ▼
  vectorResults     textResults
    │                  │
    └────────┬─────────┘
             ▼
       RRF 融合排序
      (mergeByRRF)
             │
             ▼
       最终 TopK 结果

RRF 算法公式:
  score(d) = Σ weight_i × 1 / (k + rank_i)

  其中:
  - k = 60 (RRF 标准常数，平滑排名差异)
  - weight_i = 向量权重 0.7 / 关键词权重 0.3
  - rank_i = 文档在第 i 路搜索中的排名
```

**为什么用 RRF 而非 CombSUM？**
向量距离（0~2）和 BM25 分数（0~∞）不在同一尺度，直接加权求和会被高量级的 BM25 分数主导。RRF 只依赖排名而非原始分数，天然规避了尺度不一致问题。

**降级策略：** 混合检索失败时（如 Redis 版本不支持全文搜索），自动降级为纯向量搜索，保证可用性。全文检索取 TopK×3 过采样是因为融合排序会重排结果，需要更大候选池才能保证最终 TopK 质量。

**面试直答：**

> 混合检索并行执行两路搜索：向量语义搜索捕获同义词和语义近似，BM25 全文搜索捕获精确关键词匹配，两者互补。融合使用 RRF 算法——只看排名不看分数，score = Σ weight × 1/(60+rank)。选择 RRF 而非直接加权求和，是因为向量距离和 BM25 分数量纲不同，直接相加会被高量级一方主导，RRF 只依赖排名完美规避了这个问题。同一文档被两路同时命中时分数叠加，排名提升。全文搜索取 TopK×3 过采样保证融合后候选池足够大。混合检索失败时优雅降级为纯向量搜索。

---

### Q10: 检索管线中 HyDE、Multi-Query、Rerank、上下文压缩各自的作用？

**技术分析：**

```
完整检索管线 (从左到右依次执行):

  用户原始 Query
       │
       ▼
  ┌─── Multi-Query ────┐  生成 N 个查询变体, 并发检索, RRF融合
  │ "K8s部署" →         │  提升召回率: 不同措辞覆盖不同文档
  │  "K8s集群搭建步骤"   │
  │  "容器编排平台部署"   │
  └────────┬───────────┘
           │ (若失败则跳过)
           ▼
  ┌─── HyDE ───────────┐  LLM 生成假想答案, 用假想答案做检索
  │ Query + 假想文档     │  提升召回率: 假想答案与目标文档更相似
  └────────┬───────────┘
           │ (超时预算不足则跳过)
           ▼
     向量/混合检索
           │
           ▼
  ┌─── 上下文压缩 ─────┐  两种模式:
  │ LLM: 提取相关信息   │  减少 token 消耗: 去除 chunk 中无关内容
  │ Embedding: 句子过滤  │
  └────────┬───────────┘
           │
           ▼
  ┌─── Rerank 精排 ────┐  外部 Rerank 模型二次排序
  │ 扩大召回 → 精排截取  │  提升精确度: 先多召回再精排
  └────────┬───────────┘
           │
           ▼
     最终 TopK 结果
```

**超时预算管理：** 每个阶段开始前检查剩余时间，Multi-Query 需 >15s，HyDE 需 >10s，上下文压缩需 >5s，不足时跳过该阶段，确保整体请求不会因 LLM 调用超时。

**面试直答：**

> 检索管线有四个可选增强阶段。Multi-Query 将查询扩展为多个语义变体并发检索后 RRF 融合，提升召回率——比如"K8s 部署"会生成"集群搭建步骤""容器编排指南"等变体。HyDE 用 LLM 生成假想答案，用假想答案做向量检索，因为假想答案与目标文档的语义更接近。上下文压缩有两种模式——LLM 压缩提取关键信息、Embedding 压缩按句子相似度过滤，减少送入 LLM 的 token 量。Rerank 是最后的精排阶段，先扩大召回量再用专业 Rerank 模型重排序取 TopN。每个阶段都有超时预算检查，时间不够就跳过，保证整体响应不超时。

---

## 六、分布式异步索引

### Q11: 基于 Redis Streams 的异步任务队列是如何设计的？如何保证"至少一次"语义？

**技术分析：**

```
分布式异步索引架构:

  MCP Client                   Redis Streams              Workers
  ──────────                   ─────────────              ───────
  index(async=true)
       │
       ▼
  Submit() ──XADD──→  rag:index:tasks ──XREADGROUP──→ Worker-0
       │               (消费者组:         ──XREADGROUP──→ Worker-1
       │                rag-workers)      ──XREADGROUP──→ Worker-2
       ▼
  返回 task_id          PEL (待确认列表)
                             │
                        超时 5min 未 ACK
                             │
                             ▼
                        XAUTOCLAIM ──→ 其他 Worker 接管

生命周期:
  Submit: 先写状态(Redis Hash) → 再 XADD 入队 (XADD失败则回滚状态)
  Consume: 先 claimStale(XAUTOCLAIM) → 再 XREADGROUP(阻塞拉取新消息)
  Process: UpdateStatus(processing) → IndexDocument → UpdateStatus(completed/failed)
  Ack: XACK 确认 → 从 PEL 移除
```

**"至少一次"保证机制：**
1. **消费者组**：XREADGROUP 保证消息只被组内一个 consumer 消费
2. **PEL（Pending Entry List）**：未 ACK 的消息保留在 PEL 中
3. **XAUTOCLAIM**：超过 ClaimTimeout（5min）未 ACK 的消息，被其他 Worker 自动认领重新处理
4. **先处理后 ACK**：只在 IndexDocument 成功/失败后才 ACK，确保不丢消息
5. **panic 恢复**：processTask 使用 defer/recover 捕获 panic，标记任务失败并 ACK

**多实例负载均衡：** 每个实例启动 N 个 Worker goroutine，consumer name = instanceID-PID-workerIndex，全局唯一。多实例通过同一消费者组竞争消费，自动实现负载均衡。

**面试直答：**

> 异步索引基于 Redis Streams 消费者组实现。客户端提交时先写任务状态到 Redis Hash，再 XADD 入队，XADD 失败则回滚状态保持一致。Worker 消费时先尝试 XAUTOCLAIM 认领超时未 ACK 的消息（处理前一个 Worker 崩溃的情况），再 XREADGROUP 阻塞拉取新消息。处理完成后才 XACK 确认，确保"至少一次"语义——消息不会在处理中丢失。超时 5 分钟未 ACK 的消息会被其他 Worker 自动接管。多实例通过同一消费者组竞争消费，consumer name 用实例 ID + PID + 序号保证全局唯一，自动实现跨实例负载均衡。

---

### Q12: Webhook 回调通知是如何设计的？安全性如何保证？

**技术分析：**

```
Webhook 通知流程:
  任务完成/失败 → NotifyWebhook() (fire-and-forget goroutine)
     │
     ▼
  构造 JSON payload (event/task_id/status/result)
     │
     ▼
  HMAC-SHA256 签名: sha256(secret, payload) → X-Webhook-Signature 头
     │
     ▼
  POST 到 webhookURL (timeout=10s)
     │
     ├── 2xx → 成功，退出
     ├── 4xx → 客户端错误，不重试
     └── 5xx → 指数退避重试 (1s, 2s, 4s, 最多3次)
```

安全设计：HMAC-SHA256 签名放在 X-Webhook-Signature: sha256=xxx 头中，接收方用相同密钥验证请求合法性，防止伪造回调。4xx 不重试避免无意义的重复请求（如 URL 配错），5xx 才重试因为是服务端临时故障。

**面试直答：**

> Webhook 通知在任务完成或失败时触发，用独立 goroutine 异步发送不阻塞主流程。安全性通过 HMAC-SHA256 签名保证——用预共享密钥对 payload 签名放在请求头中，接收方可验证请求合法性防止伪造。重试策略区分错误类型：4xx 客户端错误不重试避免无效请求，5xx 服务端错误用指数退避重试最多 3 次。这样既保证了可靠投递，又避免了对故障端点的无谓重试。

---

## 七、缓存设计

### Q13: 二级缓存的设计思路？LRU 缓存的实现细节？

**技术分析：**

```
二级缓存查询路径:

  文本 → SHA256(TrimSpace) → cache key
           │
           ▼
     L1: 本地 LRU Cache (进程内, 纳秒级)
           │ miss
           ▼
     L2: Redis Cache (跨实例共享, 毫秒级)
           │ miss                  │ hit
           ▼                      ▼
     Embedding API 调用 ←──── 回填 L1
           │
           ▼
     写入 L1 + L2

LRU 缓存数据结构:
  ┌──────────────────────┐
  │ HashMap (O(1) 查找)   │ ← key → *list.Element
  │ + 双向链表 (淘汰顺序) │ ← Front=最近使用, Back=最久未使用
  └──────────────────────┘

  Get: HashMap 定位 → TTL 过期检查 → 移到链表头部
  Put: 已存在则更新+移头部; 不存在则检查容量→淘汰尾部→插入头部
```

**过期策略：双重保障**
1. **惰性过期**：Get 时检查 TTL，过期条目立即删除
2. **后台清理**：每 5 分钟从链表尾部扫描，批量删除过期条目（最多 1000 个），释放长期不被 Get 的过期条目

**缓存 Key 选择 SHA256 而非原文的原因：**
- 原文可能很长（一个 chunk 上千字符），做 HashMap key 浪费内存且比较慢
- SHA256 固定 64 字符，碰撞概率极低（2^128 次操作才有 50% 概率碰撞）
- TrimSpace 保证首尾空白差异不产生不同 key

**面试直答：**

> 缓存采用两级设计：L1 是进程内 LRU 缓存，纳秒级延迟；L2 是 Redis 缓存，跨实例共享。查询时先查 L1，miss 则查 L2，L2 命中后回填 L1 避免下次再走网络。两级都 miss 才调 Embedding API，结果同时写入 L1 和 L2。LRU 用经典的双向链表加 HashMap 实现，O(1) 查找和淘汰。过期用惰性检查加后台清理双重保障。缓存 key 用 SHA256 哈希而非原文，因为原文可能上千字符，做 key 浪费内存且比较慢，SHA256 固定 64 字符碰撞概率极低。索引时还有去重优化——先查缓存，只对未命中的文本调 API，重复上传文档时大幅减少 API 调用。

---

## 八、Graph RAG 知识图谱

### Q14: Graph RAG 是如何集成到系统中的？与向量检索如何融合？

**技术分析：**

```
Graph RAG 数据流:

索引阶段:
  文档 → IndexDocument 完成 → extractAndStoreEntities (异步 goroutine)
     │
     ▼
  EntityExtractor.Extract(content) → entities[] + relations[]
     │                                     │
     ▼                                     ▼
  GraphStore.AddEntities()         GraphStore.AddRelations()

检索阶段 (rag_graph_search 工具):
  Query → SearchByEntity/SearchByQuery → GraphSearchResult
     │                                         │
     ├── merge_vector=true                      │
     │   ├── 创建 Retriever                     │
     │   ├── 向量检索                           │
     │   └── MergeGraphAndVectorResults() ← ────┘
     │         │
     │         ▼
     │   图谱上下文 (priority=最高) + 向量结果
     └── merge_vector=false
         └── 直接返回 GraphSearchResult
```

**融合策略：** 图谱的 ContextText 被包装成 RelevanceScore=1.0 的 RetrievalResult 插入到向量结果最前面，让 LLM 优先看到结构化的实体关系信息。

**实体提取器有两种实现：**
1. SimpleEntityExtractor：基于规则，从 Markdown 标题和 Go 代码中提取函数名、类名
2. LLMEntityExtractor：调用 LLM 提取通用实体和关系（更强大但成本更高）

**面试直答：**

> Graph RAG 在索引完成后异步提取实体和关系写入图存储。用独立 goroutine 和独立 context（10 分钟超时）执行，不阻塞索引响应。检索时通过 rag_graph_search 工具提供两种模式：按实体名搜索返回子图、按自然语言查询返回相关实体。可选与向量检索融合——图谱上下文作为最高优先级结果插入到向量结果前面，让 LLM 先看到结构化的实体关系再参考语义文档片段。实体提取器支持规则式和 LLM 式两种，规则式零 API 开销适合代码文档，LLM 式更通用但成本更高。图存储通过 GraphStore 接口抽象，当前有内存实现和 Neo4j 实现。

---

## 九、MCP 协议与工具设计

### Q15: MCP 协议是什么？项目中如何实现 MCP Server？

**技术分析：**

MCP（Model Context Protocol）是 Anthropic 提出的标准协议，定义了 AI 应用与外部工具/资源的交互方式。核心概念包括 Tool（工具调用）、Resource（资源读取）、Prompt（提示词模板）。

```
MCP 交互模型:

  AI Client (Cursor/Claude)
       │
       │  Streamable HTTP (JSON-RPC 2.0)
       ▼
  ┌─────────────────────────────────────────────────┐
  │ MCP Server (server.go)                          │
  │  ├── mcp-go/server (框架层)                      │
  │  └── Registry (注册中心)                         │
  │       ├── ToolProvider      → 12 个 MCP Tool     │
  │       ├── ResourceProvider  → 动态文档资源         │
  │       ├── PromptProvider    → 3 个提示词模板       │
  │       └── ResourceTemplateProvider               │
  └─────────────────────────────────────────────────┘

Registry 注册模式:
  provider 实现 ToolProvider/PromptProvider/ResourceProvider 等接口
  RegisterProvider() 自动类型检测，同一对象可同时提供多种能力
  ApplyToServer() 一次性将所有注册内容应用到 MCPServer
```

**12 个 MCP Tool：**
- ag_search — 向量语义检索
- ag_index_document — 文档索引（同步/异步）
- ag_index_url — 网页抓取并索引
- ag_build_prompt — 构建 RAG Prompt
- ag_chunk_text — 文档分块预览
- ag_status — 系统健康状态
- ag_delete_document — 删除文档
- ag_parse_document — 文档解析
- ag_task_status — 异步任务状态（仅在启用时暴露）
- ag_list_documents — 文档列表
- ag_export_data — 数据导出
- ag_graph_search — 图谱检索（仅在启用时暴露）

**面试直答：**

> MCP 是 Model Context Protocol，Anthropic 提出的 AI 应用与外部工具交互的标准协议，基于 JSON-RPC 2.0。项目中使用 mcp-go 框架，通过 Streamable HTTP 传输层暴露服务。核心设计是 Registry 注册中心——Provider 实现 ToolProvider、ResourceProvider、PromptProvider 等接口后统一注册，ApplyToServer 一次性应用到 MCP Server。工具按需暴露：比如异步索引未启用时不暴露 task_status 工具，Graph RAG 未启用时不暴露 graph_search 工具，避免客户端误调用。一共暴露了 12 个工具覆盖索引、检索、管理、诊断等完整功能。

---

### Q16: 工具层的错误处理设计？为什么定义了 18 个错误码？

**技术分析：**

```
三层错误信息设计:

  Code (机器可读)  → RAG_001 ~ RAG_018 (string 类型)
  Message (通用描述) → "Embedding generation failed"
  Detail (现场上下文) → "embed query: timeout after 30s"

错误码分组:
  001-006: 核心读写 (索引/embedding/检索/输入/内容过大)
  007-009: Provider 可用性 (无provider/超时/熔断)
  010-012: 辅助功能 (重排序/解析/缓存)
  013-018: 系统级 (配置/文档不存在/批量/混合/格式/就绪)

RAGError 实现:
  - Error() → "[RAG_002] Embedding generation failed: embed query (caused by: timeout)"
  - Unwrap() → 返回 Cause，支持 errors.Is/errors.As 错误链遍历
  - tools 层直接格式化 RAGError 返回给 MCP 客户端
```

错误码使用 string 而非 int 类型，是因为 JSON 序列化时直接输出 "RAG_001" 可读文本，客户端无需维护数字映射表。错误链设计让上层可以 errors.Is(err, context.DeadlineExceeded) 识别是否超时，而不仅仅看到 "embedding failed"。

**面试直答：**

> 错误体系采用三层设计：Code 是机器可读的错误码如 RAG_001，Message 是通用描述，Detail 是本次调用的现场信息。定义 18 个错误码覆盖核心读写、Provider 可用性、辅助功能、系统级四个故障域。错误码用 string 而非 int，JSON 序列化时直接输出可读文本。RAGError 实现了 Unwrap 方法支持 Go 标准的错误链遍历，上层可以用 errors.Is 沿链查找根因——比如判断 embedding 失败到底是超时还是网络错误。tools 层直接将 RAGError 格式化为 MCP 错误响应，统一了整个系统的错误处理路径。

---

## 十、工程质量与部署

### Q17: 多租户隔离是如何实现的？

**技术分析：**

```
多租户隔离模型:

  User A (ID=1)                    User B (ID=2)
     │                                │
     ▼                                ▼
  索引名: mcp_rag_user_1:idx        索引名: mcp_rag_user_2:idx
  Key前缀: mcp_rag_user_1:          Key前缀: mcp_rag_user_2:
     │                                │
     ▼                                ▼
  Redis Keys:                       Redis Keys:
  mcp_rag_user_1:chunk_abc          mcp_rag_user_2:chunk_xyz
  mcp_rag_user_1:chunk_def          mcp_rag_user_2:chunk_uvw

  同一用户多知识库:
  mcp_rag_user_1_mylib:idx          (collection="mylib")
  mcp_rag_user_1_mylib:chunk_abc
```

隔离通过三个机制实现：
1. **索引名模板** ag_user_%d:idx：每用户独立的 RediSearch 索引
2. **Key 前缀模板** ag_user_%d:：FT.CREATE 的 PREFIX 参数限定索引作用范围
3. **Collection 后缀**：同一用户下多知识库的二级隔离

冒号分隔符兼容 Redis Cluster Hash Tag {user_1}，确保同一用户的索引和数据落在相同 slot。

**面试直答：**

> 多租户隔离通过三个维度实现：每个用户有独立的 RediSearch 索引（通过索引名模板生成如 rag_user_1:idx）、独立的 Key 前缀限定索引作用范围、以及 collection 后缀支持同一用户下多个知识库。所有 chunk 的 Redis Key 都以用户前缀开头，索引创建时通过 PREFIX 参数只索引该前缀下的数据。这样用户 A 的检索绝不会命中用户 B 的数据。Key 中的冒号分隔符还兼容 Redis Cluster 的 Hash Tag 机制，保证同一用户的数据落在相同 slot。

---

### Q18: Schema 版本化迁移是怎么做的？如何保证零停机？

**技术分析：**

```
蓝绿迁移流程:

  当前在线:  rag_user_1:idx (v1, FLAT)
                │
                ▼
  Step 1: 创建新索引  rag_user_1:idx_v2 (v2, HNSW)
  Step 2: SCAN + Pipeline COPY 数据 (旧索引在线不受影响)
  Step 3: FT.DROPINDEX rag_user_1:idx DD (DD=仅删索引定义,保留数据)
  Step 4: FT.ALIASADD rag_user_1:idx → rag_user_1:idx_v2
  Step 5: 更新 schema 元信息
```

蓝绿迁移确保零停机：整个过程中旧索引始终在线可查询，Step 4 的别名切换是原子操作（毫秒级），切换后所有查询自动路由到新索引。

**迁移触发条件检查：** 启动时 MigrateAllOnStartup SCAN 所有 schema 元数据 key，对比版本号和算法类型，需要迁移时才执行。

**面试直答：**

> Schema 迁移采用蓝绿部署模式保证零停机。先在新前缀下创建新索引（比如从 FLAT 升级到 HNSW），然后 SCAN 加 Pipeline COPY 将数据复制过去——整个过程旧索引一直在线服务。复制完成后用 FT.DROPINDEX DD 删除旧索引定义但保留数据，再用 FT.ALIASADD 将原索引名指向新索引。别名切换是原子操作，毫秒级完成，上层代码完全无感知。启动时自动扫描 schema 元数据检查是否需要迁移，版本号或算法变更才触发。

---

### Q19: 项目中有哪些优雅降级的设计？

**技术分析：**

```
降级策略清单:

  ┌────────────────────────────┬────────────────────────────────┐
  │ 故障场景                    │ 降级策略                        │
  ├────────────────────────────┼────────────────────────────────┤
  │ EmbeddingManager 不可用     │ 降级为直连单一 Ark Embedder     │
  │ 混合检索失败                │ 降级为纯向量检索               │
  │ HyDE 超时/失败              │ 使用原始 query 继续检索         │
  │ Multi-Query 失败            │ 回退到单 query 检索             │
  │ 上下文压缩超时              │ 返回原始未压缩结果              │
  │ Rerank 失败                │ 回退到分数排序 (ScoreReranker)  │
  │ 结构感知分块失败             │ 回退到固定窗口分块              │
  │ 语义分块 Embedding 失败     │ 回退到固定窗口分块              │
  │ 异步索引 Worker panic       │ defer/recover 标记任务失败+ACK  │
  │ Webhook 发送失败            │ 指数退避重试，不影响主流程       │
  │ Schema 元信息保存失败        │ 仅警告日志，不影响索引创建      │
  │ 文档格式未知                │ 降级为纯文本处理               │
  │ Provider 单点故障            │ 熔断+故障转移到其他 Provider    │
  │ L2 Redis 缓存不可用         │ 仅使用 L1 本地缓存             │
  └────────────────────────────┴────────────────────────────────┘
```

**面试直答：**

> 系统中有十多处优雅降级设计，核心原则是"宁可质量降低也不中断服务"。比如 Embedding Manager 不可用时降级为直连单一 Embedder；混合检索的全文搜索失败时降级为纯向量检索；HyDE 和 Multi-Query 超时就跳过用原始查询；Rerank 失败回退到简单分数排序；结构感知和语义分块失败都回退到固定窗口分块；Worker 处理 panic 时通过 defer/recover 捕获并标记任务失败避免 goroutine 泄漏。每个可选增强阶段都有对应的降级路径，确保核心检索链路始终可用。

---

### Q20: Docker 部署方案是怎样的？有哪些运维考量？

**技术分析：**

```
docker-compose 服务拓扑:

  ┌─────────────────┐     ┌─────────────────┐     ┌───────────┐
  │ mcp-rag-server  │────→│  redis-stack     │     │  neo4j    │
  │ (Go 应用)        │     │ (RediSearch +    │     │ (可选)    │
  │ port: 8082→8083 │     │  RedisJSON)      │     │ 7474+7687│
  └─────────────────┘     │ port: 6380→6379  │     └───────────┘
                          │       8002→8001  │
                          └─────────────────┘
```

**运维要点：**
- **多阶段构建**：Builder 阶段编译 → Alpine 运行阶段仅含二进制和 CA 证书，镜像精简
- **健康检查**：每 30s wget POST 探测 MCP 端点，连续 3 次失败标记 unhealthy
- **依赖启动顺序**：service_healthy 条件确保 Redis 就绪后才启动应用
- **敏感信息**：API Key 通过环境变量注入，不写入镜像层
- **数据持久化**：redis_data 和 neo4j_data 使用命名卷

**面试直答：**

> Docker 部署使用多阶段构建——Go 编译阶段和 Alpine 运行阶段分离，最终镜像只含二进制文件和 CA 证书。docker-compose 编排三个服务：MCP Server、Redis Stack（含 RediSearch 模块）和可选的 Neo4j。启动顺序通过 depends_on + service_healthy 条件控制，确保 Redis 健康检查通过后才启动应用。敏感信息如 API Key 通过环境变量注入，配置文件只读挂载。数据通过命名卷持久化。健康检查每 30 秒探测一次 MCP 端点，连续 3 次失败标记为 unhealthy，方便编排系统自动重启。

---

## 附录：高频追问与应对

### Q21: 如果让你重新设计这个系统，你会做什么改进？

**面试直答：**

> 有几个方向可以改进。第一，当前的 Embedding 维度一致性检查只在 AddProvider 时做，如果中途换模型可能不兼容，可以引入维度版本化机制。第二，异步任务队列可以支持优先级——大文件的索引任务优先级低、实时查询相关的索引优先级高。第三，语义分块的额外 Embedding 调用成本可以通过引入轻量级本地模型来降低。第四，Schema 迁移的 COPY 阶段在数据量极大时会比较慢，可以引入增量迁移机制。第五，Graph RAG 的实体提取可以加入置信度评分，低置信度实体不入图。

---

### Q22: 系统的性能瓶颈在哪里？如何优化？

**面试直答：**

> 性能瓶颈主要在三个地方。第一是 Embedding API 调用延迟，这是整个链路最慢的环节，通过二级缓存和去重可以减少 60-80% 的 API 调用。第二是大文档索引时的分批向量化，通过 Pipeline 批量写入 Redis 减少网络 RTT，异步模式把索引从请求链路中完全移出。第三是混合检索时向量搜索和全文搜索是串行的，理论上可以改成并行执行再融合，进一步减少检索延迟。此外，检索管线中 HyDE、Multi-Query 等 LLM 调用的延迟通过超时预算管理来控制——时间不够就自动跳过，保证整体响应在可接受范围内。

---

### Q23: 为什么选择 Redis Stack 作为主向量数据库，而不是 Milvus 或 Qdrant？

**面试直答：**

> 选择 Redis Stack 有三个原因。第一是统一基础设施——Redis 同时承担向量存储、Embedding 缓存（L2 层）、异步任务队列（Streams）和任务状态存储四个角色，减少运维复杂度。第二是 RediSearch 模块原生支持混合检索——向量 KNN 搜索和 BM25 全文搜索在同一个引擎中完成，不需要跨系统协调。第三是延迟低——Redis 全内存架构，对 RAG 这种对延迟敏感的场景很合适。当数据量超过百万级需要专业向量数据库时，通过 VectorStore 接口可以无缝切换到 Milvus 或 Qdrant，这也是做接口抽象的意义。

---

### Q24: 并发安全是如何保证的？有哪些关键的锁设计？

**面试直答：**

> 系统中有四层并发安全设计。第一层是全局单例的读写锁——globalManager、globalCache、globalReranker 各自有 RWMutex 保护初始化和替换操作。第二层是 Manager 的 RWMutex 保护 providers 列表，读操作（GetStats、selectProvider）用 RLock 不阻塞写。第三层是每个 Provider 独立的 RWMutex 保护熔断器状态和统计计数器，两个 Provider 的并发调用互不干扰。第四层是 LRU 缓存的 Mutex 保护链表和 HashMap 的并发读写。RoundRobin 策略的 rrIndex 用 atomic 操作而非锁，因为它是热路径，atomic 保证 O(1) 无争用。整体设计原则是锁粒度尽量小，读多写少的场景用 RWMutex，热路径用 atomic。
