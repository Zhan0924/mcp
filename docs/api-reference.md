# MCP API 参考文档

## 1. MCP Tools (12 个)

### 1.1 `rag_search` — 向量语义检索

在用户 RAG 知识库中搜索与查询最相关的文档片段。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | string | ✅ | 搜索查询文本 |
| `user_id` | number | ✅ | 用户 ID |
| `top_k` | number | | 返回结果数量（默认 5，上限 100） |
| `file_ids` | string | | 限定文件 ID 列表（逗号分隔） |
| `min_score` | number | | 最低相关度阈值 0~1 |
| `rerank` | boolean | | 是否启用 Rerank 重排序 |
| `collection` | string | | 知识库集合名称 |

**数据流示例：**
```
请求: rag_search(query="K8s Service类型", user_id=1, top_k=3, rerank=true)

内部流程:
  1. rerank=true → recall topK 扩大到 3×3=9
  2. Retrieve(query, topK=9) → 9 个候选结果
  3. RerankResults(query, 9个结果, topK=3) → 重排后取 Top 3

返回:
[
  {"content":"## Service类型\n...", "file_id":"k8s001",
   "file_name":"k8s_guide.md", "relevance_score":0.92},
  {"content":"NodePort 允许...", "file_id":"k8s001",
   "relevance_score":0.85},
  {"content":"LoadBalancer 类型...", "file_id":"k8s001",
   "relevance_score":0.78}
]
```

### 1.2 `rag_index_document` — 文档索引

将文档分块、向量化并存入向量索引。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `file_id` | string | ✅ | 文件唯一标识 |
| `user_id` | number | ✅ | 用户 ID |
| `content` | string | | 文档内容（与 upload_id 二选一） |
| `upload_id` | string | | 上传文件 ID（大文件使用） |
| `file_name` | string | | 文件名 |
| `format` | string | | text/markdown/html/pdf/docx |
| `collection` | string | | 知识库集合名称 |
| `async` | boolean | | 异步索引（返回 task_id） |

**数据流示例：**
```
请求: rag_index_document(file_id="doc001", user_id=1,
       content="# 分布式系统\n## CAP定理\n...",
       file_name="distributed.md")

返回:
{
  "file_id": "doc001",
  "file_name": "distributed.md",
  "total_chunks": 12,
  "indexed": 12,
  "failed": 0,
  "cached": 8    ← 8个chunk的Embedding命中缓存
}
```

### 1.3 `rag_index_url` — 网页索引

抓取 URL 网页内容，自动提取正文，分块向量化。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `url` | string | ✅ | 网页 URL (http/https) |
| `user_id` | number | ✅ | 用户 ID |
| `file_id` | string | | 自定义文件 ID（默认 URL hash） |
| `file_name` | string | | 文件名（默认使用 URL） |
| `async` | boolean | | 异步索引 |

### 1.4 `rag_build_prompt` — 构建 RAG 提示词

自动检索相关文档，按文件分组构建包含上下文的提示词。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | string | ✅ | 用户问题 |
| `user_id` | number | ✅ | 用户 ID |
| `top_k` | number | | 上下文数量（默认 5） |
| `file_ids` | string | | 限定文件 ID 列表 |

**返回示例：**
```
你是一个知识丰富的AI助手。请根据以下参考文档回答用户问题。

## 📄 来源: k8s_guide.md

### 片段 1 (相关度: 92%)
Service 是 Kubernetes 中将运行在一组 Pod 上的应用程序
暴露为网络服务的抽象方法...

### 片段 2 (相关度: 85%)
NodePort 类型允许通过每个节点的固定端口访问服务...

---
## 用户问题
Kubernetes Service有哪些类型？
```

### 1.5 `rag_chunk_text` — 文档分块

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `content` | string | ✅ | 文档内容 |
| `max_chunk_size` | number | | 最大分块大小（默认 1000） |
| `min_chunk_size` | number | | 最小分块大小（默认 100） |
| `overlap_size` | number | | 重叠大小（默认 200） |
| `structure_aware` | boolean | | 结构感知分块（默认 true） |

### 1.6 `rag_status` — 系统状态

无参数。返回 Provider 健康状态、缓存命中率。

**返回示例：**
```json
{
  "status": "ok",
  "providers": [
    {"name":"primary-dashscope", "status":"active",
     "circuit_state":"closed", "success_rate_percent":99.5,
     "avg_latency_ms":120, "total_requests":1523}
  ],
  "cache": {
    "hits": 8432, "misses": 1201,
    "hit_rate_percent": 87.5,
    "local_size": 5000, "local_capacity": 10000
  }
}
```

### 1.7 `rag_delete_document` — 文档删除

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `file_id` | string | ✅ | 文件唯一标识 |
| `user_id` | number | ✅ | 用户 ID |

### 1.8 `rag_parse_document` — 文档解析

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `content` | string | ✅ | 文档内容（PDF/DOCX 为 base64） |
| `format` | string | | text/markdown/html/pdf/docx |

