#!/bin/bash
# ╔═══════════════════════════════════════════════════════════════════════════╗
# ║  RAG MCP Server — 全面测试脚本                                            ║
# ║                                                                           ║
# ║  测试项:                                                                   ║
# ║    1. 健康检查端点 (GET /health)                                           ║
# ║    2. MCP 协议端点 (POST /mcp — JSON-RPC)                                 ║
# ║    3. 端口可达性 (8082 + 8083)                                             ║
# ║    4. Docker 容器状态                                                      ║
# ║    5. 依赖服务连通性 (Redis, Neo4j)                                        ║
# ║    6. MCP 工具列表                                                         ║
# ║                                                                           ║
# ║  用法: bash scripts/test_server.sh [host] [port]                           ║
# ║  默认: localhost 8082                                                      ║
# ╚═══════════════════════════════════════════════════════════════════════════╝

set -euo pipefail

HOST="${1:-localhost}"
PORT="${2:-8082}"
BASE_URL="http://${HOST}:${PORT}"
TIMEOUT=5
PASS=0
FAIL=0
TOTAL=0

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_header() {
    echo ""
    echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  RAG MCP Server 全面测试${NC}"
    echo -e "${BLUE}  目标: ${BASE_URL}${NC}"
    echo -e "${BLUE}  时间: $(date '+%Y-%m-%d %H:%M:%S')${NC}"
    echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
}

assert_ok() {
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
        echo -e "    实际结果: ${result}"
        return 1
    fi
}

assert_http_code() {
    local test_name="$1"
    local url="$2"
    local expected_code="$3"
    local method="${4:-GET}"
    local data="${5:-}"
    TOTAL=$((TOTAL + 1))

    local code
    if [[ "$method" == "POST" ]]; then
        code=$(curl -s -o /dev/null -w "%{http_code}" --max-time "$TIMEOUT" -X POST "$url" -H "Content-Type: application/json" -d "$data" 2>/dev/null || echo "000")
    else
        code=$(curl -s -o /dev/null -w "%{http_code}" --max-time "$TIMEOUT" "$url" 2>/dev/null || echo "000")
    fi

    if [[ "$code" == "$expected_code" ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} ${test_name} (HTTP ${code})"
        return 0
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} ${test_name} (HTTP ${code}, 期望 ${expected_code})"
        return 1
    fi
}

# ═══════════════════════════════════════════════════════════════════════════
#  测试 0: Docker 容器状态
# ═══════════════════════════════════════════════════════════════════════════
test_docker_status() {
    echo ""
    echo -e "${YELLOW}▸ 测试 0: Docker 容器状态${NC}"

    # 检查 mcp-rag-server
    local status
    status=$(docker inspect --format='{{.State.Status}}' mcp-rag-server 2>/dev/null || echo "not_found")
    assert_ok "mcp-rag-server 容器运行中" "$status" "running" || true

    # 检查健康状态
    local health
    health=$(docker inspect --format='{{.State.Health.Status}}' mcp-rag-server 2>/dev/null || echo "unknown")
    assert_ok "mcp-rag-server 健康状态" "$health" "healthy" || true

    # 检查 redis-stack
    status=$(docker inspect --format='{{.State.Status}}' redis-stack 2>/dev/null || echo "not_found")
    assert_ok "redis-stack 容器运行中" "$status" "running" || true

    # 检查 neo4j
    status=$(docker inspect --format='{{.State.Status}}' neo4j 2>/dev/null || echo "not_found")
    assert_ok "neo4j 容器运行中" "$status" "running" || true

    # 显示端口映射
    echo ""
    echo -e "  ${BLUE}端口映射:${NC}"
    docker port mcp-rag-server 2>/dev/null | sed 's/^/    /' || echo "    (无法获取)"
}

# ═══════════════════════════════════════════════════════════════════════════
#  测试 1: 健康检查端点
# ═══════════════════════════════════════════════════════════════════════════
test_health_endpoint() {
    echo ""
    echo -e "${YELLOW}▸ 测试 1: 健康检查端点 (GET /health)${NC}"

    # 端口 8082
    assert_http_code "/health 端口 8082 可达" "http://${HOST}:8082/health" "200" || true

    # 端口 8083
    assert_http_code "/health 端口 8083 可达" "http://${HOST}:8083/health" "200" || true

    # 验证返回 JSON 内容
    local body
    body=$(curl -s --max-time "$TIMEOUT" "${BASE_URL}/health" 2>/dev/null || echo "{}")
    assert_ok "/health 返回 status 字段" "$body" '"status"' || true
    assert_ok "/health 返回 server 字段" "$body" '"server"' || true
    assert_ok "/health 返回 redis 字段" "$body" '"redis"' || true
    assert_ok "/health Redis 连通" "$body" '"redis":"ok"' || true

    echo ""
    echo -e "  ${BLUE}完整响应:${NC}"
    echo "$body" | python3 -m json.tool 2>/dev/null | sed 's/^/    /' || echo "    $body"
}

