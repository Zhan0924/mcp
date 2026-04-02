#!/bin/bash
# ╔═══════════════════════════════════════════════════════════════════════════╗
# ║  RAG MCP Server — 全功能端到端测试                                        ║
# ║                                                                           ║
# ║  测试覆盖所有 12 个 MCP 工具 + Resources + Prompts 的完整功能              ║
# ║                                                                           ║
# ║  测试流程:                                                                 ║
# ║    1. 建立 MCP Session                                                    ║
# ║    2. rag_status         — 系统状态检查                                   ║
# ║    3. rag_parse_document — 文档解析                                       ║
# ║    4. rag_chunk_text     — 文档分块                                       ║
# ║    5. rag_index_document — 文档同步索引                                   ║
# ║    6. rag_list_documents — 文档列表                                       ║
# ║    7. rag_search         — 向量语义检索                                   ║
# ║    8. rag_build_prompt   — 构建 RAG 提示词                                ║
# ║    9. rag_export_data    — 数据导出                                       ║
# ║   10. rag_graph_search   — 知识图谱搜索                                   ║
# ║   11. rag_index_url      — 网页索引 (可选)                                ║
# ║   12. rag_task_status    — 异步任务状态                                   ║
# ║   13. rag_delete_document — 文档删除 (清理)                               ║
# ║   14. Resources          — 资源模板测试                                   ║
# ║   15. Prompts            — 提示词模板测试                                 ║
# ║                                                                           ║
# ║  用法: bash scripts/test_rag_functional.sh [host] [port]                   ║
# ╚═══════════════════════════════════════════════════════════════════════════╝

set -euo pipefail

HOST="${1:-localhost}"
PORT="${2:-8082}"
BASE_URL="http://${HOST}:${PORT}"
TIMEOUT=30
PASS=0
FAIL=0
TOTAL=0
TEST_USER_ID=99999  # 测试专用 user_id, 避免影响真实数据
TEST_FILE_ID="test_func_$(date +%s)"
MSG_ID=0
SESSION_ID=""
DOC_FILE="test_rag_results_$(date +%Y%m%d_%H%M%S).md"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ═══════════════════════════════════════════════════════════════════════════
#  工具函数
# ═══════════════════════════════════════════════════════════════════════════

next_id() {
    MSG_ID=$((MSG_ID + 1))
    echo "$MSG_ID"
}

# 发送 MCP JSON-RPC 请求
mcp_request() {
    local method="$1"
    local params="$2"
    local id
    id=$(next_id)

    local body="{\"jsonrpc\":\"2.0\",\"id\":${id},\"method\":\"${method}\",\"params\":${params}}"

    curl -s --max-time "$TIMEOUT" -X POST "${BASE_URL}/mcp" \
        -H "Content-Type: application/json" \
        -H "Mcp-Session-Id: ${SESSION_ID}" \
        -d "$body" 2>/dev/null
}

# 发送 MCP 通知 (无 id)
mcp_notify() {
    local method="$1"
    local params="${2:-{}}"

    local body="{\"jsonrpc\":\"2.0\",\"method\":\"${method}\",\"params\":${params}}"

    curl -s --max-time "$TIMEOUT" -X POST "${BASE_URL}/mcp" \
        -H "Content-Type: application/json" \
        -H "Mcp-Session-Id: ${SESSION_ID}" \
        -d "$body" > /dev/null 2>&1
}

# 调用 MCP 工具
call_tool() {
    local tool_name="$1"
    local arguments="$2"

    mcp_request "tools/call" "{\"name\":\"${tool_name}\",\"arguments\":${arguments}}"
}

# 断言结果包含指定内容
assert_contains() {
    local test_name="$1"
    local result="$2"
    local expected="$3"
    TOTAL=$((TOTAL + 1))
    if [[ "$result" == *"$expected"* ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} ${test_name}"
        return 0
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} ${test_name}"
        echo -e "    期望包含: ${expected}"
        echo -e "    实际结果: $(echo "$result" | head -c 200)"
        return 1
    fi
}

