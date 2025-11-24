.PHONY: build run clean test

# 构建二进制文件
build:
	@echo "构建 db-probe..."
	@go build -o bin/db-probe ./cmd

# 本地运行（使用默认配置）
run: build
	@echo "运行 db-probe..."
	@./bin/db-probe

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

# Docker 相关命令
.PHONY: docker-build docker-up docker-down docker-logs docker-ps docker-restart

# 构建 Docker 镜像
docker-build:
	@echo "构建 Docker 镜像..."
	@docker-compose build

# 启动服务
docker-up:
	@echo "启动 db-probe 服务..."
	@docker-compose up -d

# 停止服务
docker-down:
	@echo "停止 db-probe 服务..."
	@docker-compose down

# 查看日志
docker-logs:
	@docker-compose logs -f db-probe

# 查看运行状态
docker-ps:
	@docker-compose ps

# 重启服务
docker-restart: docker-down docker-up

# 构建并启动
docker-build-up: docker-build docker-up

# Docker Hub 推送相关命令
.PHONY: docker-push docker-tag docker-login

# 登录 Docker Hub（需要先执行：docker login）
docker-login:
	@echo "请先执行: docker login"
	@echo "然后输入您的 Docker Hub 用户名和密码"

# 构建并标记镜像（用于推送到 Docker Hub）
docker-build-tag:
	@echo "构建并标记 Docker 镜像..."
	@if [ -z "$$DOCKER_HUB_USER" ]; then \
		echo "错误: 请设置环境变量 DOCKER_HUB_USER"; \
		echo "例如: export DOCKER_HUB_USER=yourusername"; \
		exit 1; \
	fi
	@VERSION=$${VERSION:-$$(git describe --tags --always 2>/dev/null || echo "dev")} \
	BUILD_TIME=$$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
	GIT_COMMIT=$$(git rev-parse --short HEAD 2>/dev/null || echo "unknown") \
	IMAGE_TAG=$${IMAGE_TAG:-latest} \
	DOCKER_HUB_USER=$$DOCKER_HUB_USER \
	docker-compose build
	@IMAGE_TAG=$${IMAGE_TAG:-latest}; \
	if [ "$$IMAGE_TAG" != "latest" ]; then \
		docker tag $$DOCKER_HUB_USER/db-probe:$$IMAGE_TAG $$DOCKER_HUB_USER/db-probe:latest; \
	fi

# 推送镜像到 Docker Hub
docker-push: docker-build-tag
	@echo "推送 Docker 镜像到 Docker Hub..."
	@if [ -z "$$DOCKER_HUB_USER" ]; then \
		echo "错误: 请设置环境变量 DOCKER_HUB_USER"; \
		echo "例如: export DOCKER_HUB_USER=yourusername"; \
		exit 1; \
	fi
	@IMAGE_TAG=$${IMAGE_TAG:-latest} \
	docker push $$DOCKER_HUB_USER/db-probe:$${IMAGE_TAG:-latest}
	@docker push $$DOCKER_HUB_USER/db-probe:latest
	@echo "推送完成！"
	@echo "镜像地址: https://hub.docker.com/r/$$DOCKER_HUB_USER/db-probe"

# 推送所有标签（包括 latest 和版本标签）
docker-push-all: docker-build-tag
	@echo "推送所有标签到 Docker Hub..."
	@if [ -z "$$DOCKER_HUB_USER" ]; then \
		echo "错误: 请设置环境变量 DOCKER_HUB_USER"; \
		echo "例如: export DOCKER_HUB_USER=yourusername"; \
		exit 1; \
	fi
	@VERSION=$${VERSION:-$$(git describe --tags --always 2>/dev/null || echo "dev")} \
	IMAGE_TAG=$${IMAGE_TAG:-latest} \
	docker push $$DOCKER_HUB_USER/db-probe:$${IMAGE_TAG:-latest}
	@docker push $$DOCKER_HUB_USER/db-probe:latest
	@if [ "$$VERSION" != "dev" ]; then \
		docker tag $$DOCKER_HUB_USER/db-probe:latest $$DOCKER_HUB_USER/db-probe:$$VERSION && \
		docker push $$DOCKER_HUB_USER/db-probe:$$VERSION; \
	fi
	@echo "推送完成！"
	@echo "镜像地址: https://hub.docker.com/r/$$DOCKER_HUB_USER/db-probe"