### 1.9 `rag_task_status` — 异步任务状态

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `task_id` | string | ✅ | 任务 ID |

### 1.10 `rag_list_documents` — 文档列表

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `user_id` | number | ✅ | 用户 ID |
| `collection` | string | | 知识库集合名称 |

### 1.11 `rag_export_data` — 数据导出

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `user_id` | number | ✅ | 用户 ID |
| `file_id` | string | | 导出指定文件的分块 |
| `collection` | string | | 知识库集合名称 |

### 1.12 `rag_graph_search` — 知识图谱搜索

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | string | ✅ | 搜索查询 |
| `search_type` | string | | entity / query（默认 query） |
| `depth` | number | | 图遍历深度 1-3（entity 模式） |
| `top_k` | number | | 结果数量（query 模式） |
| `merge_vector` | boolean | | 是否与向量检索融合 |
| `user_id` | number | | 融合检索时需要 |

---

## 2. MCP Resources (2 个)

### 2.1 `rag://status`
系统健康状态信息（embedding provider、缓存统计）。

### 2.2 `rag://docs/{user_id}`
用户知识库文档列表（URI 模板）。

---

## 3. MCP Prompts (2 个)

### 3.1 `rag_qa`
RAG 问答提示词模板，自动检索知识库并构建上下文。

| 参数 | 说明 |
|------|------|
| `query` | 用户问题 |
| `user_id` | 用户 ID |

### 3.2 `rag_summarize`
文档摘要提示词模板，检索文档后生成结构化摘要。

| 参数 | 说明 |
|------|------|
| `file_id` | 要摘要的文件 ID |
| `user_id` | 用户 ID |

---

## 4. HTTP 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/mcp` | POST | MCP Streamable HTTP (JSON-RPC over SSE) |
| `/health` | GET | 健康检查（Redis 连接、Provider 状态） |
| `/upload` | POST | 大文件上传（multipart/form-data） |
| `/metrics` | GET | Prometheus 指标 |

### 4.1 认证方式

```
# API Key 模式
Authorization: Bearer <api-key>
# 或 Header
X-API-Key: <api-key>

# JWT 模式
Authorization: Bearer <jwt-token>

# 配置格式 (AUTH_API_KEYS 环境变量)
"key1:user_id1,key2:user_id2"
```

白名单路径（无需认证）: `/health`, `/metrics`

---

## 5. 源码文件索引

```
rag/
├── retriever.go           # 核心检索器: IndexDocument, Retrieve
├── retriever_hyde.go       # HyDE 查询扩展
├── retriever_multiquery.go # Multi-Query 多查询检索
├── retriever_reranker.go   # Rerank 重排序
├── retriever_compressor.go # 上下文压缩
├── retriever_adapter.go    # LangChain-style 适配器
├── retriever_prompt.go     # RAG Prompt 构建
├── chunking.go             # 分块: 固定窗口/结构感知/父子块
├── chunking_semantic.go    # 语义分块
├── chunking_code.go        # 代码感知分块
├── embedding_manager.go    # 多Provider管理器(故障转移)
├── embedding_provider.go   # Provider 接口与实现
├── embedding_cache.go      # L1 LRU + L2 Redis 缓存
├── embedding_f32.go        # float64↔float32 转换工具
├── store_redis.go          # Redis VectorStore (FT.SEARCH)
├── store_milvus.go         # Milvus VectorStore (REST API)
├── store_qdrant.go         # Qdrant VectorStore
├── store_circuit_breaker.go# 熔断器装饰器
├── store_migration.go      # Schema 版本迁移
├── graph_rag.go            # Graph RAG 核心逻辑
├── graph_neo4j.go          # Neo4j 图存储
├── graph_extractor.go      # LLM 实体提取
├── worker_queue.go         # Redis Streams 任务队列
├── worker_pool.go          # Worker Pool 并发执行
├── stream_indexer.go       # 流式索引器
├── webhook.go              # Webhook 通知
├── parsing_engine.go       # 文档解析引擎
├── parsing_pdf.go          # PDF 解析
├── parsing_docx.go         # DOCX 解析
├── parsing_service.go      # 解析服务
├── upload_store.go         # 文件上传暂存
├── incremental.go          # 增量索引
├── tenant.go               # 多租户管理
├── search_cache.go         # 搜索结果缓存
├── validation.go           # 输入校验
├── errors.go               # 错误码定义
├── types.go                # 核心类型定义
├── config.go               # RAG 配置结构
└── retry.go                # 重试策略

tools/
├── rag_tools.go            # 12 个 MCP Tool 实现
├── registry.go             # Tool/Resource/Prompt 注册中心
├── resources.go            # MCP Resource 实现
└── prompts.go              # MCP Prompt 实现

middleware/
├── auth.go                 # API Key / JWT 认证
├── session_redis.go        # Redis Session 管理
├── prometheus.go           # Prometheus 指标中间件
├── tracing.go              # 分布式追踪
└── metrics.go              # 指标收集器
```