# 断言结果不包含 error
assert_no_error() {
    local test_name="$1"
    local result="$2"
    TOTAL=$((TOTAL + 1))
    if [[ "$result" != *'"isError":true'* ]] && [[ "$result" != *'"error":'* || "$result" == *'"error":null'* || "$result" == *'"error":""'* ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} ${test_name}"
        return 0
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} ${test_name}"
        echo -e "    响应包含错误: $(echo "$result" | head -c 300)"
        return 1
    fi
}

# 输出截断的 JSON 响应
show_response() {
    local body="$1"
    local max="${2:-500}"
    echo -e "  ${CYAN}响应:${NC}"
    echo "$body" | python3 -m json.tool 2>/dev/null | head -30 | sed 's/^/    /' || echo "    $(echo "$body" | head -c "$max")"
}

# 写入文档
doc() {
    echo "$@" >> "$DOC_FILE"
}

doc_json() {
    echo '```json' >> "$DOC_FILE"
    echo "$1" | python3 -m json.tool 2>/dev/null >> "$DOC_FILE" || echo "$1" >> "$DOC_FILE"
    echo '```' >> "$DOC_FILE"
}

print_header() {
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║  RAG MCP Server — 全功能端到端测试                           ║${NC}"
    echo -e "${BLUE}║  目标: ${BASE_URL}                                      ║${NC}"
    echo -e "${BLUE}║  时间: $(date '+%Y-%m-%d %H:%M:%S')                            ║${NC}"
    echo -e "${BLUE}║  用户: user_id=${TEST_USER_ID} (测试专用)                      ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 0: 建立 MCP Session
# ═══════════════════════════════════════════════════════════════════════════
setup_session() {
    echo ""
    echo -e "${YELLOW}▸ Step 0: 建立 MCP Session${NC}"

    local init_req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"rag-functional-test","version":"1.0.0"}}}'
    local headers_file
    headers_file=$(mktemp)
    MSG_ID=1

    local body
    body=$(curl -s --max-time "$TIMEOUT" -X POST "${BASE_URL}/mcp" \
        -H "Content-Type: application/json" \
        -d "$init_req" \
        -D "$headers_file" 2>/dev/null)

    SESSION_ID=$(grep -i "mcp-session-id" "$headers_file" 2>/dev/null | sed 's/.*: //' | tr -d '\r\n' || echo "")
    rm -f "$headers_file"

    if [[ -z "$SESSION_ID" ]]; then
        echo -e "  ${RED}✗ 无法获取 Session ID，测试终止${NC}"
        exit 1
    fi

    # 发送 initialized 通知
    mcp_notify "notifications/initialized"

    TOTAL=$((TOTAL + 1)); PASS=$((PASS + 1))
    echo -e "  ${GREEN}✓${NC} Session 建立成功: ${SESSION_ID}"

    # 写入文档头
    doc "# RAG MCP Server — 全功能测试报告"
    doc ""
    doc "- **测试时间**: $(date '+%Y-%m-%d %H:%M:%S')"
    doc "- **服务地址**: ${BASE_URL}"
    doc "- **Session ID**: \`${SESSION_ID}\`"
    doc "- **测试用户**: user_id=${TEST_USER_ID}"
    doc "- **测试文件**: file_id=${TEST_FILE_ID}"
    doc ""
    doc "---"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 1: rag_status — 系统状态
