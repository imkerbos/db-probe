# db-probe

数据库可用性探针 + Prometheus Exporter

支持监控 **MySQL**、**TiDB** 和 **Oracle** 数据库，通过周期性执行轻量级 SQL 查询来检测数据库可用性和延迟，并通过 Prometheus 指标暴露监控数据。

## 功能特性

- ✅ **多数据库支持**：MySQL、TiDB、Oracle
- ✅ **实时探测**：支持 2 秒间隔的实时监控
- ✅ **完整指标**：13 个 Prometheus 指标，覆盖可用性、延迟、失败统计等
- ✅ **细粒度监控**：Ping 和 SQL 查询分离，精确定位问题
- ✅ **连接管理**：自动连接池管理、重连检测
- ✅ **灵活配置**：支持 IP 地址和 DNS 域名，自定义 DSN 和查询
- ✅ **独立部署**：Docker 镜像包含所有依赖，开箱即用

## 项目结构

```
db-probe/
├── cmd/
│   └── main.go              # 程序入口
├── internal/
│   ├── config/
│   │   └── config.go        # 配置加载 & 校验
│   ├── metrics/
│   │   └── metrics.go        # Prometheus 指标定义
│   ├── db/
│   │   └── driver.go        # DB 类型抽象（mysql/tidb/oracle）
│   └── prober/
│       └── prober.go        # 探针核心逻辑
├── pkg/
│   └── logger/
│       └── logger.go        # zap 日志封装
├── configs/
│   └── config.yaml          # 配置文件
├── Makefile                 # 构建脚本
├── Dockerfile               # Docker 构建文件（包含 Oracle 支持）
└── README.md
```

## 快速开始

### 1. 使用 Docker（推荐）

```bash
# 构建镜像（包含 MySQL、TiDB、Oracle 支持）
docker build -t db-probe:latest .

# 运行容器
docker run -d \
  --name db-probe \
  -p 9100:9100 \
  -v $(pwd)/configs/config.yaml:/app/configs/config.yaml \
  db-probe:latest
```

### 2. 编译 Linux 二进制文件

```bash
# 使用 Docker 编译 Linux 版本（包含所有数据库支持）
make linux-build

# 会生成：bin/db-probe-linux-amd64
# 可以直接在 Linux 服务器上运行
```

### 3. 本地开发

```bash
# 安装依赖
make deps

# 构建
make build

# 运行
make run
```

## 配置说明

### 主配置项

编辑 `configs/config.yaml`：

```yaml
# 监听地址
listen_address: ":9100"

# 探测间隔（实时性要求：2秒，一般生产环境：5秒）
probe_interval: 2s

# 探测超时时间（推荐：探测间隔的 40%-60%，实时性场景推荐 1秒）
probe_timeout: 1s
```

### 数据库配置

每个数据库实例可以配置不同的项目和环境：

#### MySQL/TiDB 配置示例

```yaml
databases:
  - name: "mysql-prod"
    type: "mysql"              # 或 "tidb"
    host: "192.168.1.100"      # 支持 IP 地址和 DNS 域名
    port: 3306
    user: "monitor"
    password: "password"
    project: "production"       # 项目名称（用于 Prometheus label）
    env: "prod"                 # 环境标识（用于 Prometheus label）
    labels:
      role: "master"            # 可选的标签
```

#### Oracle 配置示例

```yaml
databases:
  - name: "oracle-prod"
    type: "oracle"
    host: "192.168.1.200"
    port: 1521
    user: "system"
    password: "password"
    service_name: "ORCLDB"      # Oracle 服务名（重要！）
    project: "production"
    env: "prod"
    labels:
      role: "primary"
```

### 配置字段说明

| 字段 | 必填 | 说明 |
|------|------|------|
| `name` | ✅ | 数据库名称（必须唯一） |
| `type` | ✅ | 数据库类型：`mysql`、`tidb`、`oracle` |
| `host` | ✅ | 数据库主机（支持 IP 地址和 DNS 域名） |
| `port` | ✅ | 数据库端口 |
| `user` | ✅ | 用户名 |
| `password` | ✅ | 密码 |
| `service_name` | ⚠️ | Oracle 专用：服务名称（默认 "ORCL"） |
| `project` | ✅ | 项目名称（用于 Prometheus label） |
| `env` | ✅ | 环境标识（用于 Prometheus label） |
| `dsn` | ❌ | 可选，自定义 DSN（如果提供则优先使用） |
| `query` | ❌ | 可选，自定义探测 SQL（默认：`SELECT 1` 或 `SELECT 1 FROM dual`） |
| `labels` | ❌ | 额外的 label 维度（如 `role`） |

## Prometheus 指标

db-probe 暴露 **13 个 Prometheus 指标**，所有指标都包含统一的 label 维度。

### 基础指标

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `db_probe_up` | Gauge | 数据库可用性状态（1=可用，0=不可用） |
| `db_probe_duration_seconds` | Gauge | 总探测耗时（秒） |
| `db_probe_last_timestamp` | Gauge | 最近探测时间戳（Unix 时间戳） |
| `db_probe_target_info` | Gauge | 目标信息（静态信息，固定为 1） |

