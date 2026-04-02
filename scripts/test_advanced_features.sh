#!/usr/bin/env bash
# 高级 RAG 功能详细测试: Rerank / HyDE / Multi-Query / Context Compressor / Graph RAG
set -euo pipefail

BASE="http://localhost:8083/mcp"
OUT="/tmp/rag_advanced_test.txt"
SID=""
ID=0

send() {
  local method=$1; shift
  ID=$((ID+1))
  local params="$*"
  local body
  if [ "$method" = "initialize" ]; then
    body='{"jsonrpc":"2.0","id":'$ID',"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}'
  else
    body='{"jsonrpc":"2.0","id":'$ID',"method":"'"$method"'","params":{'"$params"'}}'
  fi
  curl -s -X POST "$BASE" -H "Content-Type: application/json" -H "Mcp-Session-Id: $SID" -d "$body"
}

call_tool() {
  local name=$1; shift
  send "tools/call" '"name":"'"$name"'","arguments":{'"$*"'}'
}

# Init
echo "等待服务器就绪..."
for i in $(seq 1 30); do
  if curl -sf http://localhost:8083/health > /dev/null 2>&1; then break; fi
  sleep 1
done

echo "=== 初始化 MCP 会话 ===" | tee "$OUT"
INIT=$(send initialize)
SID=$(echo "$INIT" | grep -o '"Mcp-Session-Id"' || true)
# 从 HTTP header 获取 session
SID=$(curl -s -D - -X POST "$BASE" -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}' 2>&1 | grep -i "mcp-session-id" | tr -d '\r' | awk '{print $2}')
ID=1
echo "Session: $SID" | tee -a "$OUT"

# ========================
# Step 1: 索引测试文档（更丰富的内容）
# ========================
echo "" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "  Step 1: 索引丰富的测试文档" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"

DOC1_CONTENT="# Kubernetes 架构指南\n\n## Pod\nPod是Kubernetes中最小的可部署单元。一个Pod可以包含一个或多个容器，它们共享网络命名空间和存储卷。Pod中的容器通过localhost通信。\n\n## Service\nService为一组Pod提供稳定的网络端点。Service支持ClusterIP、NodePort、LoadBalancer三种类型。ClusterIP是默认类型，只能在集群内部访问。\n\n## Deployment\nDeployment管理Pod的生命周期和滚动更新。它支持声明式更新、回滚、扩缩容。ReplicaSet确保指定数量的Pod副本始终运行。\n\n## ConfigMap 和 Secret\nConfigMap用于存储非敏感配置数据，Secret用于存储敏感数据如密码和API密钥。两者都可以通过环境变量或卷挂载注入Pod。"

DOC2_CONTENT="# Redis 向量搜索指南\n\n## 向量索引\nRedis Stack支持两种向量索引算法：FLAT（暴力搜索，适合小规模数据）和HNSW（近似最近邻，适合大规模数据）。HNSW的M参数控制连接数，EF参数控制搜索精度。\n\n## 查询语法\nFT.SEARCH命令支持KNN查询、混合查询（向量+文本）、过滤查询。使用DIALECT 2启用向量查询语法。\n\n## 性能优化\n建议批量写入时使用Pipeline减少网络开销。对于高并发场景，可使用连接池。Redis的向量搜索延迟通常在毫秒级别。"

DOC3_CONTENT="# 分布式系统设计\n\n## CAP定理\nCAP定理指出分布式系统无法同时满足一致性(Consistency)、可用性(Availability)和分区容错性(Partition tolerance)。在网络分区时必须在C和A之间选择。\n\n## 微服务架构\n微服务将单体应用拆分为独立部署的服务。每个服务有自己的数据库。服务间通过API网关或消息队列通信。\n\n## 负载均衡\n常见的负载均衡算法包括轮询、加权轮询、最少连接、一致性哈希。Kubernetes的Service默认使用iptables轮询。"

echo "索引文档1: kubernetes_guide.md" | tee -a "$OUT"
R=$(call_tool rag_index_document '"file_id":"adv-001","file_name":"kubernetes_guide.md","user_id":99999,"content":"'"$DOC1_CONTENT"'"')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "索引文档2: redis_vector.md" | tee -a "$OUT"
R=$(call_tool rag_index_document '"file_id":"adv-002","file_name":"redis_vector.md","user_id":99999,"content":"'"$DOC2_CONTENT"'"')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "索引文档3: distributed_systems.md" | tee -a "$OUT"
R=$(call_tool rag_index_document '"file_id":"adv-003","file_name":"distributed_systems.md","user_id":99999,"content":"'"$DOC3_CONTENT"'"')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "文档列表:" | tee -a "$OUT"
R=$(call_tool rag_list_documents '"user_id":99999')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

sleep 2

# ========================
# Step 2: Rerank 重排序测试
# ========================
echo "" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "  Step 2: Rerank 重排序" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"

