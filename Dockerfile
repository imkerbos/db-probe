# 多阶段构建
# 阶段1: 构建
FROM golang:1.21-alpine AS builder

WORKDIR /build

# 复制 go mod 文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 构建
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o db-probe ./cmd

# 阶段2: 运行
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /build/db-probe .

# 复制配置文件
COPY --from=builder /build/configs/config.yaml ./configs/

# 暴露端口
EXPOSE 9100

# 运行
CMD ["./db-probe", "-config", "configs/config.yaml"]

