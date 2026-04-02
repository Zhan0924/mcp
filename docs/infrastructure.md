# 基础设施与存储后端技术文档

## 1. 向量存储后端 (VectorStore 接口)

### 1.1 VectorStore 统一接口

```go
type VectorStore interface {
    EnsureIndex(ctx, IndexConfig) error              // 创建/确认索引
    UpsertVectors(ctx, []VectorEntry) (int, error)   // 批量写入
    SearchVectors(ctx, VectorQuery) ([]VectorSearchResult, error) // KNN 搜索
    HybridSearch(ctx, HybridQuery) ([]VectorSearchResult, error) // 混合搜索
    DeleteByFileID(ctx, indexName, prefix, fileID) (int64, error) // 删除
    GetDocumentChunks(ctx, indexName, prefix, fileID) ([]string, error)
    ListDocuments(ctx, indexName) ([]DocumentMeta, error)
    Close() error
}
```

### 1.2 Redis VectorStore (默认后端)

```
┌─────────────────────────────────────────────────────────┐
│  Redis Stack (redis/redis-stack:latest)                  │
│  内置 RediSearch 模块 (FT.* 命令族)                      │
│                                                         │
│  数据结构: Redis Hash                                    │
│  每个 chunk = 1 个 Hash Key:                             │
│                                                         │
│  HSET mcp_rag_user_1:chunk-abc123                       │
│    content    "Service 是 Kubernetes 中..."             │
│    file_id    "k8s001"                                  │
│    file_name  "k8s_guide.md"                            │
│    chunk_id   "chunk-abc123"                            │
│    chunk_index "0"                                      │
│    parent_chunk_id ""                                   │
│    vector     <4096 bytes>  ← 1024×float32 小端序       │
└─────────────────────────────────────────────────────────┘
```

**Redis 命令使用总览：**

| 命令 | 用途 | 示例 |
|------|------|------|
| `FT.CREATE` | 创建向量索引 | 见下方 Schema |
| `FT.INFO` | 检查索引是否存在 | `FT.INFO mcp_rag_user_1:idx` |
| `FT.SEARCH` | KNN/BM25/TAG 搜索 | 见检索示例 |
| `FT.AGGREGATE` | 文档聚合统计 | `GROUPBY @file_id REDUCE COUNT 0` |
| `HSET` | 写入 chunk 数据 | Pipeline 批量执行 |
| `DEL` | 删除 chunk | Pipeline 批量执行 |
| `SET/GET` | 任务状态存储 | `SET rag:task:{id} {json} EX 86400` |
| `XADD/XREADGROUP` | 异步任务队列 | Redis Streams |

**索引 Schema 定义：**
```
FT.CREATE mcp_rag_user_1:idx ON HASH
  PREFIX 1 mcp_rag_user_1:
  SCHEMA
    content         TEXT                          ← BM25 全文检索
    file_id         TAG                          ← 精确过滤
    file_name       TAG
    chunk_id        TAG
    chunk_index     NUMERIC SORTABLE             ← 分块排序
    parent_chunk_id TAG
    vector          VECTOR {ALGO} {PARAMS}       ← 向量索引
      TYPE FLOAT32
      DIM 1024
      DISTANCE_METRIC COSINE

支持算法:
  FLAT  — 暴力扫描, 适合 <10万向量
  HNSW  — 近似最近邻, 适合 >10万向量
    参数: M=16, EF_CONSTRUCTION=200, EF_RUNTIME=10
```

**KNN 向量检索命令：**
```
FT.SEARCH mcp_rag_user_1:idx
  "*=>[KNN 5 @vector $vec AS distance]"
  PARAMS 2 vec <binary_vector>
  SORTBY distance ASC
  RETURN 7 content file_id file_name chunk_id
           chunk_index parent_chunk_id distance
  DIALECT 2
  LIMIT 0 5

数据流:
  输入: 1024维 float32 向量 (4096 bytes)
  算法: COSINE 距离 (distance ∈ [0, 2])
  转换: score = 1 - distance/2 (∈ [0, 1])
  输出: Top-5 最相似 Hash Key + 字段值
```