echo "--- 2a: 不带Rerank搜索 'Kubernetes Service类型' ---" | tee -a "$OUT"
R=$(call_tool rag_search '"query":"Kubernetes Service类型","user_id":99999,"top_k":5')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- 2b: 带Rerank搜索 'Kubernetes Service类型' ---" | tee -a "$OUT"
R=$(call_tool rag_search '"query":"Kubernetes Service类型","user_id":99999,"top_k":5,"rerank":true')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- 2c: Rerank跨领域搜索 '如何优化性能' ---" | tee -a "$OUT"
R=$(call_tool rag_search '"query":"如何优化性能","user_id":99999,"top_k":5,"rerank":true')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

# ========================
# Step 3: 检查日志中的 HyDE / Multi-Query / Compressor 行为
# ========================
echo "" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "  Step 3: HyDE 查询扩展" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"

echo "--- 3a: 搜索 'CAP定理是什么' (HyDE 会生成假想答案) ---" | tee -a "$OUT"
R=$(call_tool rag_search '"query":"CAP定理是什么","user_id":99999,"top_k":3')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- 3b: 搜索 '如何实现负载均衡' (HyDE + 跨文档) ---" | tee -a "$OUT"
R=$(call_tool rag_search '"query":"如何实现负载均衡","user_id":99999,"top_k":3')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

# ========================
# Step 4: Multi-Query 多查询检索
# ========================
echo "" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "  Step 4: Multi-Query 多查询检索" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"

echo "--- 4a: 搜索 'K8s部署应用的方式' (Multi-Query 扩展变体) ---" | tee -a "$OUT"
R=$(call_tool rag_search '"query":"K8s部署应用的方式","user_id":99999,"top_k":5')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

# ========================
# Step 5: Context Compressor 上下文压缩
# ========================
echo "" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "  Step 5: Context Compressor 上下文压缩" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"

echo "--- 5a: 搜索 'HNSW参数配置' (Compressor 压缩结果) ---" | tee -a "$OUT"
R=$(call_tool rag_search '"query":"HNSW参数配置","user_id":99999,"top_k":5')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- 5b: Build Prompt 'Secret和ConfigMap区别' (完整流程) ---" | tee -a "$OUT"
R=$(call_tool rag_build_prompt '"query":"Secret和ConfigMap有什么区别","user_id":99999,"top_k":3')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

# ========================
# Step 6: Graph RAG 知识图谱
# ========================
echo "" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "  Step 6: Graph RAG 知识图谱" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"

echo "--- 6a: 实体搜索 'Kubernetes' ---" | tee -a "$OUT"
R=$(call_tool rag_graph_search '"query":"Kubernetes","search_type":"entity","depth":2')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- 6b: 语义查询 '微服务架构和负载均衡的关系' ---" | tee -a "$OUT"
R=$(call_tool rag_graph_search '"query":"微服务架构和负载均衡的关系","search_type":"query","top_k":5')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- 6c: Graph + Vector 融合搜索 ---" | tee -a "$OUT"
R=$(call_tool rag_graph_search '"query":"Pod和Service的关系","search_type":"query","merge_vector":true,"user_id":99999,"top_k":5')
echo "$R" | jq -r '.result.content[0].text // .error.message' | tee -a "$OUT"

# ========================
# Step 7: 查看服务器日志中的高级功能行为
# ========================
echo "" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "  Step 7: 服务器日志验证" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"

sleep 2
echo "--- HyDE 日志 ---" | tee -a "$OUT"
docker compose -f /Users/qiankunzhan/code/mcp/mcp/docker-compose.yml logs mcp-rag-server --since=2m 2>&1 | grep -i "hyde\|hypothetical" | tail -5 | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- Multi-Query 日志 ---" | tee -a "$OUT"
docker compose -f /Users/qiankunzhan/code/mcp/mcp/docker-compose.yml logs mcp-rag-server --since=2m 2>&1 | grep -i "multi.query\|variant\|expanded" | tail -5 | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- Rerank 日志 ---" | tee -a "$OUT"
docker compose -f /Users/qiankunzhan/code/mcp/mcp/docker-compose.yml logs mcp-rag-server --since=2m 2>&1 | grep -i "rerank\|recall" | tail -5 | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- Compressor 日志 ---" | tee -a "$OUT"
docker compose -f /Users/qiankunzhan/code/mcp/mcp/docker-compose.yml logs mcp-rag-server --since=2m 2>&1 | grep -i "compress" | tail -5 | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "--- Graph RAG 日志 ---" | tee -a "$OUT"
docker compose -f /Users/qiankunzhan/code/mcp/mcp/docker-compose.yml logs mcp-rag-server --since=2m 2>&1 | grep -i "graph\|entity\|neo4j\|extractor\|LLMExtractor" | tail -10 | tee -a "$OUT"

# ========================
# Cleanup
# ========================
echo "" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "  清理测试数据" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
call_tool rag_delete_document '"file_id":"adv-001","user_id":99999' > /dev/null 2>&1
call_tool rag_delete_document '"file_id":"adv-002","user_id":99999' > /dev/null 2>&1
call_tool rag_delete_document '"file_id":"adv-003","user_id":99999' > /dev/null 2>&1
echo "测试数据已清理" | tee -a "$OUT"

echo "" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "  所有高级功能测试完成！" | tee -a "$OUT"
echo "═══════════════════════════════════════════════" | tee -a "$OUT"
echo "结果保存至: $OUT"
