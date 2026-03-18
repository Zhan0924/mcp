# Redis 向量搜索与 RediSearch 完全指南

## 1. 概述

Redis 从 7.0 版本开始内置了 RediSearch 模块，提供了强大的全文搜索和向量搜索能力。向量搜索（Vector Similarity Search）是现代 AI 应用的核心组件，它允许用户通过语义相似度来检索数据，而不仅仅依赖关键词匹配。在 RAG（Retrieval-Augmented Generation）系统中，向量搜索是连接知识库和大语言模型的关键桥梁。

Redis 作为向量数据库具有以下优势：极低的查询延迟（毫秒级响应），既能存储向量也能存储原始文档和元数据，支持混合查询（向量搜索 + 传统过滤），丰富的数据结构支持复杂的应用场景，以及成熟的运维生态。

### 1.1 核心概念

向量搜索的基本原理是将文本、图像等非结构化数据通过嵌入模型（Embedding Model）转换为高维向量，然后在向量空间中计算距离来衡量相似度。常用的距离度量包括：

- **余弦距离（COSINE）**：衡量两个向量方向的相似度，值域为 [0, 2]，越小越相似。最适合文本语义搜索
- **欧氏距离（L2）**：衡量向量空间中两点间的直线距离，适合需要绝对距离的场景
- **内积（IP）**：向量的点积，适合经过归一化处理的向量

### 1.2 支持的索引算法

RediSearch 支持两种向量索引算法：

- **FLAT**：暴力搜索，计算查询向量与所有存储向量的距离。优点是 100% 精确，缺点是数据量大时查询较慢。适合数据量在 10 万以下的场景
- **HNSW（Hierarchical Navigable Small World）**：近似最近邻算法，通过构建多层图结构来加速搜索。查询速度快（对数级复杂度），但需要更多内存，且结果是近似的。适合大规模数据场景

## 2. 数据模型与索引设计

### 2.1 Schema 设计

在 RediSearch 中，向量索引的 Schema 定义了数据的存储格式和搜索字段：

```
FT.CREATE my_index ON HASH PREFIX 1 doc:
  SCHEMA
    content TEXT
    file_id TAG
    file_name TEXT NOINDEX
    chunk_id TAG
    chunk_index NUMERIC
    vector VECTOR FLAT 6
      TYPE FLOAT32
      DIM 1024
      DISTANCE_METRIC COSINE
```

字段类型说明：

- **TEXT**：全文搜索字段，支持分词和模糊匹配
- **TAG**：精确匹配字段，支持多值标签和 OR 查询，适合 ID、类别等
- **NUMERIC**：数值字段，支持范围查询
- **VECTOR**：向量字段，存储浮点数组用于相似度搜索

### 2.2 索引前缀与多租户设计

RediSearch 使用前缀（PREFIX）来确定哪些 Redis Key 属于某个索引。在多租户系统中，可以为每个用户创建独立的索引和前缀，实现数据隔离：

```
FT.CREATE user_1001:idx ON HASH PREFIX 1 user_1001:
FT.CREATE user_1002:idx ON HASH PREFIX 1 user_1002:
```

这种设计方案的好处是数据完全隔离、删除用户数据简单（直接删除前缀匹配的所有 Key），缺点是索引数量随用户增长，需要注意 Redis 的内存管理和索引数量限制。

### 2.3 HASH vs JSON 存储

RediSearch 支持 HASH 和 JSON 两种底层数据结构。HASH 结构简单高效，适合扁平的键值数据；JSON 结构支持嵌套数据和数组，更灵活但内存开销略大。对于 RAG 场景，HASH 通常是更好的选择，因为分块数据结构相对简单。

## 3. 向量搜索查询

### 3.1 KNN 查询

KNN（K-Nearest Neighbors）查询返回与目标向量最相似的 K 个结果。这是最基本的向量搜索操作：

```
FT.SEARCH my_index
  "*=>[KNN 5 @vector $vec AS distance]"
  PARAMS 2 vec <binary_vector>
  RETURN 4 content file_id chunk_id distance
  SORTBY distance ASC
  DIALECT 2
```

`*` 表示在所有文档中搜索，也可以替换为过滤条件来缩小搜索范围。`AS distance` 将计算得到的距离值命名为 `distance` 字段返回。

### 3.2 混合查询（Hybrid Query）

混合查询允许在向量搜索的同时应用传统的过滤条件，这对于实现精确的结果筛选非常有用：

```
FT.SEARCH my_index
  "@file_id:{doc001|doc002}=>[KNN 10 @vector $vec AS distance]"
  PARAMS 2 vec <binary_vector>
  SORTBY distance ASC
  DIALECT 2
```

上面的查询只在 file_id 为 doc001 或 doc002 的文档中进行向量搜索。混合查询是 Redis 向量搜索相比纯向量数据库的一大优势，因为它允许充分利用 Redis 的传统索引能力来精确过滤。

### 3.3 Range 查询

除了 KNN 查询，RediSearch 还支持基于距离阈值的范围查询，返回距离在指定范围内的所有结果：

```
FT.SEARCH my_index
  "@vector:[VECTOR_RANGE 0.5 $vec]"
  PARAMS 2 vec <binary_vector>
  DIALECT 2
```

Range 查询适合需要找出所有"足够相似"结果的场景，而不限制返回数量。

### 3.4 查询方言（Dialect）