**混合检索 (Hybrid Search)：**
```
第1路: 向量检索 (TopK×3 过采样)
  FT.SEARCH idx "*=>[KNN 15 @vector $vec AS distance]" ...

第2路: BM25 全文检索
  FT.SEARCH idx "@content:(Kubernetes Service 类型)"
    SORTBY __score ASC LIMIT 0 15

RRF 融合算法:
  对每个文档 d:
    score(d) = w_vec / (k + rank_vec(d)) + w_kw / (k + rank_kw(d))
  其中: w_vec=0.7, w_kw=0.3, k=60
  按 score 降序排列，截断到 TopK
```

### 1.3 Milvus VectorStore

```
┌─────────────────────────────────────────────────────────┐
│  Milvus 2.4+ (REST API v2)                               │
│  部署: milvus-standalone + etcd + minio                  │
│                                                         │
│  Collection Schema:                                      │
│    id              VarChar(256)  PRIMARY KEY             │
│    content         VarChar(65535)                        │
│    file_id         VarChar(256)                          │
│    file_name       VarChar(512)                          │
│    chunk_id        VarChar(256)                          │
│    chunk_index     Int32                                 │
│    parent_chunk_id VarChar(256)                          │
│    vector          FloatVector(1024) + COSINE Index     │
└─────────────────────────────────────────────────────────┘
```

**Milvus REST API 端点：**

| 操作 | API | 说明 |
|------|-----|------|
| 创建 Collection | `POST /v2/vectordb/collections/create` | 含 Schema + Index |
| 检查 Collection | `POST /v2/vectordb/collections/describe` | 幂等检查 |
| 写入数据 | `POST /v2/vectordb/entities/upsert` | 批量500条 |
| 向量搜索 | `POST /v2/vectordb/entities/search` | ANN + filter |
| 条件查询 | `POST /v2/vectordb/entities/query` | 标量过滤 |
| 删除数据 | `POST /v2/vectordb/entities/delete` | 按表达式删除 |

**安全防护：**
```
// sanitizeMilvusString 防止过滤表达式注入
// 输入: 'test" OR file_id == "hack'
// 清理后: 'test OR file_id == hack'
func sanitizeMilvusString(s string) string {
    s = strings.ReplaceAll(s, `\`, ``)
    s = strings.ReplaceAll(s, `"`, ``)
    s = strings.ReplaceAll(s, `'`, ``)
    return s
}
```

### 1.4 熔断器装饰器 (CircuitBreakerVectorStore)

```
┌─────────────────────────────────────────────────────────┐
│  CircuitBreakerVectorStore 包装任意 VectorStore          │
│                                                         │
│  每个操作执行前检查熔断状态:                              │
│                                                         │
│  func (s *CBVS) SearchVectors(ctx, q) {                 │
│    if !s.cb.Allow() {                                   │
│      return ErrCircuitOpen  // 熔断期间快速失败          │
│    }                                                    │
│    result, err := s.inner.SearchVectors(ctx, q)         │
│    if err != nil {                                      │
│      s.cb.RecordFailure()   // 累计失败计数              │
│    } else {                                             │
│      s.cb.RecordSuccess()   // 重置失败计数              │
│    }                                                    │
│    return result, err                                   │
│  }                                                      │
└─────────────────────────────────────────────────────────┘

状态转换:
  CLOSED ──[5次连续失败]──> OPEN ──[60s后]──> HALF_OPEN
  HALF_OPEN ──[探针成功]──> CLOSED
  HALF_OPEN ──[探针失败]──> OPEN
