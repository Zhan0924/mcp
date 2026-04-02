#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────────────
#  TLS 证书生成脚本（开发/测试用）
#
#  生成自签名证书用于开发环境 HTTPS 测试。
#  生产环境请使用 Let's Encrypt 或企业 CA 签发的证书。
#
#  使用方式：
#    bash scripts/gen-certs.sh                    # 默认输出到 ./certs/
#    bash scripts/gen-certs.sh /etc/rag/tls       # 指定输出目录
#    DOMAIN=rag.example.com bash scripts/gen-certs.sh  # 指定域名
#
#  生成文件：
#    certs/ca.crt       — CA 根证书
#    certs/server.crt   — 服务器证书
#    certs/server.key   — 服务器私钥
# ──────────────────────────────────────────────────────────────────────────────
set -euo pipefail

CERT_DIR="${1:-./certs}"
DOMAIN="${DOMAIN:-localhost}"
DAYS="${DAYS:-365}"
CA_SUBJECT="/C=CN/ST=Beijing/O=RAG-MCP-Server/CN=RAG-Dev-CA"
SERVER_SUBJECT="/C=CN/ST=Beijing/O=RAG-MCP-Server/CN=${DOMAIN}"

info() { echo "[gen-certs] $*"; }

mkdir -p "$CERT_DIR"

# 1. 生成 CA 私钥和证书
info "Generating CA certificate..."
openssl genrsa -out "$CERT_DIR/ca.key" 4096 2>/dev/null
openssl req -new -x509 -days "$DAYS" -key "$CERT_DIR/ca.key" \
    -out "$CERT_DIR/ca.crt" -subj "$CA_SUBJECT" 2>/dev/null

# 2. 生成服务器私钥和 CSR
info "Generating server certificate for: $DOMAIN"
openssl genrsa -out "$CERT_DIR/server.key" 2048 2>/dev/null

# SAN 扩展（Subject Alternative Names）—— 现代浏览器要求
cat > "$CERT_DIR/san.cnf" << EOF
[req]
distinguished_name = req_dn
req_extensions = v3_req
prompt = no

[req_dn]
C = CN
ST = Beijing
O = RAG-MCP-Server
CN = ${DOMAIN}

[v3_req]
subjectAltName = @alt_names
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth

[alt_names]
DNS.1 = ${DOMAIN}
DNS.2 = *.${DOMAIN}
DNS.3 = localhost
IP.1 = 127.0.0.1
IP.2 = ::1
EOF

openssl req -new -key "$CERT_DIR/server.key" \
    -out "$CERT_DIR/server.csr" -config "$CERT_DIR/san.cnf" 2>/dev/null

# 3. CA 签发服务器证书
openssl x509 -req -days "$DAYS" \
    -in "$CERT_DIR/server.csr" \
    -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
    -out "$CERT_DIR/server.crt" \
    -extensions v3_req -extfile "$CERT_DIR/san.cnf" 2>/dev/null

# 4. 清理中间文件
rm -f "$CERT_DIR/server.csr" "$CERT_DIR/san.cnf" "$CERT_DIR/ca.srl"

# 5. 设置权限
chmod 600 "$CERT_DIR"/*.key
chmod 644 "$CERT_DIR"/*.crt

info "Certificates generated in: $CERT_DIR"
info "  ca.crt      — CA root certificate (add to trust store)"
info "  server.crt  — Server certificate"
info "  server.key  — Server private key"
info ""
info "config.toml 配置："
info "  [server.tls]"
info "  enabled = true"
info "  cert_file = \"$CERT_DIR/server.crt\""
info "  key_file = \"$CERT_DIR/server.key\""
info ""
info "验证: openssl x509 -in $CERT_DIR/server.crt -text -noout | head -20"
