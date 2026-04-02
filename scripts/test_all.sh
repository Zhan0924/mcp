#!/bin/bash
# 全面 RAG MCP 测试 - 输出到文件
BASE="http://localhost:8083"
OUT="/tmp/rag_full_test.txt"
> "$OUT"

log() { echo "$1" >> "$OUT"; }

# Step 0: 初始化
INIT=$(curl -s -D /tmp/h.txt -X POST "$BASE/mcp" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1.0.0"}}}')
S=$(grep -i "Mcp-Session-Id" /tmp/h.txt | awk '{print $2}' | tr -d '\r\n')
log "=== Step 0: Init === Session=$S"
log "$INIT"
log ""

# Notification
curl -s -X POST "$BASE/mcp" -H "Content-Type: application/json" -H "Mcp-Session-Id: $S" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' > /dev/null

# Helper function
call() {
  local id=$1; local method=$2; local params=$3
  curl -s --max-time 120 -X POST "$BASE/mcp" \
    -H "Content-Type: application/json" \
    -H "Mcp-Session-Id: $S" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":$id,\"method\":\"$method\",\"params\":$params}"
}

# Step 1: List tools
log "=== Step 1: tools/list ==="
R=$(call 2 "tools/list" '{}')
log "$R"
log ""

# Step 2: rag_status
log "=== Step 2: rag_status ==="
R=$(call 3 "tools/call" '{"name":"rag_status","arguments":{}}')
log "$R"
log ""

# Step 3: rag_parse_document
log "=== Step 3: rag_parse_document ==="
R=$(call 4 "tools/call" '{"name":"rag_parse_document","arguments":{"content":"# Title\n\n## Section 1\nHello world.\n\n## Section 2\nFoo bar.","format":"markdown"}}')
log "$R"
log ""

# Step 4: rag_chunk_text
log "=== Step 4: rag_chunk_text ==="
R=$(call 5 "tools/call" '{"name":"rag_chunk_text","arguments":{"content":"Kubernetes is an open-source container orchestration platform. Pods are the smallest deployable units. Services provide network access to pods. Deployments manage pod lifecycle and updates."}}')
log "$R"
log ""

# Step 5a: rag_index_document (doc1)
log "=== Step 5a: rag_index_document (doc1) ==="
R=$(call 6 "tools/call" '{"name":"rag_index_document","arguments":{"content":"Kubernetes Pod是最小的部署单元。Service提供访问策略。Deployment提供声明式更新。","file_name":"k8s.md","file_id":"tf-001","user_id":99999}}')
log "$R"
log ""

# Step 5b: rag_index_document (doc2)
log "=== Step 5b: rag_index_document (doc2) ==="
R=$(call 7 "tools/call" '{"name":"rag_index_document","arguments":{"content":"Redis支持FLAT和HNSW两种向量索引算法。FT.SEARCH可执行KNN查询。","file_name":"redis.md","file_id":"tf-002","user_id":99999}}')
log "$R"
log ""

# Wait for indexing
sleep 3

# Step 6: rag_list_documents
log "=== Step 6: rag_list_documents ==="
R=$(call 8 "tools/call" '{"name":"rag_list_documents","arguments":{"user_id":99999}}')
log "$R"
log ""

# Step 7a: rag_search basic
log "=== Step 7a: rag_search basic ==="
R=$(call 9 "tools/call" '{"name":"rag_search","arguments":{"query":"什么是Pod","user_id":99999,"top_k":3}}')
log "$R"
log ""

# Step 7b: rag_search cross-doc
log "=== Step 7b: rag_search cross-doc ==="
R=$(call 10 "tools/call" '{"name":"rag_search","arguments":{"query":"向量索引","user_id":99999,"top_k":5}}')
log "$R"
log ""

# Step 7c: rag_search with rerank
log "=== Step 7c: rag_search rerank ==="
R=$(call 11 "tools/call" '{"name":"rag_search","arguments":{"query":"Service","user_id":99999,"top_k":3,"rerank":true}}')
log "$R"
log ""

# Step 7d: rag_search with file filter
log "=== Step 7d: rag_search file filter ==="
R=$(call 12 "tools/call" '{"name":"rag_search","arguments":{"query":"Deployment","user_id":99999,"top_k":3,"file_ids":"tf-001"}}')
log "$R"
log ""

# Step 8: rag_build_prompt
log "=== Step 8: rag_build_prompt ==="
R=$(call 13 "tools/call" '{"name":"rag_build_prompt","arguments":{"query":"如何配置Service","user_id":99999,"top_k":3}}')
log "$R"
log ""

# Step 9: rag_export_data
log "=== Step 9: rag_export_data ==="
R=$(call 14 "tools/call" '{"name":"rag_export_data","arguments":{"file_id":"tf-001","user_id":99999}}')
log "$R"
log ""

# Step 10: rag_graph_search
log "=== Step 10: rag_graph_search ==="
R=$(call 15 "tools/call" '{"name":"rag_graph_search","arguments":{"query":"Kubernetes","user_id":99999}}')
log "$R"
log ""

# Step 11: rag_task_status (non-existent)
log "=== Step 11: rag_task_status ==="
R=$(call 16 "tools/call" '{"name":"rag_task_status","arguments":{"task_id":"non-existent"}}')
log "$R"
log ""

# Step 12: resources/list
log "=== Step 12: resources/list ==="
R=$(call 17 "resources/list" '{}')
log "$R"
log ""

# Step 13: resources/templates/list
log "=== Step 13: resources/templates/list ==="
R=$(call 18 "resources/templates/list" '{}')
log "$R"
log ""

# Step 14: resources/read
log "=== Step 14: resources/read rag://status ==="
R=$(call 19 "resources/read" '{"uri":"rag://status"}')
log "$R"
log ""

# Step 15: prompts/list
log "=== Step 15: prompts/list ==="
R=$(call 20 "prompts/list" '{}')
log "$R"
log ""

# Step 16: prompts/get
log "=== Step 16: prompts/get rag_qa ==="
R=$(call 21 "prompts/get" '{"name":"rag_qa","arguments":{"query":"Service类型","user_id":"99999"}}')
log "$R"
log ""

# Step 17: rag_delete_document
log "=== Step 17a: rag_delete_document (doc1) ==="
R=$(call 22 "tools/call" '{"name":"rag_delete_document","arguments":{"file_id":"tf-001","user_id":99999}}')
log "$R"
log ""

log "=== Step 17b: rag_delete_document (doc2) ==="
R=$(call 23 "tools/call" '{"name":"rag_delete_document","arguments":{"file_id":"tf-002","user_id":99999}}')
log "$R"
log ""

# Step 18: Verify deletion
log "=== Step 18: rag_list_documents after delete ==="
R=$(call 24 "tools/call" '{"name":"rag_list_documents","arguments":{"user_id":99999}}')
log "$R"
log ""

log "=== ALL TESTS COMPLETE ==="
echo "Tests complete. Output saved to $OUT"
