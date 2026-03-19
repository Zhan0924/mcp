# RAG 子系统 — 文件组织结构

`rag` 包是 RAG (Retrieval-Augmented Generation) 子系统的核心实现。
文件按功能域使用**统一前缀命名**，便于快速定位和理解模块边界。

> **为什么不拆分为子目录/子包？**
> 本包内的类型和函数存在大量交叉引用（如 `retriever.go` 调用了解析、分块、
> 向量化、存储等几乎所有模块），强行拆分会导致 Go 不允许的循环依赖。
> 采用前缀命名是 Go 大型单包的标准组织模式（Go 标准库亦如此）。

---

## 📁 文件分组总览

```
rag/
├── ⚙️ 基础设施 (Infrastructure)
│   ├── config.go                 # 全局配置中心（默认值 + TOML 覆盖）
│   ├── types.go                  # 对外 API 边界类型（RetrievalResult / IndexResult / DeleteResult）
│   └── errors.go                 # 统一错误码体系（RAG_001 ~ RAG_018）
│
├── 📄 文档解析 (parsing_*)
│   ├── parsing_engine.go         # 多格式解析引擎、格式检测、结构感知分块、表格线性化
│   ├── parsing_pdf.go            # PDF 解析器（base64 → 逐页提取文本/表格）
│   ├── parsing_docx.go           # DOCX 解析器（ZIP → XML 解析段落/表格/图片）
│   └── parsing_service.go        # 统一文档预处理服务（解析 → 分块策略调度）
│
├── ✂️ 文档分块 (chunking*)
│   ├── chunking.go               # 固定窗口分块（滑动窗口 + 重叠 + 碎片合并）
│   ├── chunking_code.go          # 代码感知分块（按函数/类边界切分）
│   └── chunking_semantic.go      # 语义分块（Embedding 相似度断点检测）
│
├── 🧮 向量化 (embedding_*)
│   ├── embedding_manager.go      # 多 Provider 调度器（熔断 + 负载均衡 + 健康检查）
│   ├── embedding_provider.go     # Provider 工厂注册（Ark / OpenAI / Local）
│   └── embedding_cache.go        # 二级缓存（L1 本地 LRU + L2 Redis）
│
├── 🗄️ 向量存储 (store_*)
│   ├── store_redis.go            # VectorStore 接口定义 + Redis 实现（FT.CREATE / FT.SEARCH）
│   ├── store_milvus.go           # Milvus 向量数据库实现（RESTful API v2）
│   ├── store_qdrant.go           # Qdrant 向量数据库实现（RESTful API）
│   └── store_migration.go        # Redis 索引 Schema 版本化与蓝绿迁移
│
├── 🔍 检索与排序 (retriever*)
│   ├── retriever.go              # 核心检索器（分块→向量化→存储→检索 全流程编排）
│   ├── retriever_adapter.go      # JSON 适配器（Retriever → MCP Tool 层文本协议）
│   ├── retriever_reranker.go     # 重排序引擎（DashScope / Cohere / Jina / Score）
│   ├── retriever_hyde.go         # HyDE 查询扩展（假想文档生成）
│   ├── retriever_multiquery.go   # 多查询检索（查询变体 + RRF 融合）
│   ├── retriever_compressor.go   # 上下文压缩（LLM / Embedding 相似度）
│   └── retriever_prompt.go       # RAG Prompt 构建器（多文件分组展示）
│
├── 🕸️ 知识图谱 (graph_*)
│   ├── graph_rag.go              # Graph RAG 接口（Entity / Relation / GraphStore）
│   ├── graph_neo4j.go            # Neo4j 持久化图存储（Cypher 查询）
│   └── graph_extractor.go        # LLM 实体关系提取器（结构化三元组）
│
└── 🔄 异步任务 (worker_*)
    ├── worker_queue.go           # Redis Streams 任务队列（提交 / 消费 / ACK / 状态）
    └── worker_pool.go            # Worker Pool（多实例竞争消费 + 故障恢复）
```

---

## 🔗 模块依赖关系

```
                    ┌─────────────┐
                    │  config.go  │  types.go  errors.go
                    └──────┬──────┘
                           │ 配置注入
          ┌────────────────┼────────────────┐
          ▼                ▼                ▼
   ┌─────────────┐  ┌───────────┐  ┌──────────────┐
   │  parsing_*  │  │ chunking* │  │ embedding_*  │
   │  文档解析    │  │ 文档分块   │  │ 向量化管理    │
   └──────┬──────┘  └─────┬─────┘  └──────┬───────┘
          │               │               │
          └───────┬───────┘               │
                  ▼                       │
          ┌──────────────┐                │
          │   store_*    │◄───────────────┘
          │  向量存储     │
          └──────┬───────┘
                 │
                 ▼
          ┌──────────────┐     ┌────────────┐
          │  retriever*  │────►│  graph_*   │
          │  检索与排序   │     │  知识图谱   │
          └──────┬───────┘     └────────────┘
                 │
                 ▼
          ┌──────────────┐
          │  worker_*    │
          │  异步任务     │
          └──────────────┘
```

---

## 📝 命名约定

| 前缀 | 功能域 | 说明 |
|------|--------|------|
| `parsing_` | 文档解析 | 多格式解析（text/markdown/html/pdf/docx） |
| `chunking` | 文档分块 | 固定窗口 / 代码感知 / 语义分块 |
| `embedding_` | 向量化 | Embedding Provider 管理、缓存 |
| `store_` | 向量存储 | 存储抽象层 + 具体实现（Redis/Milvus/Qdrant） |
| `retriever` | 检索排序 | 核心检索流程 + 辅助组件 |
| `graph_` | 知识图谱 | Graph RAG 实体/关系存储与检索 |
| `worker_` | 异步任务 | 任务队列 + Worker Pool |
| *(无前缀)* | 基础设施 | 配置、类型定义、错误码 |