# ═══════════════════════════════════════════════════════════════════════════
#  测试 2: MCP 协议端点 — initialize
# ═══════════════════════════════════════════════════════════════════════════
test_mcp_initialize() {
    echo ""
    echo -e "${YELLOW}▸ 测试 2: MCP 协议端点 (POST /mcp — initialize)${NC}"

    local init_req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-script","version":"1.0.0"}}}'

    # 端口 8082
    assert_http_code "POST /mcp initialize 端口 8082" "http://${HOST}:8082/mcp" "200" "POST" "$init_req" || true

    # 端口 8083
    assert_http_code "POST /mcp initialize 端口 8083" "http://${HOST}:8083/mcp" "200" "POST" "$init_req" || true

    # 验证返回内容
    local body
    body=$(curl -s --max-time "$TIMEOUT" -X POST "${BASE_URL}/mcp" -H "Content-Type: application/json" -d "$init_req" 2>/dev/null || echo "{}")
    assert_ok "返回 protocolVersion" "$body" '"protocolVersion"' || true
    assert_ok "返回 serverInfo" "$body" '"serverInfo"' || true
    assert_ok "返回 capabilities.tools" "$body" '"tools"' || true

    echo ""
    echo -e "  ${BLUE}完整响应:${NC}"
    echo "$body" | python3 -m json.tool 2>/dev/null | sed 's/^/    /' || echo "    $body"
}

# ═══════════════════════════════════════════════════════════════════════════
#  测试 3: MCP 协议端点 — tools/list
# ═══════════════════════════════════════════════════════════════════════════
test_mcp_tools_list() {
    echo ""
    echo -e "${YELLOW}▸ 测试 3: MCP 工具列表 (POST /mcp — tools/list)${NC}"

    # 先初始化获取 session
    local init_req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-script","version":"1.0.0"}}}'
    local headers_file
    headers_file=$(mktemp)

    curl -s --max-time "$TIMEOUT" -X POST "${BASE_URL}/mcp" \
        -H "Content-Type: application/json" \
        -d "$init_req" \
        -D "$headers_file" > /dev/null 2>&1 || true

    # 提取 session ID
    local session_id
    session_id=$(grep -i "mcp-session-id" "$headers_file" 2>/dev/null | sed 's/.*: //' | tr -d '\r\n' || echo "")

    if [[ -n "$session_id" ]]; then
        echo -e "  ${BLUE}Session ID: ${session_id}${NC}"

        # 发送 initialized 通知
        curl -s --max-time "$TIMEOUT" -X POST "${BASE_URL}/mcp" \
            -H "Content-Type: application/json" \
            -H "Mcp-Session-Id: ${session_id}" \
            -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' > /dev/null 2>&1 || true

        # 获取工具列表
        local tools_req='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
        local tools_body
        tools_body=$(curl -s --max-time "$TIMEOUT" -X POST "${BASE_URL}/mcp" \
            -H "Content-Type: application/json" \
            -H "Mcp-Session-Id: ${session_id}" \
            -d "$tools_req" 2>/dev/null || echo "{}")

        assert_ok "返回 tools 列表" "$tools_body" '"tools"' || true

        # 统计工具数量
        local tool_count
        tool_count=$(echo "$tools_body" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('result',{}).get('tools',[])))" 2>/dev/null || echo "0")
        TOTAL=$((TOTAL + 1))
        if [[ "$tool_count" -gt 0 ]]; then
            PASS=$((PASS + 1))
            echo -e "  ${GREEN}✓${NC} 已注册 ${tool_count} 个工具"
        else
            FAIL=$((FAIL + 1))
            echo -e "  ${RED}✗${NC} 工具列表为空"
        fi

        # 列出工具名称
        echo ""
        echo -e "  ${BLUE}工具列表:${NC}"
        echo "$tools_body" | python3 -c "
import sys, json
d = json.load(sys.stdin)
tools = d.get('result', {}).get('tools', [])
for t in tools:
    print(f\"    - {t['name']}: {t.get('description', 'N/A')[:60]}\")
" 2>/dev/null || echo "    (解析失败)"
    else
        TOTAL=$((TOTAL + 1))
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} 未获取到 Session ID"
    fi

    rm -f "$headers_file"
}