```

## 2. 异步索引系统

### 2.1 Redis Streams 任务队列

```
┌─────────────────────────────────────────────────────────┐
│  Stream Key: rag:index:tasks                             │
│  Consumer Group: rag-workers                             │
│                                                         │
│  提交任务:                                               │
│  XADD rag:index:tasks * task_id "task-uuid-12345"       │
│                                                         │
│  消费任务 (Worker):                                      │
│  XREADGROUP GROUP rag-workers worker-0                   │
│    COUNT 1 BLOCK 5000                                    │
│    STREAMS rag:index:tasks >                             │
│                                                         │
│  确认完成:                                               │
│  XACK rag:index:tasks rag-workers <message-id>          │
│                                                         │
│  任务状态存储:                                            │
│  SET rag:task:task-uuid-12345 {                          │
│    "task_id": "task-uuid-12345",                        │
│    "user_id": 1,                                        │
│    "file_id": "doc001",                                 │
│    "status": "processing",                               │
│    "submitted_at": "2026-04-02T10:30:00Z",              │
│    "started_at": "2026-04-02T10:30:01Z"                 │
│  } EX 86400  (24小时过期)                                │
└─────────────────────────────────────────────────────────┘
```

### 2.2 Worker Pool 生命周期

```
NewIndexWorker(cfg, redisClient, store, configs...)
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  启动 N 个 Worker goroutine (默认 N=3)                   │
│                                                         │
│  Worker[i].Run():                                        │
│    for {                                                │
│      select {                                           │
│      case <-ctx.Done():                                 │
│        return  // 优雅退出                               │
│      default:                                           │
│        msg := XREADGROUP(BLOCK 5s)                      │
│        if msg == nil { continue }                       │
│                                                         │
│        task := loadTask(msg.task_id)                    │
│        updateStatus("processing")                       │
│                                                         │
│        // 创建独立 Retriever + Context                   │
│        retriever := NewMultiFileRetriever(...)           │
│        result, err := retriever.IndexDocument(...)       │
│                                                         │
│        if err != nil {                                  │
│          updateStatus("failed", error=err.Error())      │
│          NotifyWebhook(EventIndexFailed, task)           │
│        } else {                                         │
│          updateStatus("completed", result=result)       │
│          NotifyWebhook(EventIndexComplete, task)        │
│          // Graph RAG 实体提取 (如果启用)               │
│          extractEntities(content, fileID)               │
│        }                                                │
│        XACK(msg.ID)                                     │
│      }                                                  │
│    }                                                    │
└─────────────────────────────────────────────────────────┘

优雅关闭:
  SIGTERM/SIGINT → cancel(ctx) → Workers 完成当前任务后退出
  → 清理: XACK 已处理消息, 关闭 Redis 连接
```

## 3. Webhook 通知

```
┌─────────────────────────────────────────────────────────┐
│  WebhookNotifier (异步发送, 自动重试)                    │
│                                                         │
│  事件类型:                                               │
│    index.complete  — 索引完成                            │
│    index.failed    — 索引失败                            │
│    doc.deleted     — 文档删除                            │
│    search.error    — 搜索异常                            │
│    health.changed  — 健康状态变化                        │
│                                                         │
│  发送流程:                                               │
│  1. 序列化事件为 JSON                                    │
│  2. HMAC-SHA256 签名: sign(payload, secret)              │
│  3. POST webhook_url                                    │
│     Header: X-Webhook-Signature: sha256=<hex>           │
│     Body: {type, timestamp, data}                       │
│  4. 失败自动重试 (最多 3 次, 指数退避)                   │
│                                                         │
│  数据示例:                                               │
│  {                                                      │
│    "type": "index.complete",                            │
│    "timestamp": "2026-04-02T10:35:00Z",                 │
│    "data": {                                            │
│      "task_id": "task-uuid-12345",                      │
│      "file_id": "doc001",                               │
│      "user_id": 1,                                      │
│      "total_chunks": 12,                                │
│      "indexed": 12                                      │
│    }                                                    │
│  }                                                      │
└─────────────────────────────────────────────────────────┘
```

## 4. Schema 迁移

```
┌─────────────────────────────────────────────────────────┐
│  SchemaMigrator (蓝绿部署迁移)                           │
│                                                         │
│  版本管理: Redis Key "rag:schema:version"                │
│                                                         │
│  迁移流程:                                               │
│  1. GET rag:schema:version → "v1"                        │
│  2. 比对目标版本 "v2"                                    │
│  3. 创建新索引 (v2 schema)                               │
│  4. 数据迁移: 从 v1 索引复制到 v2                        │
│  5. 原子切换: SET rag:schema:version "v2"                │
│  6. 异步清理旧 v1 索引                                   │
│                                                         │
│  回滚: 切换失败 → 保留 v1 索引不变                       │
└─────────────────────────────────────────────────────────┘
```

## 5. 文件上传暂存

```
大文件上传流程:

