# RAG MCP Server — 系统架构文档

> 版本: 2.0.0 | 语言: Go 1.21+ | 协议: MCP (Model Context Protocol) Streamable HTTP

## 1. 系统总览

RAG MCP Server 是一个企业级检索增强生成（Retrieval-Augmented Generation）服务，通过 MCP 协议向 AI 客户端（如 Cursor、Claude Desktop）暴露知识库管理能力。

```
┌─────────────────────────────────────────────────────────────────────┐
│                        MCP Client (Cursor / Claude)                 │
│                    POST /mcp (SSE Streamable HTTP)                  │
└──────────────────────────────┬──────────────────────────────────────┘
                               │ JSON-RPC over HTTP
                               ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    Enterprise Middleware Chain                       │
│  ┌──────────┐ ┌────────┐ ┌──────┐ ┌─────────┐ ┌───────┐          │
│  │Prometheus│→│Tracing │→│ Auth │→│RateLimit│→│ Audit │          │
│  └──────────┘ └────────┘ └──────┘ └─────────┘ └───────┘          │
└──────────────────────────────┬──────────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     MCP Server (mcp-go)                             │
│                                                                     │
│   ┌──────────────┐  ┌──────────────┐  ┌──────────────┐            │
│   │  12 Tools    │  │  Resources   │  │   Prompts    │            │
│   │ (rag_search  │  │ (rag://status│  │ (rag_qa /    │            │
│   │  rag_index.. │  │  rag://docs) │  │  summarize)  │            │
│   └──────┬───────┘  └──────────────┘  └──────────────┘            │
└──────────┼──────────────────────────────────────────────────────────┘
           │
           ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      RAG Pipeline (rag/)                            │
│                                                                     │
│  ┌─────────────┐  ┌───────────────┐  ┌──────────────────────────┐ │
│  │  Chunking   │  │  Embedding    │  │    Advanced Retrieval    │ │
│  │ (4 strategies│  │  Manager      │  │  ┌─────┐ ┌───────────┐ │ │
│  │  结构感知/   │  │ (多Provider  │  │  │HyDE │ │Multi-Query│ │ │
│  │  语义/代码/  │  │  故障转移)    │  │  └─────┘ └───────────┘ │ │
│  │  固定窗口)   │  │              │  │  ┌──────┐ ┌──────────┐  │ │
│  └─────────────┘  │  ┌──────────┐│  │  │Rerank│ │Compressor│  │ │
│                   │  │L1 LRU   ││  │  └──────┘ └──────────┘  │ │
│                   │  │L2 Redis ││  │  ┌──────────────────┐    │ │
│                   │  └──────────┘│  │  │   Graph RAG      │    │ │
│                   └───────────────┘  │  │  (Neo4j + LLM)   │    │ │
│                                      │  └──────────────────┘    │ │
│                                      └──────────────────────────┘ │
└────────────────────────┬────────────────────────────────────────────┘
                         │
            ┌────────────┼────────────────┐
            ▼            ▼                ▼
   ┌─────────────┐ ┌──────────┐   ┌───────────┐
   │   Milvus    │ │  Redis   │   │   Neo4j   │
   │ (向量存储)  │ │ Stack    │   │ (图数据库) │
   │ :19530      │ │ :6379    │   │ :7687     │
   └─────────────┘ └──────────┘   └───────────┘
```

## 2. 启动初始化序列

```
main()
  │
  ├── 1. LoadConfig("config.toml")
  │       → 解析 TOML → 替换 ${ENV_VAR} → Validate()
  │
  ├── 2. createRedisClient(cfg)
  │       → 支持 standalone / sentinel / cluster
  │       → 返回 redis.UniversalClient (统一接口)
  │
  ├── 3. InitEmbeddingManager(cfg)
  │       → 创建 Manager → 注册 Provider(s) → 启动健康检查
  │       │
  │       │  数据示例:
  │       │  Provider: {name:"primary-dashscope", model:"text-embedding-v3",
  │       │             dimension:1024, priority:1, weight:100}
  │       │
  │       └── manager.Start()  // 后台 goroutine: 健康检查 + QPS 统计
  │
  ├── 4. InitCache(cfg, redisClient)
  │       → L1: LRU(10000条, 30min TTL)
  │       → L2: Redis(24h TTL, prefix="emb_cache:")
  │
  ├── 5. InitReranker(cfg)
  │       → DashScope qwen3-rerank / gte-rerank-v2
  │
  ├── 6. InitCompressor(cfg)
  │       → LLM-based 或 Embedding-based 上下文压缩
  │
  ├── 7. InitGraphRAG(cfg)
  │       → Neo4j GraphStore + LLM EntityExtractor
  │       │  或 InMemory GraphStore + Simple Extractor
  │       │
  │       └── 失败时自动降级到 InMemory
  │
  ├── 8. NewTaskQueue(cfg, redisClient)
  │       → Redis Streams: key="rag:index:tasks", group="rag-workers"
  │       → 启动 N 个 IndexWorker goroutine
  │
  ├── 9. SchemaMigrator.AutoMigrate()
  │       → 检查索引 Schema 版本 → 自动蓝绿迁移
  │
  ├── 10. NewUploadStore(redisClient, uploadCfg)
  │        → 大文件暂存: disk_path="/tmp/rag-uploads", TTL=1h
  │
  └── 11. BuildServerFull() → srv.ListenAndServe()
          → 组装中间件链 → 注册路由 → 监听 :8083
```