### Ping 相关指标

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `db_probe_ping_up` | Gauge | Ping 操作状态（1=成功，0=失败） |
| `db_probe_ping_duration_seconds` | Gauge | Ping 操作耗时（秒） |

**用途**：检测网络连接问题，区分是网络问题还是数据库功能问题。

### SQL 查询相关指标

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `db_probe_query_up` | Gauge | SQL 查询状态（1=成功，0=失败） |
| `db_probe_query_duration_seconds` | Gauge | SQL 查询耗时（秒） |

**用途**：检测数据库功能问题，即使 Ping 成功，SQL 查询也可能失败（如权限问题、数据库只读等）。

### 连接重连相关指标

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `db_probe_connection_reconnects_total` | Counter | 连接重连总次数（累计值） |
| `db_probe_connection_reconnect_duration_seconds` | Gauge | 连接重连耗时（秒） |

**用途**：监控连接稳定性，识别频繁重连的数据库实例。

### 失败统计指标

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `db_probe_failures_total` | Counter | 探测失败总次数（累计值） |
| `db_probe_ping_failures_total` | Counter | Ping 失败总次数（累计值） |
| `db_probe_query_failures_total` | Counter | SQL 查询失败总次数（累计值） |

**用途**：统计失败次数，监控数据库稳定性，识别频繁失败的数据库实例。

### Label 维度

所有指标都包含以下 label：
- `project`: 项目名称
- `env`: 环境标识
- `db_name`: 数据库名称
- `db_type`: 数据库类型（`mysql`、`tidb`、`oracle`）
- `db_host`: 数据库主机（配置的 host）
- `db_ip`: 解析后的 IP 地址
- `role`: 角色（从 labels 中提取，可选）

### PromQL 查询示例

**检测数据库不可用**：
```promql
db_probe_up == 0
```

**检测网络连接问题**：
```promql
db_probe_ping_up == 0
```

**检测数据库功能问题**：
```promql
db_probe_ping_up == 1 AND db_probe_query_up == 0
```

**检测频繁重连**：
```promql
rate(db_probe_connection_reconnects_total[5m]) > 0.1
```

**检测频繁失败**：
```promql
rate(db_probe_failures_total[5m]) > 0.1
```

**统计失败趋势**：
```promql
increase(db_probe_failures_total[1h])
```

## HTTP 端点

- **`/metrics`**: Prometheus 指标端点
- **`/health`**: 健康检查端点（返回 `OK`）
- **`/targets`**: 目标列表（JSON 格式，用于调试）

## 编译和部署

### 使用 Docker 编译 Linux 二进制

```bash
# 编译 Linux 版本（包含 MySQL、TiDB、Oracle 支持）
make linux-build

# 会生成：bin/db-probe-linux-amd64
# 可以直接在 Linux 服务器上运行
```

### 使用 Docker 镜像

```bash
# 构建镜像
docker build -t db-probe:latest .

# 运行容器
docker run -d \
  --name db-probe \
  -p 9100:9100 \
  -v $(pwd)/configs/config.yaml:/app/configs/config.yaml \
  db-probe:latest
```

### 本地编译

```bash
# 安装依赖
make deps

# 构建
make build

# 运行
make run
```

## 环境变量

支持通过环境变量覆盖配置（前缀 `DB_PROBE_`）：

```bash
export DB_PROBE_LISTEN_ADDRESS=":9100"
export DB_PROBE_PROBE_INTERVAL="2s"
export DB_PROBE_PROBE_TIMEOUT="1s"
```

**注意**：配置文件固定从 `configs/config.yaml` 读取，不支持命令行参数指定配置文件路径。

## 性能建议

### 探测间隔和超时时间

**实时性要求高的场景**（推荐）：
- 探测间隔：`2s`
- 超时时间：`1s`（50% 的间隔）

**一般生产环境**：
- 探测间隔：`5s`
- 超时时间：`2s`（40% 的间隔）

**大规模生产环境**：
- 探测间隔：`10s`
- 超时时间：`3s`（30% 的间隔）

### 配置验证

程序启动时会自动验证配置：
- 超时时间必须小于探测间隔
- 建议超时时间为探测间隔的 40%-60%
- 如果配置不合理，会输出警告信息

## 开发

```bash
# 格式化代码
make fmt

# 检查代码
make vet

# 运行测试
make test

# 清理构建产物
make clean
```

## 常见问题

### Q1: Oracle 连接失败

**解决方案**：
- 检查服务名（`service_name`）是否正确
- 检查 IP 和端口是否正确
- 检查账号密码是否正确
- 确保网络可达

### Q2: 编译时 Oracle 驱动失败

**解决方案**：
- Oracle 驱动需要 CGO 和 Oracle Instant Client
- 使用 Docker 编译（已包含所有依赖）
- 或使用 `make linux-build` 命令

### Q3: 运行时找不到 Oracle 库

**解决方案**：
- Docker 镜像已包含 Oracle Instant Client
- 如果直接使用二进制文件，需要安装 Oracle Instant Client
- 设置 `LD_LIBRARY_PATH` 环境变量

## 许可证

MIT
