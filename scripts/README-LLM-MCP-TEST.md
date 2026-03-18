# 大模型使用 RAG MCP 服务测试指南

在 `scripts/rag-mcp.http` 手工测试通过后，可按本指南验证 **AI 助手（大模型）能否正确选择并调用** 本 MCP 提供的 RAG 工具。

---

## 一、前置条件

1. **RAG MCP 服务已启动**
   - Docker：`docker-compose up -d`（端口 8082）
   - 或本机：`./rag-mcp-server -config config.toml`（端口 8083，下面配置需改端口）

2. **Cursor 已安装**，并能在本机访问上述服务地址（如 `http://localhost:8082`）。

---

## 二、在 Cursor 中配置 MCP

### 1. 项目级配置（推荐）

在项目根目录创建或编辑 `.cursor/mcp.json`：

```json
{
  "mcpServers": {
    "rag-server": {
      "url": "http://localhost:8082/mcp"
    }
  }
}
```

若本机直接运行服务且使用 8083 端口，将 `8082` 改为 `8083`。

### 2. 重启 Cursor

保存配置后**完全退出并重新打开 Cursor**，或通过设置中的 MCP 相关入口刷新，确保加载新配置。

### 3. 确认 MCP 已连接

- 在 Cursor 的 **设置 → MCP** 或 **聊天面板** 中查看是否出现 `rag-server` 且状态为已连接。
- 若未连接，检查服务是否在运行、端口是否正确、防火墙是否放行。

---

## 三、测试话术与预期行为

在 Cursor 的 **AI 聊天** 中依次使用下面话术，观察 AI 是否调用了对应工具并返回合理结果。

| 序号 | 测试话术（直接复制到聊天） | 期望调用的工具 | 验证要点 |
|------|----------------------------|----------------|----------|
| 1 | 先看一下 RAG 服务状态，有没有报错？ | `rag_status` | 返回里应包含 status、providers、cache 等 |
| 2 | 帮我把下面这段内容按文档分块预览一下，每块大概 200 字，重叠 50 字，要按结构分块：「# 测试\n\n这是第一段。\n\n## 第二节\n\n这是第二段。」 | `rag_chunk_text` | 应返回多个 chunk，且 content 与结构合理 |
| 3 | 帮我把这段文档索引进去：内容「# Go 并发\n\nGo 用 goroutine 做并发，channel 用来通信。」，file_id 用 `llm-test-go`，user_id 用 1，文件名 `go_intro.md`，格式 markdown。 | `rag_index_document` | 返回中有 file_id、total_chunks、indexed 等 |
| 4 | 在 user_id=1 的索引里，用「Go 并发和 channel」检索，取 top 3 条。 | `rag_search` | 应返回与 Go 并发/channel 相关的片段，带 relevance_score |
| 5 | 用 RAG 帮我构建一个 prompt：问题是「解释 goroutine 和 channel 的关系」，user_id=1，取 top 3 条相关文档。 | `rag_build_prompt` | 返回应是一段可直接给大模型用的带上下文的 prompt 文本 |
| 6 | 只在我刚索引的 file_id=`llm-test-go` 里检索「goroutine」，user_id=1，top_k=3。 | `rag_search`（带 file_ids） | 结果应都来自 `llm-test-go` |
| 7 | 解析一下这段文档的结构和格式，不要建索引：「## Redis\n\nRediSearch 支持向量检索。」格式是 markdown。 | `rag_parse_document` | 返回中有格式、结构或段落信息，无索引操作 |
| 8 | 把 file_id=`llm-test-go`、user_id=1 的文档从 RAG 索引里删掉。 | `rag_delete_document` | 返回中有 deleted 数量 |
| 9 | 再查一次 RAG 服务状态。 | `rag_status` | 与步骤 1 对比，可看到缓存等变化 |

---

## 四、如何判断「大模型是否正确使用」

- **选对工具**：聊天中或 Cursor 的 MCP 调用记录里，能看到对应请求调用了上表中的工具名（如 `rag_search`、`rag_status`），且没有误用其它工具。
- **参数正确**：必填参数（如 `user_id`、`query`、`file_id`）与你在话术里给出的一致或合理推断。
- **结果可用**：返回内容与预期一致（例如检索到相关片段、状态信息完整、删除后再查无该文档等）。

若某条话术没有触发预期工具，或参数明显错误，可把该条话术改得更直白（例如明确写出「请调用 rag_xxx 工具，参数为 …」）再测一次，以区分是「描述不清」还是「模型未正确理解工具用途」。

---

## 五、可选：异步索引与任务状态

若服务开启了异步索引（`config.toml` 中 `[async_index] enabled = true`），可追加测试：

| 话术 | 期望工具 |
|------|----------|
| 把一篇长文档用**异步**方式索引进去，file_id=`async-llm-001`，user_id=1，文件名 `async_doc.md`，格式 markdown。 | `rag_index_document`（带 async: true） |
| 查一下 task_id 为「上一步返回的 task_id」的异步任务状态。 | `rag_task_status` |

---

## 六、常见问题

- **Cursor 里看不到 rag-server / 显示未连接**  
  检查 `.cursor/mcp.json` 路径与内容、服务是否启动、端口是否为本机可访问的 8082/8083。

- **AI 没有调用工具，只给文字回答**  
  把需求写得更明确，例如加上「请用 RAG 的 xxx 工具」「先查 rag_status」等，或换一条上表中的标准话术重试。

- **工具报错（如 Redis、Embedding 错误）**  
  说明 MCP 调用已发出，问题在服务端或环境（Redis、API Key 等），可结合服务日志与 `rag-mcp.http` 用例排查。

按上述步骤做完一轮，即可确认大模型是否能正确使用本 RAG MCP 服务。