## 3. 中间件链

请求处理顺序（洋葱模型，从外到内）：

```
HTTP Request
    │
    ▼
┌────────────────────┐
│ Prometheus Metrics  │  记录请求延迟/状态码/QPS
│ (最外层，记录全貌)  │
└────────┬───────────┘
         ▼
┌────────────────────┐
│ Distributed Tracing│  生成 TraceID，注入 Header
│ (X-Trace-ID)       │
└────────┬───────────┘
         ▼
┌────────────────────┐
│ Auth Middleware     │  API Key / JWT 验证
│ (skip: /health,    │  AUTH_API_KEYS="key:uid,..."
│  /metrics)         │
└────────┬───────────┘
         ▼
┌────────────────────┐
│ Rate Limiter       │  令牌桶算法 (默认 100 RPS)
│ (全局 + Per-IP)    │
└────────┬───────────┘
         ▼
┌────────────────────┐
│ Audit Logger       │  记录请求 Method/Path/Duration
│ (JSON 格式)        │
└────────┬───────────┘
         ▼
┌────────────────────┐
│  HTTP Mux Router   │
│ /mcp    → SSE      │
│ /health → JSON     │
│ /upload → Multipart│
│ /metrics→ Prometheus│
└────────────────────┘
```

## 4. 多租户隔离模型

```
User 1 (ID=1)                    User 2 (ID=2)
    │                                │
    ▼                                ▼
┌──────────────────────┐   ┌──────────────────────┐
│ Index: mcp_rag_user_1│   │ Index: mcp_rag_user_2│
│ Prefix: mcp_rag_     │   │ Prefix: mcp_rag_     │
│         user_1_      │   │         user_2_      │
│                      │   │                      │
│ Keys:                │   │ Keys:                │
│  mcp_rag_user_1_     │   │  mcp_rag_user_2_     │
│    chunk-abc123      │   │    chunk-xyz789      │
│  mcp_rag_user_1_     │   │                      │
│    chunk-def456      │   │                      │
└──────────────────────┘   └──────────────────────┘

Collection 隔离 (同一用户下多知识库):

User 1 + Collection "docs"          User 1 + Collection "code"
    │                                    │
    ▼                                    ▼
┌───────────────────────────┐  ┌───────────────────────────┐
│ Index: mcp_rag_user_1_docs│  │ Index: mcp_rag_user_1_code│
│ Prefix: mcp_rag_user_1_  │  │ Prefix: mcp_rag_user_1_  │
│         docs_             │  │         code_             │
└───────────────────────────┘  └───────────────────────────┘
```

## 5. Docker 服务编排

```yaml
services:
  mcp-rag-server:    # Go 主服务       → :8083
  redis-stack:       # Redis + Search  → :6380(host) / :6379(内网)
  milvus:            # 向量数据库      → :19530
  etcd:              # Milvus 依赖     → :2379
  minio:             # Milvus 对象存储 → :9000
  milvus-standalone: # Milvus 主进程
  neo4j:             # 图数据库        → :7474(HTTP) / :7687(Bolt)
  prometheus:        # 监控指标收集    → :9090
```

## 6. 配置结构总览

```toml
[server]        # 端口/名称/版本/TLS
[auth]          # API Key / JWT 认证
[redis]         # standalone / sentinel / cluster
[retriever]     # 索引模板/TopK/向量字段/混合检索权重/HNSW参数
[chunking]      # 分块大小/重叠/父子块/语义分块/代码分块
[embedding_manager]  # 多Provider策略/熔断/重试
[cache]         # L1 LRU + L2 Redis 缓存
[rerank]        # DashScope Reranker 配置
[hyde]          # HyDE 查询扩展 LLM
[multi_query]   # 多查询检索 LLM
[context_compressor] # 上下文压缩
[vector_store]  # redis / milvus / qdrant 后端选择
[async_index]   # Redis Streams 异步索引
[migration]     # Schema 版本迁移
[upload]        # 大文件上传
[graph_rag]     # 知识图谱 (Neo4j + LLM 实体提取)
[[embedding_providers]]  # Embedding Provider 列表（支持多个）
```
