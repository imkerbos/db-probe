# db-probe

数据库可用性探针 + Prometheus Exporter

## 功能特性

- 支持 MySQL/TiDB 和 Oracle 数据库探测
- 周期性执行轻量探测 SQL（`SELECT 1` / `SELECT 1 FROM dual`）
- 采集指标：
  - **基础指标**：可用性、总耗时、时间戳、目标信息
  - **Ping 指标**：Ping 状态和耗时（用于检测网络连接）
  - **SQL 查询指标**：查询状态和耗时（用于检测数据库功能）
  - **连接重连指标**：重连次数和耗时（用于检测连接稳定性）
- 通过 HTTP `/metrics` 暴露 Prometheus 指标
- 支持通过配置文件设置探测间隔、超时时间等参数

## 项目结构

```
db-probe/
├── cmd/
│   └── main.go              # 程序入口
├── internal/
│   ├── config/
│   │   └── config.go        # 配置加载 & 校验
│   ├── metrics/
│   │   └── metrics.go       # Prometheus 指标定义
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
├── Dockerfile               # Docker 构建文件
└── README.md
```

## 快速开始

### 1. 安装依赖

```bash
make deps
```

### 2. 配置数据库

编辑 `configs/config.yaml`，配置要探测的数据库：

```yaml
listen_address: ":9100"
probe_interval: 30s
probe_timeout: 5s

databases:
  - name: "mysql-test"
    type: "mysql"
    host: "localhost"
    port: 3306
    user: "root"
    password: "password"
    project: "project-a"  # 项目名称
    env: "prod"           # 环境标识
    labels:
      role: "master"
```

### 3. 构建和运行

```bash
# 构建
make build

# 运行
make run

# 或直接运行（固定从 configs/config.yaml 读取配置）
./bin/db-probe
```

### 4. 查看指标

访问 `http://localhost:9100/metrics` 查看 Prometheus 指标。

## 配置说明

### 主配置项

- `listen_address`: HTTP 服务监听地址（默认: `:9100`）
- `probe_interval`: 探测间隔（默认: `30s`）
- `probe_timeout`: 探测超时时间（默认: `5s`）

### 数据库配置

每个数据库实例可以配置不同的项目和环境：

- `name`: 数据库名称（必须唯一）
- `type`: 数据库类型（`mysql`、`tidb`、`oracle`）
- `host`: 数据库主机（支持 IP 地址和 DNS 域名）
- `port`: 数据库端口
- `user`: 用户名
- `password`: 密码
- `project`: 项目名称（用于 Prometheus label，每个实例独立配置）
- `env`: 环境标识（用于 Prometheus label，每个实例独立配置）
- `dsn`: 可选，自定义 DSN（如果提供则优先使用）
- `query`: 可选，自定义探测 SQL（默认：`SELECT 1` 或 `SELECT 1 FROM dual`）
- `labels`: 额外的 label 维度（如 `role`）

## Prometheus 指标

db-probe 暴露以下 Prometheus 指标，所有指标都包含统一的 label 维度。

### 基础指标

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `db_probe_up` | Gauge | 数据库可用性状态（1=可用，0=不可用） |
| `db_probe_duration_seconds` | Gauge | 总探测耗时（秒），包含 Ping + SQL 查询 + 连接建立时间 |
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
- `project`: 项目名称（从数据库配置中获取）
- `env`: 环境标识（从数据库配置中获取）
- `db_name`: 数据库名称
- `db_type`: 数据库类型（`mysql`、`tidb`、`oracle`）
- `db_host`: 数据库主机（配置的 host）
- `db_ip`: 解析后的 IP 地址
- `role`: 角色（从 labels 中提取，可选）

### 指标使用示例

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

**查看详细指标说明**：请参考 [指标参考文档](docs/metrics-reference.md)

## HTTP 端点

- `/metrics`: Prometheus 指标端点
- `/health`: 健康检查端点
- `/targets`: 目标列表（JSON 格式，用于调试）

## Docker 部署

```bash
# 构建镜像
docker build -t db-probe:latest .

# 运行容器
docker run -d \
  -p 9100:9100 \
  -v $(pwd)/configs/config.yaml:/app/configs/config.yaml \
  db-probe:latest
```

## 环境变量

支持通过环境变量覆盖配置（前缀 `DB_PROBE_`）：

```bash
export DB_PROBE_LISTEN_ADDRESS=":9100"
export DB_PROBE_PROBE_INTERVAL="30s"
export DB_PROBE_PROBE_TIMEOUT="5s"
```

**注意**：配置文件固定从 `configs/config.yaml` 读取，不支持命令行参数指定配置文件路径。

## 开发

```bash
# 格式化代码
make fmt

# 检查代码
make vet

# 运行测试
make test
```

## 许可证

MIT

