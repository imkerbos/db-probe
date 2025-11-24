# 多阶段构建
# 阶段1: 构建
FROM golang:1.24-alpine AS builder

# 设置构建参数
ARG VERSION=unknown
ARG BUILD_TIME=unknown
ARG GIT_COMMIT=unknown

WORKDIR /build

# 复制 go mod 文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 构建（纯 Go，无需 CGO）
# 注入版本信息到二进制文件
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}" \
    -o db-probe ./cmd

# 阶段2: 运行（使用 Alpine）
FROM alpine:latest

# 安装运行时依赖
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    wget \
    && update-ca-certificates

# 创建非 root 用户
RUN addgroup -g 1000 dbprobe && \
    adduser -D -u 1000 -G dbprobe dbprobe

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /build/db-probe .

# 复制配置文件（config.yaml 在 configs 目录）
COPY --from=builder /build/configs ./configs

# 设置文件权限
RUN chown -R dbprobe:dbprobe /app

# 切换到非 root 用户
USER dbprobe

# 暴露端口
EXPOSE 9100

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:9100/health || exit 1

# 运行
CMD ["./db-probe"]