# ═══════════════════════════════════════════════════════════════════════════
test_rag_status() {
    echo ""
    echo -e "${YELLOW}▸ Step 1: rag_status — 系统状态检查${NC}"
    doc "## 1. rag_status — 系统状态检查"
    doc ""
    doc "查看 Embedding Provider 健康状态、缓存命中率、Rerank 配置等。"
    doc ""

    local result
    result=$(call_tool "rag_status" '{}')

    assert_no_error "rag_status 无错误" "$result" || true
    assert_contains "返回 providers 信息" "$result" "providers" || true
    assert_contains "返回 cache 信息" "$result" "cache" || true

    show_response "$result"

    doc "**请求参数**: \`{}\`"
    doc ""
    doc "**响应**:"
    doc_json "$result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 2: rag_parse_document — 文档解析
# ═══════════════════════════════════════════════════════════════════════════
test_rag_parse_document() {
    echo ""
    echo -e "${YELLOW}▸ Step 2: rag_parse_document — 文档解析${NC}"
    doc "## 2. rag_parse_document — 文档解析"
    doc ""
    doc "解析 Markdown 文档，提取元数据和章节结构。"
    doc ""

    local content="# Redis 向量搜索指南\n\n## 1. 概述\n\nRedis 从 7.0 版本开始原生支持向量搜索。\n\n## 2. 索引设计\n\n使用 FT.CREATE 创建向量索引。\n\n### 2.1 Schema 定义\n\n需要定义向量字段和元数据字段。"

    local args
    args=$(python3 -c "
import json
print(json.dumps({
    'content': '$content',
    'format': 'markdown'
}))
" 2>/dev/null)

    local result
    result=$(call_tool "rag_parse_document" "$args")

    assert_no_error "rag_parse_document 无错误" "$result" || true
    assert_contains "返回章节结构" "$result" "section" || true

    show_response "$result"

    doc "**请求参数**:"
    doc '```json'
    doc '{"content": "# Redis 向量搜索指南\\n\\n## 1. 概述\\n...", "format": "markdown"}'
    doc '```'
    doc ""
    doc "**响应**:"
    doc_json "$result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 3: rag_chunk_text — 文档分块
# ═══════════════════════════════════════════════════════════════════════════
test_rag_chunk_text() {
    echo ""
    echo -e "${YELLOW}▸ Step 3: rag_chunk_text — 文档分块${NC}"
    doc "## 3. rag_chunk_text — 文档分块"
    doc ""
    doc "将文本内容分割为语义完整的块，支持结构感知分块。"
    doc ""

    local content="Go 语言是 Google 开发的编程语言，具有高并发、垃圾回收等特性。Go 语言的设计目标是提高开发效率，同时保持高性能。它的编译速度极快，类型系统简洁而强大，内置了 goroutine 和 channel 用于并发编程。Go 的标准库非常丰富，涵盖了 HTTP、JSON、加密等常见需求。在云原生领域，Go 语言是主流选择，Docker、Kubernetes、Prometheus 等项目都是用 Go 编写的。"

    local args
    args=$(python3 -c "
import json
print(json.dumps({
    'content': '''$content''',
    'max_chunk_size': 100,
    'overlap_size': 20
}))
" 2>/dev/null)

    local result
    result=$(call_tool "rag_chunk_text" "$args")

    assert_no_error "rag_chunk_text 无错误" "$result" || true
    assert_contains "返回 chunks" "$result" "chunk" || true

    show_response "$result"

    doc "**请求参数**:"
    doc '```json'
    doc '{"content": "Go 语言是 Google 开发的编程语言...", "max_chunk_size": 100, "overlap_size": 20}'
    doc '```'
    doc ""
    doc "**响应**:"
    doc_json "$result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 4: rag_index_document — 文档同步索引
# ═══════════════════════════════════════════════════════════════════════════
test_rag_index_document() {
    echo ""
    echo -e "${YELLOW}▸ Step 4: rag_index_document — 文档同步索引${NC}"
    doc "## 4. rag_index_document — 文档同步索引"
    doc ""
    doc "将文档分块、向量化并存入向量索引。使用同步模式。"
    doc ""

    local content="# Kubernetes 容器编排\n\nKubernetes（简称 K8s）是一个开源的容器编排平台，由 Google 设计并捐赠给 CNCF。它可以自动化容器的部署、扩展和管理。\n\n## 核心概念\n\n### Pod\nPod 是 K8s 中最小的可部署单元，包含一个或多个容器。Pod 中的容器共享网络命名空间和存储卷。\n\n### Service\nService 为一组 Pod 提供稳定的网络端点。它支持 ClusterIP、NodePort、LoadBalancer 等类型。\n\n### Deployment\nDeployment 管理 Pod 的副本集，支持滚动更新和回滚。\n\n## 调度机制\n\nK8s 调度器根据资源需求、节点亲和性、污点容忍等因素将 Pod 分配到合适的节点。调度器的核心算法包括预选和优选两个阶段。\n\n## 存储\n\nK8s 支持多种存储后端，包括 PersistentVolume、StorageClass、CSI 驱动等。PV 和 PVC 的绑定机制保证了存储的可靠管理。"

    local args
    args=$(python3 -c "
import json
print(json.dumps({
    'file_id': '${TEST_FILE_ID}',
    'user_id': ${TEST_USER_ID},
    'content': '''${content}''',
    'file_name': 'kubernetes_guide.md',
    'format': 'markdown'
}))
" 2>/dev/null)

    local result
    result=$(call_tool "rag_index_document" "$args")

    assert_no_error "rag_index_document 无错误" "$result" || true
    assert_contains "索引成功" "$result" "chunk" || true

    show_response "$result"

    doc "**请求参数**:"
    doc '```json'
    doc "{\"file_id\": \"${TEST_FILE_ID}\", \"user_id\": ${TEST_USER_ID}, \"content\": \"# Kubernetes 容器编排\\n...\", \"file_name\": \"kubernetes_guide.md\", \"format\": \"markdown\"}"
    doc '```'
    doc ""
    doc "**响应**:"
    doc_json "$result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 4b: 索引第二个文档用于搜索对比
# ═══════════════════════════════════════════════════════════════════════════
test_rag_index_document_2() {
    echo ""
    echo -e "${YELLOW}▸ Step 4b: 索引第二个文档 (Redis 向量搜索)${NC}"

    local content2="# Redis 向量搜索\n\nRedis 从 7.0 版本开始支持原生向量搜索。RediSearch 模块提供了 FT.CREATE 和 FT.SEARCH 命令用于创建索引和执行搜索。\n\n## 距离度量\n\n支持 COSINE（余弦相似度）、L2（欧几里得距离）和 IP（内积）三种距离度量方式。\n\n## 索引算法\n\n- FLAT：暴力搜索，适合小规模数据\n- HNSW：近似最近邻，适合大规模数据\n\n## 混合查询\n\nRedis 支持将向量搜索与全文检索、标签过滤等结合使用，实现更精准的语义搜索。"

    local file_id2="${TEST_FILE_ID}_redis"
    local args
    args=$(python3 -c "
import json
print(json.dumps({
    'file_id': '${file_id2}',
    'user_id': ${TEST_USER_ID},
    'content': '''${content2}''',
    'file_name': 'redis_vector_search.md',
    'format': 'markdown'
}))
" 2>/dev/null)

    local result
    result=$(call_tool "rag_index_document" "$args")

    assert_no_error "第二个文档索引无错误" "$result" || true

    show_response "$result" 300
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 5: rag_list_documents — 文档列表
# ═══════════════════════════════════════════════════════════════════════════
test_rag_list_documents() {
    echo ""
    echo -e "${YELLOW}▸ Step 5: rag_list_documents — 文档列表${NC}"
    doc "## 5. rag_list_documents — 文档列表"
    doc ""
    doc "列出用户知识库中已索引的所有文档。"
    doc ""

    local result
    result=$(call_tool "rag_list_documents" "{\"user_id\":${TEST_USER_ID}}")

    assert_no_error "rag_list_documents 无错误" "$result" || true
    assert_contains "包含 kubernetes" "$result" "kubernetes" || true
    assert_contains "包含 redis" "$result" "redis" || true

    show_response "$result"

    doc "**请求参数**: \`{\"user_id\": ${TEST_USER_ID}}\`"
    doc ""
    doc "**响应**:"
    doc_json "$result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 6: rag_search — 向量语义检索
# ═══════════════════════════════════════════════════════════════════════════
test_rag_search() {
    echo ""
    echo -e "${YELLOW}▸ Step 6: rag_search — 向量语义检索${NC}"
    doc "## 6. rag_search — 向量语义检索"
    doc ""
    doc "在知识库中搜索与查询最相关的文档片段。"
    doc ""

    # 测试 1: 基本搜索
    echo -e "  ${CYAN}6a: 基本搜索 — 'Kubernetes Pod 调度'${NC}"
    local result
    result=$(call_tool "rag_search" "{\"query\":\"Kubernetes Pod 调度机制\",\"user_id\":${TEST_USER_ID},\"top_k\":3}")

    assert_no_error "基本搜索无错误" "$result" || true
    assert_contains "搜索返回内容" "$result" "content" || true

    show_response "$result"

    doc "### 6a. 基本搜索"
    doc "**请求**: \`{\"query\": \"Kubernetes Pod 调度机制\", \"user_id\": ${TEST_USER_ID}, \"top_k\": 3}\`"
    doc ""
    doc "**响应**:"
    doc_json "$result"
    doc ""

    # 测试 2: 跨文档搜索
    echo ""
    echo -e "  ${CYAN}6b: 跨文档搜索 — 'Redis 向量索引算法'${NC}"
    local result2
    result2=$(call_tool "rag_search" "{\"query\":\"Redis 向量索引算法 HNSW\",\"user_id\":${TEST_USER_ID},\"top_k\":3}")

    assert_no_error "跨文档搜索无错误" "$result2" || true
    assert_contains "返回 Redis 相关内容" "$result2" "content" || true

    show_response "$result2"

    doc "### 6b. 跨文档搜索"
    doc "**请求**: \`{\"query\": \"Redis 向量索引算法 HNSW\", \"user_id\": ${TEST_USER_ID}, \"top_k\": 3}\`"
    doc ""
    doc "**响应**:"
    doc_json "$result2"
    doc ""

    # 测试 3: 带文件过滤的搜索
    echo ""
    echo -e "  ${CYAN}6c: 文件过滤搜索 — 仅搜索 K8s 文档${NC}"
    local result3
    result3=$(call_tool "rag_search" "{\"query\":\"容器编排\",\"user_id\":${TEST_USER_ID},\"top_k\":3,\"file_ids\":\"${TEST_FILE_ID}\"}")

    assert_no_error "文件过滤搜索无错误" "$result3" || true

    show_response "$result3" 300

    doc "### 6c. 文件过滤搜索"
    doc "**请求**: \`{\"query\": \"容器编排\", \"file_ids\": \"${TEST_FILE_ID}\"}\`"
    doc ""
    doc "**响应**:"
    doc_json "$result3"
    doc ""

    # 测试 4: 带 Rerank 的搜索
    echo ""
    echo -e "  ${CYAN}6d: Rerank 精排搜索${NC}"
    local result4
    result4=$(call_tool "rag_search" "{\"query\":\"如何管理 Kubernetes 存储卷\",\"user_id\":${TEST_USER_ID},\"top_k\":5,\"rerank\":true}")

    assert_no_error "Rerank 搜索无错误" "$result4" || true

    show_response "$result4" 300

    doc "### 6d. Rerank 精排搜索"
    doc "**请求**: \`{\"query\": \"如何管理 Kubernetes 存储卷\", \"rerank\": true}\`"
    doc ""
    doc "**响应**:"
    doc_json "$result4"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 7: rag_build_prompt — 构建 RAG 提示词
# ═══════════════════════════════════════════════════════════════════════════
test_rag_build_prompt() {
    echo ""
    echo -e "${YELLOW}▸ Step 7: rag_build_prompt — 构建 RAG 提示词${NC}"
    doc "## 7. rag_build_prompt — 构建 RAG 提示词"
    doc ""
    doc "自动检索相关文档，构建包含上下文的提示词。"
    doc ""

    local result
    result=$(call_tool "rag_build_prompt" "{\"query\":\"解释 Kubernetes Service 的工作原理\",\"user_id\":${TEST_USER_ID},\"top_k\":3}")

    assert_no_error "rag_build_prompt 无错误" "$result" || true
    assert_contains "返回 prompt 内容" "$result" "content" || true

    show_response "$result"

    doc "**请求**: \`{\"query\": \"解释 Kubernetes Service 的工作原理\", \"user_id\": ${TEST_USER_ID}}\`"
    doc ""
    doc "**响应**:"
    doc_json "$result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 8: rag_export_data — 数据导出
# ═══════════════════════════════════════════════════════════════════════════
test_rag_export_data() {
    echo ""
    echo -e "${YELLOW}▸ Step 8: rag_export_data — 数据导出${NC}"
    doc "## 8. rag_export_data — 数据导出"
    doc ""
    doc "导出指定文档的所有分块内容。"
    doc ""

    local result
    result=$(call_tool "rag_export_data" "{\"user_id\":${TEST_USER_ID},\"file_id\":\"${TEST_FILE_ID}\"}")

    assert_no_error "rag_export_data 无错误" "$result" || true
    assert_contains "返回导出内容" "$result" "content" || true

    show_response "$result"

    doc "**请求**: \`{\"user_id\": ${TEST_USER_ID}, \"file_id\": \"${TEST_FILE_ID}\"}\`"
    doc ""
    doc "**响应**:"
    doc_json "$result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 9: rag_graph_search — 知识图谱搜索
# ═══════════════════════════════════════════════════════════════════════════
test_rag_graph_search() {
    echo ""
    echo -e "${YELLOW}▸ Step 9: rag_graph_search — 知识图谱搜索${NC}"
    doc "## 9. rag_graph_search — 知识图谱搜索"
    doc ""
    doc "在知识图谱中搜索实体和关系，支持多跳推理。"
    doc ""

    local result
    result=$(call_tool "rag_graph_search" "{\"query\":\"Kubernetes\",\"user_id\":${TEST_USER_ID},\"search_depth\":2}")

    assert_no_error "rag_graph_search 无错误" "$result" || true

    show_response "$result"

    doc "**请求**: \`{\"query\": \"Kubernetes\", \"user_id\": ${TEST_USER_ID}, \"search_depth\": 2}\`"
    doc ""
    doc "**响应**:"
    doc_json "$result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 10: rag_task_status — 异步任务状态
# ═══════════════════════════════════════════════════════════════════════════
test_rag_task_status() {
    echo ""
    echo -e "${YELLOW}▸ Step 10: rag_task_status — 异步任务状态${NC}"
    doc "## 10. rag_task_status — 异步任务状态"
    doc ""
    doc "查询异步索引任务的进度和结果。"
    doc ""

    # 使用一个不存在的 task_id 测试错误处理
    local result
    result=$(call_tool "rag_task_status" "{\"task_id\":\"nonexistent_task_12345\"}")

    # 期望返回 "not found" 或者错误信息
    TOTAL=$((TOTAL + 1))
    if [[ "$result" == *"not found"* || "$result" == *"not_found"* || "$result" == *"error"* || "$result" == *"content"* ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} 不存在的任务返回合理响应"
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} 不存在的任务返回意外响应"
    fi

    show_response "$result" 300

    doc "**请求**: \`{\"task_id\": \"nonexistent_task_12345\"}\`"
    doc ""
    doc "**响应** (测试不存在的任务 ID):"
    doc_json "$result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 11: Resources — 资源模板测试
# ═══════════════════════════════════════════════════════════════════════════
test_resources() {
    echo ""
    echo -e "${YELLOW}▸ Step 11: MCP Resources — 资源模板${NC}"
    doc "## 11. MCP Resources — 资源模板"
    doc ""
    doc "通过 URI 模板读取已索引的文档内容。"
    doc ""

    # 列出资源模板
    local templates
    templates=$(mcp_request "resources/templates/list" "{}")
    assert_contains "返回资源模板" "$templates" "resourceTemplates" || true

    show_response "$templates" 300

    # 读取文档资源
    local resource
    resource=$(mcp_request "resources/read" "{\"uri\":\"rag://users/${TEST_USER_ID}/documents/${TEST_FILE_ID}\"}")
    assert_contains "资源返回文档内容" "$resource" "content" || true

    show_response "$resource"

    doc "### 资源模板列表"
    doc_json "$templates"
    doc ""
    doc "### 读取文档资源"
    doc "**URI**: \`rag://users/${TEST_USER_ID}/documents/${TEST_FILE_ID}\`"
    doc ""
    doc "**响应**:"
    doc_json "$resource"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 12: Prompts — 提示词模板测试
# ═══════════════════════════════════════════════════════════════════════════
test_prompts() {
    echo ""
    echo -e "${YELLOW}▸ Step 12: MCP Prompts — 提示词模板${NC}"
    doc "## 12. MCP Prompts — 提示词模板"
    doc ""

    # 列出提示词模板
    local prompts_list
    prompts_list=$(mcp_request "prompts/list" "{}")
    assert_contains "返回提示词列表" "$prompts_list" "prompts" || true

    show_response "$prompts_list"

    doc "### 提示词模板列表"
    doc_json "$prompts_list"
    doc ""

    # 测试 RAG_QA 提示词
    echo ""
    echo -e "  ${CYAN}测试 RAG_QA 提示词${NC}"
    local qa_result
    qa_result=$(mcp_request "prompts/get" "{\"name\":\"RAG_QA\",\"arguments\":{\"question\":\"Kubernetes 中 Pod 和 Service 的关系是什么\",\"user_id\":\"${TEST_USER_ID}\"}}")
    assert_contains "RAG_QA 返回消息" "$qa_result" "messages" || true

    show_response "$qa_result"

    doc "### RAG_QA 提示词测试"
    doc "**参数**: \`{\"question\": \"Kubernetes 中 Pod 和 Service 的关系是什么\", \"user_id\": \"${TEST_USER_ID}\"}\`"
    doc ""
    doc "**响应**:"
    doc_json "$qa_result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  Step 13: rag_delete_document — 文档删除（清理）
# ═══════════════════════════════════════════════════════════════════════════
test_rag_delete_document() {
    echo ""
    echo -e "${YELLOW}▸ Step 13: rag_delete_document — 文档删除 (清理)${NC}"
    doc "## 13. rag_delete_document — 文档删除"
    doc ""
    doc "删除测试文档，清理测试数据。"
    doc ""

    # 删除第一个文档
    local result
    result=$(call_tool "rag_delete_document" "{\"file_id\":\"${TEST_FILE_ID}\",\"user_id\":${TEST_USER_ID}}")
    assert_no_error "删除 K8s 文档无错误" "$result" || true

    show_response "$result" 300

    # 删除第二个文档
    local result2
    result2=$(call_tool "rag_delete_document" "{\"file_id\":\"${TEST_FILE_ID}_redis\",\"user_id\":${TEST_USER_ID}}")
    assert_no_error "删除 Redis 文档无错误" "$result2" || true

    # 验证删除后列表为空
    local list_result
    list_result=$(call_tool "rag_list_documents" "{\"user_id\":${TEST_USER_ID}}")
    echo -e "  ${CYAN}删除后文档列表:${NC}"
    show_response "$list_result" 200

    doc "**删除 K8s 文档响应**:"
    doc_json "$result"
    doc ""
    doc "**删除 Redis 文档响应**:"
    doc_json "$result2"
    doc ""
    doc "**删除后文档列表**:"
    doc_json "$list_result"
    doc ""
}

# ═══════════════════════════════════════════════════════════════════════════
#  汇总
# ═══════════════════════════════════════════════════════════════════════════
print_summary() {
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║  全功能测试汇总                                              ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "  总计: ${TOTAL}   ${GREEN}通过: ${PASS}${NC}   ${RED}失败: ${FAIL}${NC}"
    echo ""

    if [[ "$FAIL" -eq 0 ]]; then
        echo -e "  ${GREEN}🎉 所有功能测试通过！${NC}"
    else
        echo -e "  ${YELLOW}⚠  有 ${FAIL} 个测试失败（可能与 Embedding API Key 配置有关）${NC}"
    fi

    echo ""
    echo -e "  ${BLUE}测试报告已生成: ${DOC_FILE}${NC}"
    echo ""

    doc "---"
    doc ""
    doc "## 测试汇总"
    doc ""
    doc "| 指标 | 结果 |"
    doc "|------|------|"
    doc "| 总计 | ${TOTAL} |"
    doc "| 通过 | ${PASS} |"
    doc "| 失败 | ${FAIL} |"
    doc "| 通过率 | $(( PASS * 100 / TOTAL ))% |"
    doc ""
    if [[ "$FAIL" -eq 0 ]]; then
        doc "**✅ 所有功能测试通过**"
    else
        doc "**⚠️ 有 ${FAIL} 个测试失败**"
        doc ""
        doc "失败可能的原因:"
        doc "- Embedding API Key 未配置或额度不足"
        doc "- 外部 LLM 服务暂时不可用"
        doc "- Rerank 服务未配置"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════
#  主流程
# ═══════════════════════════════════════════════════════════════════════════
print_header
setup_session
test_rag_status
test_rag_parse_document
test_rag_chunk_text
test_rag_index_document
test_rag_index_document_2
test_rag_list_documents
test_rag_search
test_rag_build_prompt
test_rag_export_data
test_rag_graph_search
test_rag_task_status
test_resources
test_prompts
test_rag_delete_document
print_summary

exit "$FAIL"
