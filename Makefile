.PHONY: build run clean test

# 构建二进制文件
build:
	@echo "构建 db-probe..."
	@go build -o bin/db-probe ./cmd

# 本地运行（使用默认配置）
run: build
	@echo "运行 db-probe..."
	@./bin/db-probe -config configs/config.yaml

# 清理构建产物
clean:
	@echo "清理构建产物..."
	@rm -rf bin/

# 运行测试
test:
	@echo "运行测试..."
	@go test ./...

# 格式化代码
fmt:
	@echo "格式化代码..."
	@go fmt ./...

# 检查代码
vet:
	@echo "检查代码..."
	@go vet ./...

# 安装依赖
deps:
	@echo "安装依赖..."
	@go mod download
	@go mod tidy