# ═══════════════════════════════════════════════════════════════════════════
#  测试 4: 错误处理
# ═══════════════════════════════════════════════════════════════════════════
test_error_handling() {
    echo ""
    echo -e "${YELLOW}▸ 测试 4: 错误处理${NC}"

    # GET /mcp 返回 SSE 流 — curl 超时退出，但 HTTP 状态码是 200
    # 注意: curl --max-time 超时后 exit code=28，-w 输出 "200"，|| echo "000" 会拼接，所以取前 3 位
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 "${BASE_URL}/mcp" 2>/dev/null; true)
    code="${code:0:3}"  # 取前 3 位，避免超时拼接问题
    TOTAL=$((TOTAL + 1))
    if [[ "$code" == "200" || "$code" == "405" ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} GET /mcp 返回 HTTP ${code} (SSE 流，正常行为)"
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} GET /mcp 返回 HTTP ${code}"
    fi

    # 不存在的路径
    assert_http_code "GET /nonexistent 返回 404" "${BASE_URL}/nonexistent" "404" || true

    # 非法 JSON-RPC (无 session 时 MCP 库返回 404，属于正常防护)
    local invalid_code
    invalid_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time "$TIMEOUT" -X POST "${BASE_URL}/mcp" -H "Content-Type: application/json" -d '{"invalid":true}' 2>/dev/null || echo "000")
    TOTAL=$((TOTAL + 1))
    if [[ "$invalid_code" =~ ^(400|404|405)$ ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} POST /mcp 无效请求被拒绝 (HTTP ${invalid_code})"
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} POST /mcp 无效请求返回 HTTP ${invalid_code} (期望 400/404/405)"
    fi

    # /health 只接受 GET
    assert_http_code "POST /health 返回 405" "${BASE_URL}/health" "405" "POST" '{}' || true
}

# ═══════════════════════════════════════════════════════════════════════════
#  测试 5: 依赖服务直接连通性
# ═══════════════════════════════════════════════════════════════════════════
test_dependencies() {
    echo ""
    echo -e "${YELLOW}▸ 测试 5: 依赖服务连通性${NC}"

    # Redis
    TOTAL=$((TOTAL + 1))
    if docker exec redis-stack redis-cli -a 123456 ping 2>/dev/null | grep -q "PONG"; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} Redis PING → PONG"
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} Redis PING 失败"
    fi

    # Neo4j HTTP
    TOTAL=$((TOTAL + 1))
    local neo4j_code
    neo4j_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 3 "http://${HOST}:7474" 2>/dev/null || echo "000")
    if [[ "$neo4j_code" == "200" ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} Neo4j HTTP 端点可达 (HTTP ${neo4j_code})"
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} Neo4j HTTP 端点不可达 (HTTP ${neo4j_code})"
    fi

    # RedisInsight
    TOTAL=$((TOTAL + 1))
    local insight_code
    insight_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 3 "http://${HOST}:8002" 2>/dev/null || echo "000")
    if [[ "$insight_code" =~ ^[23] ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} RedisInsight 面板可达 (HTTP ${insight_code})"
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} RedisInsight 面板不可达 (HTTP ${insight_code})"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════
#  测试 6: 响应时间
# ═══════════════════════════════════════════════════════════════════════════
test_response_time() {
    echo ""
    echo -e "${YELLOW}▸ 测试 6: 响应时间${NC}"

    # /health 响应时间
    local health_time
    health_time=$(curl -s -o /dev/null -w "%{time_total}" --max-time "$TIMEOUT" "${BASE_URL}/health" 2>/dev/null || echo "999")
    TOTAL=$((TOTAL + 1))
    local health_ms
    health_ms=$(echo "$health_time * 1000" | bc 2>/dev/null | cut -d. -f1 || echo "999")
    if [[ "$health_ms" -lt 500 ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} /health 响应时间: ${health_ms}ms (< 500ms)"
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} /health 响应时间: ${health_ms}ms (>= 500ms，过慢)"
    fi

    # MCP initialize 响应时间
    local init_req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}'
    local mcp_time
    mcp_time=$(curl -s -o /dev/null -w "%{time_total}" --max-time "$TIMEOUT" -X POST "${BASE_URL}/mcp" -H "Content-Type: application/json" -d "$init_req" 2>/dev/null || echo "999")
    TOTAL=$((TOTAL + 1))
    local mcp_ms
    mcp_ms=$(echo "$mcp_time * 1000" | bc 2>/dev/null | cut -d. -f1 || echo "999")
    if [[ "$mcp_ms" -lt 1000 ]]; then
        PASS=$((PASS + 1))
        echo -e "  ${GREEN}✓${NC} MCP initialize 响应时间: ${mcp_ms}ms (< 1000ms)"
    else
        FAIL=$((FAIL + 1))
        echo -e "  ${RED}✗${NC} MCP initialize 响应时间: ${mcp_ms}ms (>= 1000ms，过慢)"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════
#  汇总
# ═══════════════════════════════════════════════════════════════════════════
print_summary() {
    echo ""
    echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  测试汇总${NC}"
    echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "  总计: ${TOTAL}   ${GREEN}通过: ${PASS}${NC}   ${RED}失败: ${FAIL}${NC}"
    echo ""

    if [[ "$FAIL" -eq 0 ]]; then
        echo -e "  ${GREEN}🎉 所有测试通过！${NC}"
    else
        echo -e "  ${RED}⚠  有 ${FAIL} 个测试失败，请检查上方详情${NC}"
    fi
    echo ""
}

# 主流程
print_header
test_docker_status
test_health_endpoint
test_mcp_initialize
test_mcp_tools_list
test_error_handling
test_dependencies
test_response_time
print_summary

exit $FAIL
