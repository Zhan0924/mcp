# 构建阶段 (Build stage)
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 配置 Go 代理并下载依赖
ENV GOPROXY=https://goproxy.cn,direct

COPY go.mod go.sum ./
RUN go mod download

# 复制所有源代码
COPY . .

# 静态编译 Go 程序
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o rag-mcp-server .

# 运行阶段 (Run stage)
FROM alpine:latest

# 安装 tzdata 以支持时区 + wget 用于健康检查
RUN apk --no-cache add ca-certificates tzdata wget

WORKDIR /app

# 从 builder 阶段复制编译好的二进制文件
COPY --from=builder /app/rag-mcp-server .

# 复制配置文件
COPY config.toml .

# 创建上传暂存目录
RUN mkdir -p /tmp/rag-uploads

# 暴露服务端口 (与 config.toml [server].port 保持一致)
EXPOSE 8083

# 健康检查: 每 30s 探测 /health 端点，返回 JSON 包含服务状态和依赖连通性
# /health 端点会检测 Redis 连通性，503 表示服务降级，200 表示全部正常
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://localhost:8083/health || exit 1

# 启动服务
CMD ["./rag-mcp-server", "-config", "config.toml"]
