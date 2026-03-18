# 构建阶段 (Build stage)
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 配置 Go 代理并下载依赖 (提速并解决可能存在的网络问题)
ENV GOPROXY=https://goproxy.cn,direct

COPY go.mod go.sum ./
RUN go mod download

# 复制所有源代码
COPY . .

# 静态编译 Go 程序
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o rag-mcp-server .

# 运行阶段 (Run stage)
FROM alpine:latest

# 安装 tzdata 以支持时区
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 从 builder 阶段复制编译好的二进制文件
COPY --from=builder /app/rag-mcp-server .

# 复制配置文件
COPY config.toml .

# 暴露服务端口 (默认 8082)
EXPOSE 8082

# 启动服务
CMD ["./rag-mcp-server", "-config", "config.toml"]
