// Package metrics 定义和注册所有 Prometheus 指标
// 提供 13 个指标用于监控数据库可用性、延迟、失败统计等
// 所有指标都包含统一的 label 维度：project、env、db_name、db_type、db_host、db_ip、role
// 提供便捷的更新函数来更新指标值
package metrics

import (
	"time"

	"github.com/imkerbos/db-probe/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// DBProbeUp 数据库可用性指标 (1=可用, 0=不可用)
	DBProbeUp *prometheus.GaugeVec

	// DBProbeDurationSeconds 探测耗时（秒）
	DBProbeDurationSeconds *prometheus.GaugeVec

	// DBProbeLastTimestamp 最近探测时间戳
	DBProbeLastTimestamp *prometheus.GaugeVec

	// DBProbeTargetInfo 目标信息（静态信息）
	DBProbeTargetInfo *prometheus.GaugeVec

	// DBProbePingUp Ping 操作状态 (1=成功, 0=失败)
	DBProbePingUp *prometheus.GaugeVec

	// DBProbePingDurationSeconds Ping 操作耗时（秒）
	DBProbePingDurationSeconds *prometheus.GaugeVec

	// DBProbeQueryUp SQL 查询状态 (1=成功, 0=失败)
	DBProbeQueryUp *prometheus.GaugeVec

	// DBProbeQueryDurationSeconds SQL 查询耗时（秒）
	DBProbeQueryDurationSeconds *prometheus.GaugeVec

	// DBProbeConnectionReconnectsTotal 连接重连总次数（Counter）
	DBProbeConnectionReconnectsTotal *prometheus.CounterVec

	// DBProbeConnectionReconnectDurationSeconds 连接重连耗时（秒）
	DBProbeConnectionReconnectDurationSeconds *prometheus.GaugeVec

	// DBProbeFailuresTotal 探测失败总次数（Counter）
	DBProbeFailuresTotal *prometheus.CounterVec

	// DBProbePingFailuresTotal Ping 失败总次数（Counter）
	DBProbePingFailuresTotal *prometheus.CounterVec

	// DBProbeQueryFailuresTotal SQL 查询失败总次数（Counter）
	DBProbeQueryFailuresTotal *prometheus.CounterVec
)

func init() {
	// 统一的 label 维度
	labelNames := []string{
		"project",
		"env",
		"db_name",
		"db_type",
		"db_host",
		"db_ip",
		"role",
	}

	DBProbeUp = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_probe_up",
			Help: "Database availability status (1=up, 0=down)",
		},
		labelNames,
	)

	DBProbeDurationSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_probe_duration_seconds",
			Help: "Database probe duration in seconds",
		},
		labelNames,
	)

	DBProbeLastTimestamp = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_probe_last_timestamp",
			Help: "Last probe timestamp (Unix timestamp)",
		},
		labelNames,
	)

	DBProbeTargetInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_probe_target_info",
			Help: "Database target information (static labels)",
		},
		labelNames,
	)

	DBProbePingUp = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_probe_ping_up",
			Help: "Database ping status (1=success, 0=failure)",
		},
		labelNames,
	)

	DBProbePingDurationSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_probe_ping_duration_seconds",
			Help: "Database ping duration in seconds",
		},
		labelNames,
	)

	DBProbeQueryUp = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_probe_query_up",
			Help: "Database query execution status (1=success, 0=failure)",
		},
		labelNames,
	)

	DBProbeQueryDurationSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_probe_query_duration_seconds",
			Help: "Database query execution duration in seconds",
		},
		labelNames,
	)

	DBProbeConnectionReconnectsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_probe_connection_reconnects_total",
			Help: "Total number of database connection reconnects",
		},
		labelNames,
	)

	DBProbeConnectionReconnectDurationSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_probe_connection_reconnect_duration_seconds",
			Help: "Database connection reconnect duration in seconds",
		},
		labelNames,
	)

	DBProbeFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_probe_failures_total",
			Help: "Total number of database probe failures",
		},
		labelNames,
	)

	DBProbePingFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_probe_ping_failures_total",
			Help: "Total number of database ping failures",
		},
		labelNames,
	)

	DBProbeQueryFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_probe_query_failures_total",
			Help: "Total number of database query failures",
		},
		labelNames,
	)
}

// NewLabels 构造 Prometheus labels
func NewLabels(dbCfg *config.DBConfig, ip string) prometheus.Labels {
	labels := prometheus.Labels{
		"project": dbCfg.Project,
		"env":     dbCfg.Env,
		"db_name": dbCfg.Name,
		"db_type": dbCfg.Type,
		"db_host": dbCfg.Host,
		"db_ip":   ip,
		"role":    "",
	}

	// 从 dbCfg.Labels 中提取 role（如果存在）
	if role, ok := dbCfg.Labels["role"]; ok {
		labels["role"] = role
	}

	// 合并其他自定义 labels（但只保留在 labelNames 中的）
	// 注意：Prometheus labels 必须在注册时定义，所以这里只处理 role
	// 其他自定义 labels 可以通过 target_info 的 value 来存储（如果需要）

	return labels
}

// UpdateProbeResult 更新探测结果
func UpdateProbeResult(labels prometheus.Labels, up bool, durationSeconds float64) {
	timestamp := float64(time.Now().Unix())

	DBProbeUp.With(labels).Set(boolToFloat64(up))
	DBProbeDurationSeconds.With(labels).Set(durationSeconds)
	DBProbeLastTimestamp.With(labels).Set(timestamp)
}

// UpdatePingResult 更新 Ping 操作结果
func UpdatePingResult(labels prometheus.Labels, success bool, durationSeconds float64) {
	DBProbePingUp.With(labels).Set(boolToFloat64(success))
	DBProbePingDurationSeconds.With(labels).Set(durationSeconds)
}

// UpdateQueryResult 更新 SQL 查询结果
func UpdateQueryResult(labels prometheus.Labels, success bool, durationSeconds float64) {
	DBProbeQueryUp.With(labels).Set(boolToFloat64(success))
	DBProbeQueryDurationSeconds.With(labels).Set(durationSeconds)
}

// RecordReconnect 记录连接重连
func RecordReconnect(labels prometheus.Labels, durationSeconds float64) {
	DBProbeConnectionReconnectsTotal.With(labels).Inc()
	DBProbeConnectionReconnectDurationSeconds.With(labels).Set(durationSeconds)
}

// RecordFailure 记录探测失败
func RecordFailure(labels prometheus.Labels) {
	DBProbeFailuresTotal.With(labels).Inc()
}

// RecordPingFailure 记录 Ping 失败
func RecordPingFailure(labels prometheus.Labels) {
	DBProbePingFailuresTotal.With(labels).Inc()
}

// RecordQueryFailure 记录 SQL 查询失败
func RecordQueryFailure(labels prometheus.Labels) {
	DBProbeQueryFailuresTotal.With(labels).Inc()
}

// SetTargetInfo 设置目标信息（静态信息，只需设置一次）
func SetTargetInfo(labels prometheus.Labels) {
	DBProbeTargetInfo.With(labels).Set(1)

	// 初始化 Counter 类型指标，确保即使值为 0 也会显示
	// Counter 类型需要通过 Add(0) 来初始化，这样即使值为 0 也会在 /metrics 中显示
	DBProbeFailuresTotal.With(labels).Add(0)
	DBProbePingFailuresTotal.With(labels).Add(0)
	DBProbeQueryFailuresTotal.With(labels).Add(0)
	DBProbeConnectionReconnectsTotal.With(labels).Add(0)
}

func boolToFloat64(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