RediSearch 2.4+ 引入了查询方言版本控制。向量搜索查询必须使用 DIALECT 2 或更高版本。DIALECT 4 支持更多高级特性，如向量范围查询的布尔操作。

## 4. Embedding 向量生成

### 4.1 Embedding 模型选择

向量搜索的效果很大程度上取决于 Embedding 模型的质量。常见的 Embedding 模型包括：

- **OpenAI text-embedding-3-small/large**：通用性好，支持多语言。small 模型 1536 维，large 模型 3072 维
- **DashScope text-embedding-v4**：阿里云通义千问系列，对中文有良好支持。输出 1024 维向量，支持 8192 token 上下文
- **BGE 系列（bge-large-zh-v1.5）**：开源中文 Embedding 模型，支持本地部署

选择 Embedding 模型时需要考虑：向量维度（影响存储和计算成本）、最大输入长度（影响分块策略）、语言支持（中文场景需要选择对中文友好的模型）、推理延迟和成本。

### 4.2 文档分块策略

将长文档分割成适当大小的块（Chunk）是向量搜索的关键步骤。分块策略直接影响检索质量：

- **固定长度分块**：简单直接，但可能截断语义单元
- **递归字符分割**：按段落、句子、词逐层分割，保持语义完整性
- **基于 Markdown 标题分割**：利用文档结构进行分块，适合结构化文档
- **语义分块**：使用 Embedding 模型判断语义边界，效果最好但成本最高

分块大小的选择需要平衡精确度和完整度：小块能提供更精确的检索，但可能缺少上下文；大块包含更完整的信息，但向量表示可能不够精确。通常 500-1000 字符是一个较好的起点，并配合 10-20% 的重叠区间。

### 4.3 批量向量化

在索引大量文档时，应该使用批量 API 来提高效率。大多数 Embedding API 支持一次处理多个文本，减少网络往返次数。DashScope 等 API 通常限制每批最多 10-25 条文本，需要在代码中实现分批处理逻辑。

## 5. 性能优化

### 5.1 索引参数调优

HNSW 索引的关键参数：

- **M**：每个节点的最大邻居数。增大 M 提高召回率但增加内存和索引时间。默认值 16，推荐范围 12-64
- **EF_CONSTRUCTION**：构建索引时的候选集大小。增大提高索引质量但增加构建时间。默认值 200，推荐范围 100-500
- **EF_RUNTIME**：查询时的候选集大小。增大提高召回率但增加查询延迟。默认值 10，推荐范围 10-500

对于 FLAT 索引，BLOCK_SIZE 参数控制内存分配的块大小。较大的 BLOCK_SIZE 减少内存碎片但增加初始分配。

### 5.2 内存优化

向量数据通常是 Redis 中最大的内存消耗者。优化建议：

- 选择合适的向量维度：更低维度的向量占用更少内存。如果不需要极高精度，1024 维通常比 1536 维更经济
- 使用 FLOAT32 而非 FLOAT64：精度损失很小，但内存消耗减半
- 定期清理过期数据：结合 TTL 自动清理不再需要的向量数据
- 监控 Redis 内存使用：使用 INFO MEMORY 命令监控内存占用趋势

### 5.3 查询优化

- 合理设置 K 值：不要一次请求过多结果，大多数场景 5-10 个结果就足够
- 利用预过滤：在向量搜索前用 TAG 或 NUMERIC 条件缩小候选集
- 使用 Pipeline 批量操作：索引数据时使用 Redis Pipeline 减少网络往返
- 考虑数据分片：数据量超过单机内存时，使用 Redis Cluster 进行水平扩展

## 6. RAG 应用实践

### 6.1 检索增强生成（RAG）架构

RAG 系统的核心流程：用户输入查询 → 查询向量化 → 在向量数据库中搜索 → 获取相关文档片段 → 将文档和查询一起发送给 LLM → 生成回答。Redis 在这个架构中同时承担向量存储和检索的角色。

### 6.2 Prompt 工程

将检索结果整合到 Prompt 中的最佳实践：按文件分组展示，避免跨文件的分块混淆；标注来源和相关度分数，方便 LLM 判断信息可靠性；设置合理的 Prompt 长度上限，避免超出 LLM 的上下文窗口。

### 6.3 多租户 RAG 系统

在多用户场景下，每个用户应该有独立的索引空间，确保数据隔离。索引命名建议采用 `{app}_{user_id}:idx` 的模式，Key 前缀采用 `{app}_{user_id}:` 的模式。这样既保证了隔离性，又便于管理（如删除某个用户的所有数据）。

## 7. 监控与运维

### 7.1 索引状态监控

使用 `FT.INFO index_name` 查看索引状态，关注 num_docs（文档数）、num_records（记录数）、hash_indexing_failures（索引失败数）等指标。定期检查索引健康度是保证搜索质量的基础。

### 7.2 查询性能监控

使用 `FT.PROFILE index_name SEARCH query` 分析查询执行计划，识别性能瓶颈。关注指标包括：查询延迟 P99、向量比较次数、结果集大小。

### 7.3 容量规划

向量数据的内存消耗公式：`内存 ≈ 文档数 × (向量维度 × 4字节 + 元数据大小)`。例如 100 万个 1024 维向量约需 4GB 存储，加上元数据和索引开销，实际使用约 6-8GB。建议预留 30% 的内存余量用于峰值处理和索引更新操作。
