# RAG MCP Server — 简历项目描述

## 版本一：标准简历版（推荐，适合大多数求职场景）

**RAG MCP Server — 基于 MCP 协议的 RAG 检索增强生成服务**

**技术栈：** Go 1.24 · Redis Stack (RediSearch) · Neo4j · MCP (Model Context Protocol) · Streamable HTTP · Docker

**项目简介：** 独立设计并开发了一套生产级 RAG（检索增强生成）MCP Server，通过 Model Context Protocol 为 AI 应用（Cursor、Claude Desktop 等）提供文档索引、语义检索、Rerank 精排和 RAG Prompt 构建能力，暴露 12 个 MCP 工具供客户端调用。

**核心工作：**

- **多策略文档分块引擎**：实现 Markdown 结构感知分块、语义分块（基于 Embedding 相似度动态断点）、代码感知分块（支持 Go/Python/JS 等 8 种语言按函数边界切分）、父子块检索（子块精确匹配 + 父块完整上下文返回）等 4 种分块策略，智能降级选择
- **高可用 Embedding 管理器**：基于 Priority/RoundRobin/Weighted 等 4 种负载均衡策略调度多个 Embedding Provider，实现独立熔断器（Closed→Open→HalfOpen 三态状态机）、指数退避重试（含随机抖动防惊群）、后台主动健康检查；采用 L1 LRU 本地缓存 + L2 Redis 二级缓存，降低 API 调用成本
- **混合检索 + Rerank 精排**：实现向量语义检索 + BM25 全文关键词检索，通过 RRF（Reciprocal Rank Fusion）算法融合排序；集成 DashScope Rerank 模型进行二次精排，支持 HyDE 查询扩展和 Multi-Query 多查询变体检索提升召回率
- **向量存储抽象层**：面向接口设计 VectorStore 抽象，实现 Redis / Milvus / Qdrant 三种后端适配，支持 FLAT 和 HNSW 两种索引算法，兼容 RESP2/RESP3 双协议解析
- **分布式异步索引**：基于 Redis Streams 实现分布式任务队列，支持多实例消费者组竞争消费、超时任务自动 XCLAIM 认领、Webhook 回调通知（含 HMAC-SHA256 签名验证）
- **Graph RAG 知识图谱**：集成 Neo4j 图数据库，支持 LLM 实体提取器自动从文档中抽取实体和关系，实现多跳图谱检索并与向量检索结果融合
- **Schema 版本化迁移**：实现蓝绿索引迁移（新建→复制→别名切换），支持启动时自动检测并升级索引 Schema，实现零停机迁移
- **多租户隔离与生产部署**：通过索引名模板实现用户级数据隔离，支持 Redis Standalone/Sentinel/Cluster 三种部署模式；Docker 多阶段构建，完整的 docker-compose 编排

---

## 版本二：精简版（适合简历空间有限）

**RAG MCP Server — AI 检索增强生成服务** &emsp; *Go / Redis Stack / Neo4j / Docker*

独立设计开发生产级 RAG 服务，通过 MCP 协议为 AI 应用提供文档索引与语义检索能力：

- 实现 4 种智能分块策略（结构感知 / 语义 / 代码感知 / 父子块），向量 + BM25 混合检索 + RRF 融合排序 + Rerank 精排
- 设计多 Provider Embedding 管理器，含独立熔断器、指数退避重试、L1 LRU + L2 Redis 二级缓存
- 基于 Redis Streams 实现分布式异步索引队列，支持多实例消费者组竞争消费和故障转移
- 抽象 VectorStore 接口适配 Redis/Milvus/Qdrant 三种后端，集成 Neo4j 实现 Graph RAG 知识图谱检索
- 实现索引 Schema 蓝绿迁移、多租户隔离、Redis Sentinel/Cluster 高可用，Docker 化完整部署

---

## 版本三：STAR 描述法（适合面试口述 / 项目详情页）

**背景(S)：** AI 应用（如 Cursor、Claude Desktop）需要访问私有文档知识库来增强生成质量，但缺少标准化的 RAG 服务接入方式。

**任务(T)：** 设计并开发一套通过 MCP 协议对外提供文档索引、语义检索和 RAG Prompt 构建能力的服务，需满足生产级的可靠性、可扩展性和多租户需求。

**行动(A)：**

1. **分块引擎**：设计四级降级分块策略——代码感知 → 语义分块 → 结构感知 → 固定窗口，确保不同文档类型均获得高质量分块
2. **检索管线**：实现向量语义 + BM25 全文混合检索，采用 RRF 算法融合两路排名；集成 HyDE 查询扩展和 Multi-Query 多查询变体，经 Rerank 二次精排后返回
3. **Embedding 高可用**：每个 Provider 拥有独立的三态熔断器（连续 5 次失败触发 Open → 30s 冷却 → HalfOpen 探测恢复），配合指数退避 + 随机抖动重试策略和后台健康检查协程
4. **分布式索引**：基于 Redis Streams 实现异步任务队列，利用 XREADGROUP 消费者组在多实例间负载均衡，XAUTOCLAIM 自动认领超时任务，实现至少一次语义
5. **存储适配**：面向接口（VectorStore）设计，通过工厂模式适配 Redis/Milvus/Qdrant，索引支持 FLAT（精确搜索）和 HNSW（近似搜索）两种算法
6. **Graph RAG**：集成 Neo4j，通过 LLM 实体提取器异步提取实体关系构建知识图谱，实现实体级多跳检索并与向量结果 RRF 融合

**结果(R)：** 完成含 12 个 MCP 工具的完整服务，支持 Markdown/HTML/PDF/DOCX/代码文件索引，多租户隔离，Redis 三种部署模式（Standalone/Sentinel/Cluster），Docker 一键部署；单元 + 集成测试全覆盖。

---

## 简历关键词总结（ATS 友好）

| 领域 | 关键词 |
|------|--------|
| 语言/框架 | Go 1.24, TOML 配置, JSON-RPC 2.0, Streamable HTTP |
| AI/RAG | RAG, Embedding, Rerank, HyDE, Multi-Query Retrieval, Chunking, Vector Search, BM25, RRF Fusion |
| 数据库 | Redis Stack, RediSearch, Redis Streams, Neo4j, Milvus, Qdrant |
| 架构模式 | MCP (Model Context Protocol), 熔断器模式, 指数退避重试, LRU 缓存, 生产者-消费者, 蓝绿部署, 依赖倒置 |
| 分布式 | 消费者组竞争消费, XAUTOCLAIM 故障转移, Redis Sentinel/Cluster, 多租户隔离 |
| DevOps | Docker 多阶段构建, Docker Compose, 健康检查, 优雅关闭 |

---

## 写作建议

1. **量化成果**：如果有实际使用数据，补充进去效果更好，例如"索引 10 万+ 文档"、"P99 检索延迟 <200ms"、"缓存命中率 80%+ 降低 API 调用成本"
2. **根据岗位调整重点**：
   - **后端开发岗**：重点写架构设计（熔断器、分布式队列、接口抽象）
   - **AI 工程岗**：重点写 RAG 管线（混合检索、Rerank、HyDE、Graph RAG）
   - **基础设施岗**：重点写部署运维（Redis 三模式、蓝绿迁移、Docker 编排）
3. **一定要写"独立设计并开发"**，这是全栈项目的加分项