Client ──POST /upload (multipart)──> Server
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  UploadStore                                             │
│                                                         │
│  1. 生成 upload_id: "upload-uuid-67890"                  │
│  2. 保存文件到 disk: /tmp/rag-uploads/<upload_id>       │
│  3. 存储元数据到 Redis:                                  │
│     SET rag:upload:upload-uuid-67890                     │
│       {file_name, format, size, created_at}              │
│       EX 3600  (1小时过期)                               │
│  4. 返回 {upload_id: "upload-uuid-67890"}                │
│                                                         │
│  后续: rag_index_document(upload_id="upload-uuid-67890") │
│  → Load(upload_id) → 读取文件 → 索引 → 异步删除暂存文件  │
│                                                         │
│  自动清理: TTL 过期后 Redis Key 删除,                    │
│            后台 goroutine 定期清理磁盘文件                │
└─────────────────────────────────────────────────────────┘

Auto-Async 阈值:
  content > auto_async_threshold (默认 500KB)
  → 自动切换异步模式, 避免同步超时
```

## 6. 监控指标 (Prometheus)

```
┌─────────────────────────────────────────────────────────┐
│  自定义指标 (mcp_rag_ 前缀):                             │
│                                                         │
│  mcp_rag_request_duration_seconds{method,path}          │
│    — HTTP 请求延迟直方图                                 │
│                                                         │
│  mcp_rag_request_total{method,path,status}              │
│    — HTTP 请求计数器                                     │
│                                                         │
│  mcp_rag_embedding_duration_seconds{provider}           │
│    — Embedding API 调用延迟                              │
│                                                         │
│  mcp_rag_embedding_errors_total{provider}               │
│    — Embedding 错误计数                                  │
│                                                         │
│  mcp_rag_cache_hits_total / cache_misses_total          │
│    — 缓存命中/未命中计数                                 │
│                                                         │
│  mcp_rag_index_chunks_total{status}                     │
│    — 索引 chunk 计数 (success/failed)                   │
│                                                         │
│  mcp_rag_circuit_state{provider}                        │
│    — 熔断器状态 (0=closed, 1=half_open, 2=open)         │
└─────────────────────────────────────────────────────────┘

Prometheus 配置 (prometheus.yml):
  scrape_interval: 15s
  target: mcp-rag-server:8083/metrics
```

## 7. Docker Compose 服务依赖

```
┌─────────────┐
│ mcp-rag-    │──depends_on──┐
│ server:8083 │              │
└──────┬──────┘              │
       │                     │
       ├──────────────>┌─────▼─────┐
       │               │  Redis    │
       │               │  Stack    │
       │               │  :6379    │
       │               └───────────┘
       │
       ├──────────────>┌───────────┐    ┌───────┐   ┌───────┐
       │               │  Milvus   │◄───│ etcd  │   │ minio │
       │               │  :19530   │    │ :2379 │   │ :9000 │
       │               └───────────┘    └───────┘   └───────┘
       │
       ├──────────────>┌───────────┐
       │               │  Neo4j    │
       │               │  :7687    │
       │               └───────────┘
       │
       └──────────────>┌───────────┐
                       │Prometheus │
                       │  :9090    │
                       └───────────┘

健康检查:
  Redis:  redis-cli ping (interval=5s)
  Milvus: curl /v2/vectordb/collections/list (interval=30s)
  Neo4j:  cypher-shell "RETURN 1" (interval=30s)
```
