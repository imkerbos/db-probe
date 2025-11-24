// Package prober 实现数据库探测核心逻辑
// 负责管理多个数据库目标，周期性执行探测任务
// 探测过程包括：Ping 心跳检测和 SQL 查询执行
// 自动处理连接池管理、重连检测、错误处理和指标更新
package prober

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/imkerbos/db-probe/internal/config"
	"github.com/imkerbos/db-probe/internal/db"
	"github.com/imkerbos/db-probe/internal/metrics"
	"github.com/imkerbos/db-probe/pkg/logger"
	"github.com/prometheus/client_golang/prometheus"
	go_ora "github.com/sijms/go-ora/v2"
)

// DBTarget 数据库探测目标
type DBTarget struct {
	Config       *config.DBConfig
	DB           *sql.DB
	Labels       prometheus.Labels
	IP           string
	LastError    error
	driver       db.ProberDriver
	query        string
	mu           sync.RWMutex
	lastPingTime time.Time // 上次 Ping 时间，用于检测重连
	lastUpStatus *bool     // 上次探测状态（nil 表示首次探测），用于检测状态变化
}

// Prober 探针管理器
type Prober struct {
	targets []*DBTarget
	config  *config.Config
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewProber 创建探针管理器
func NewProber(cfg *config.Config) (*Prober, error) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Prober{
		config: cfg,
		ctx:    ctx,
		cancel: cancel,
	}

	// 初始化所有 targets
	for _, dbCfg := range cfg.Databases {
		target, err := p.newTarget(&dbCfg)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("初始化数据库目标失败 [%s]: %w", dbCfg.Name, err)
		}
		p.targets = append(p.targets, target)
	}

	return p, nil
}

// newTarget 创建单个数据库目标
func (p *Prober) newTarget(dbCfg *config.DBConfig) (*DBTarget, error) {
	// 获取驱动
	driver, err := db.GetDriver(dbCfg.Type)
	if err != nil {
		return nil, err
	}

	// 解析 IP（支持 IP 地址和 DNS 域名）
	ip := dbCfg.Host
	if dbCfg.Host != "" {
		// 先检查是否是 IP 地址格式
		if parsedIP := net.ParseIP(dbCfg.Host); parsedIP != nil {
			// 如果 host 已经是 IP 地址，直接使用
			ip = parsedIP.String()
		} else {
			// 如果是 DNS 域名，进行解析
			ips, err := net.LookupIP(dbCfg.Host)
			if err == nil && len(ips) > 0 {
				// 优先使用 IPv4
				for _, resolvedIP := range ips {
					if resolvedIP.To4() != nil {
						ip = resolvedIP.String()
						break
					}
				}
				// 如果没有 IPv4，使用第一个 IP
				if ip == dbCfg.Host && len(ips) > 0 {
					ip = ips[0].String()
				}
			}
		}
	}

	// 构造 DSN
	dsn := dbCfg.DSN
	var serviceName string // Oracle 专用，用于后续日志记录
	if dsn == "" {
		if dbCfg.Type == "oracle" {
			// 根据 go-ora 文档，应该使用 go_ora.BuildUrl 函数来构建连接字符串
			// 参考：https://github.com/sijms/go-ora#simple-connection
			serviceName = dbCfg.ServiceName
			if serviceName == "" {
				serviceName = "ORCL" // 默认 service name
			}

			// 计算连接超时时间（秒），使用探测超时时间的 2 倍，确保有足够时间建立连接
			// 但不超过 10 秒，避免过长
			connectTimeout := int(p.config.ProbeTimeout.Seconds() * 2)
			if connectTimeout < 3 {
				connectTimeout = 3 // 最小 3 秒
			}
			if connectTimeout > 10 {
				connectTimeout = 10 // 最大 10 秒
			}

			// 使用 go_ora.BuildUrl 构建连接字符串
			// 格式：go_ora.BuildUrl(server, port, service_name, username, password, urlOptions)
			urlOptions := map[string]string{
				"CONNECT TIMEOUT": fmt.Sprintf("%d", connectTimeout),
			}
			dsn = go_ora.BuildUrl(dbCfg.Host, dbCfg.Port, serviceName, dbCfg.User, dbCfg.Password, urlOptions)
		} else {
			// MySQL/TiDB DSN 格式: user:password@tcp(host:port)/database?timeout=5s
			dsn = fmt.Sprintf("%s:%s@tcp(%s:%d)/?timeout=5s&readTimeout=5s&writeTimeout=5s",
				dbCfg.User,
				dbCfg.Password,
				dbCfg.Host,
				dbCfg.Port,
			)
		}
	} else if dbCfg.Type == "oracle" {
		// 如果提供了自定义 DSN，仍然需要 serviceName 用于日志
		serviceName = dbCfg.ServiceName
		if serviceName == "" {
			serviceName = "ORCL"
		}
	}

	// 打开数据库连接
	database, err := sql.Open(driver.DriverName(), dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库连接失败: %w", err)
	}

	// 设置连接池参数
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	// 连接最大生存时间：5分钟
	// 超过此时间的连接会被关闭，避免使用过期的连接
	// 这有助于防止数据库端断开连接后，客户端仍尝试复用已断开的连接
	database.SetConnMaxLifetime(time.Minute * 5)
	// 设置连接最大空闲时间：2分钟
	// 如果连接空闲超过此时间，会被关闭
	// 这有助于及时清理被数据库端断开的连接
	database.SetConnMaxIdleTime(time.Minute * 2)

	// 确定探测 SQL
	query := dbCfg.Query
	if query == "" {
		query = driver.DefaultQuery()
	}

	// 构造 labels
	labels := metrics.NewLabels(dbCfg, ip)

	// 设置 target info（静态信息）
	metrics.SetTargetInfo(labels)

	target := &DBTarget{
		Config: dbCfg,
		DB:     database,
		Labels: labels,
		IP:     ip,
		driver: driver,
		query:  query,
	}

	// 记录脱敏的 DSN（用于诊断）
	maskedDSN := dsn
	if dbCfg.Type == "oracle" {
		// 脱敏 Oracle DSN（使用 go_ora.BuildUrl 构建的格式）
		if dbCfg.Password != "" {
			// 构建脱敏的连接字符串用于日志显示
			connectTimeout := int(p.config.ProbeTimeout.Seconds() * 2)
			if connectTimeout < 3 {
				connectTimeout = 3
			}
			if connectTimeout > 10 {
				connectTimeout = 10
			}
			urlOptions := map[string]string{
				"CONNECT TIMEOUT": fmt.Sprintf("%d", connectTimeout),
			}
			maskedDSN = go_ora.BuildUrl(dbCfg.Host, dbCfg.Port, serviceName, dbCfg.User, "***", urlOptions)
		}
	} else {
		// 脱敏 MySQL DSN: user:***@tcp(host:port)/...
		if dbCfg.Password != "" {
			maskedDSN = fmt.Sprintf("%s:***@tcp(%s:%d)/?timeout=5s&readTimeout=5s&writeTimeout=5s",
				dbCfg.User, dbCfg.Host, dbCfg.Port)
		}
	}

	logFields := []interface{}{
		"db_name", dbCfg.Name,
		"db_type", dbCfg.Type,
		"db_host", dbCfg.Host,
		"db_port", dbCfg.Port,
		"db_ip", ip,
		"dsn", maskedDSN,
	}
	// 如果是 Oracle，添加 service_name 到日志
	if dbCfg.Type == "oracle" {
		logFields = append(logFields, "service_name", serviceName)
		// 如果 service_name 是默认值，记录警告
		if serviceName == "ORCL" && dbCfg.ServiceName == "" {
			logger.L().Warnw("Oracle service_name 使用默认值 ORCL，请确认配置是否正确",
				"db_name", dbCfg.Name,
				"config_service_name", dbCfg.ServiceName,
			)
		}
	}
	logger.L().Infow("数据库目标初始化成功", logFields...)

	return target, nil
}

// analyzeError 分析错误，返回错误阶段和详细描述
// 阶段包括：TCP连接、协议握手、认证、SQL执行
func analyzeError(err error, dbType string) (stage string, details string) {
	if err == nil {
		return "", ""
	}

	errMsg := err.Error()
	errMsgLower := strings.ToLower(errMsg)

	// 使用 errors.Unwrap 获取底层错误
	unwrapped := errors.Unwrap(err)
	var underlyingErrMsg string
	if unwrapped != nil {
		underlyingErrMsg = unwrapped.Error()
	}

	// 分析错误类型和阶段
	// 网络连接错误（TCP 层）
	if strings.Contains(errMsgLower, "connection refused") ||
		strings.Contains(errMsgLower, "no such host") ||
		strings.Contains(errMsgLower, "network is unreachable") ||
		strings.Contains(errMsgLower, "timeout") && strings.Contains(errMsgLower, "dial") {
		stage = "TCP连接"
		details = fmt.Sprintf("无法建立TCP连接: %s", errMsg)
		if underlyingErrMsg != "" && underlyingErrMsg != errMsg {
			details += fmt.Sprintf(" (底层错误: %s)", underlyingErrMsg)
		}
		return
	}

	// EOF 错误（通常是协议握手阶段）
	if strings.Contains(errMsgLower, "eof") || strings.Contains(errMsgLower, "end of file") {
		stage = "协议握手"
		details = fmt.Sprintf("协议握手失败 (EOF): %s", errMsg)
		if dbType == "oracle" {
			details += "。可能原因：1) service_name不正确 2) Oracle listener未启动 3) 网络中断 4) 超时时间过短"
		} else {
			details += "。可能原因：1) 数据库服务未启动 2) 网络中断 3) 超时时间过短"
		}
		if underlyingErrMsg != "" && underlyingErrMsg != errMsg {
			details += fmt.Sprintf(" (底层错误: %s)", underlyingErrMsg)
		}
		return
	}

	// 认证错误
	if strings.Contains(errMsgLower, "access denied") ||
		strings.Contains(errMsgLower, "invalid credentials") ||
		strings.Contains(errMsgLower, "authentication failed") ||
		strings.Contains(errMsgLower, "ora-01017") || // Oracle 认证错误
		strings.Contains(errMsgLower, "ora-1017") ||
		strings.Contains(errMsgLower, "1045") { // MySQL 认证错误
		stage = "认证"
		details = fmt.Sprintf("认证失败: %s", errMsg)
		if underlyingErrMsg != "" && underlyingErrMsg != errMsg {
			details += fmt.Sprintf(" (底层错误: %s)", underlyingErrMsg)
		}
		return
	}

	// SQL 执行错误
	if strings.Contains(errMsgLower, "sql") ||
		strings.Contains(errMsgLower, "syntax error") ||
		strings.Contains(errMsgLower, "table") ||
		strings.Contains(errMsgLower, "column") {
		stage = "SQL执行"
		details = fmt.Sprintf("SQL执行失败: %s", errMsg)
		if underlyingErrMsg != "" && underlyingErrMsg != errMsg {
			details += fmt.Sprintf(" (底层错误: %s)", underlyingErrMsg)
		}
		return
	}

	// Oracle 特定错误
	if dbType == "oracle" {
		// ORA-01013: user requested cancel of current operation
		// 这通常是因为超时导致的操作被取消
		if strings.Contains(errMsgLower, "ora-01013") || strings.Contains(errMsgLower, "ora-1013") ||
			strings.Contains(errMsgLower, "user requested cancel") {
			stage = "超时"
			details = fmt.Sprintf("操作超时被取消 (ORA-01013): %s", errMsg)
			details += "。可能原因：1) 超时时间过短 2) 网络延迟较高 3) 数据库响应慢。建议增加 probe_timeout 配置"
			if underlyingErrMsg != "" && underlyingErrMsg != errMsg {
				details += fmt.Sprintf(" (底层错误: %s)", underlyingErrMsg)
			}
			return
		}

		// ORA- 错误码（其他 Oracle 错误）
		if strings.Contains(errMsgLower, "ora-") {
			stage = "Oracle协议"
			details = fmt.Sprintf("Oracle协议错误: %s", errMsg)
			// 提取 ORA 错误码
			if idx := strings.Index(errMsgLower, "ora-"); idx != -1 {
				if endIdx := strings.Index(errMsgLower[idx:], " "); endIdx != -1 {
					oraCode := errMsgLower[idx : idx+endIdx]
					details += fmt.Sprintf(" (错误码: %s)", oraCode)
				} else {
					// 如果没有空格，尝试提取到行尾或特定字符
					if endIdx := strings.Index(errMsgLower[idx:], ":"); endIdx != -1 {
						oraCode := errMsgLower[idx : idx+endIdx]
						details += fmt.Sprintf(" (错误码: %s)", oraCode)
					}
				}
			}
			if underlyingErrMsg != "" && underlyingErrMsg != errMsg {
				details += fmt.Sprintf(" (底层错误: %s)", underlyingErrMsg)
			}
			return
		}
	}

	// MySQL 特定错误
	if dbType == "mysql" || dbType == "tidb" {
		// MySQL 错误码
		if strings.Contains(errMsgLower, "error") && (strings.Contains(errMsgLower, "1045") ||
			strings.Contains(errMsgLower, "2003") ||
			strings.Contains(errMsgLower, "2006")) {
			stage = "MySQL协议"
			details = fmt.Sprintf("MySQL协议错误: %s", errMsg)
			if underlyingErrMsg != "" && underlyingErrMsg != errMsg {
				details += fmt.Sprintf(" (底层错误: %s)", underlyingErrMsg)
			}
			return
		}
	}

	// 超时错误
	if strings.Contains(errMsgLower, "context deadline exceeded") ||
		strings.Contains(errMsgLower, "timeout") {
		stage = "超时"
		details = fmt.Sprintf("操作超时: %s", errMsg)
		if underlyingErrMsg != "" && underlyingErrMsg != errMsg {
			details += fmt.Sprintf(" (底层错误: %s)", underlyingErrMsg)
		}
		return
	}

	// 默认：未知错误
	stage = "未知阶段"
	details = fmt.Sprintf("未知错误: %s", errMsg)
	if underlyingErrMsg != "" && underlyingErrMsg != errMsg {
		details += fmt.Sprintf(" (底层错误: %s)", underlyingErrMsg)
	}
	return
}

// Start 启动所有探测任务
func (p *Prober) Start() {
	for _, target := range p.targets {
		p.wg.Add(1)
		go p.probeLoop(target)
	}
	logger.L().Infof("探针已启动，共 %d 个目标", len(p.targets))
}

// Stop 停止所有探测任务
func (p *Prober) Stop() {
	p.cancel()
	p.wg.Wait()

	// 关闭所有数据库连接
	for _, target := range p.targets {
		if target.DB != nil {
			target.DB.Close()
		}
	}

	logger.L().Info("探针已停止")
}

// probeLoop 单个目标的探测循环
func (p *Prober) probeLoop(target *DBTarget) {
	defer p.wg.Done()

	ticker := time.NewTicker(p.config.ProbeInterval)
	defer ticker.Stop()

	// 立即执行一次探测
	p.probeOnce(target)

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.probeOnce(target)
		}
	}
}

// probeOnce 执行一次探测
func (p *Prober) probeOnce(target *DBTarget) {
	start := time.Now()

	// 创建带超时的 context
	ctx, cancel := context.WithTimeout(p.ctx, p.config.ProbeTimeout)
	defer cancel()

	// 执行探测
	var up bool
	var err error
	var querySuccess bool

	// 检测是否发生重连（通过检查连接状态变化）
	target.mu.RLock()
	lastPingTime := target.lastPingTime
	target.mu.RUnlock()

	// 先 Ping（作为心跳检测，检查连接有效性）
	pingStart := time.Now()
	if err = target.DB.PingContext(ctx); err != nil {
		// Ping 失败，连接可能已断开
		pingDuration := time.Since(pingStart).Seconds()
		metrics.UpdatePingResult(target.Labels, false, pingDuration)
		metrics.RecordPingFailure(target.Labels) // 记录 Ping 失败次数
		metrics.RecordFailure(target.Labels)     // 记录总体失败次数

		// 如果之前有成功的 Ping，说明连接断开了，记录重连
		// 注意：database/sql 会在下次操作时自动重建连接
		// 我们通过检测 Ping 失败后，下次成功 Ping 的时间差来估算重连时间
		if !lastPingTime.IsZero() {
			// 标记需要记录重连（在下次成功时记录）
			// 这里先记录 Ping 失败，重连时间会在下次成功 Ping 时计算
		}

		// 保存原始错误类型和消息
		originalErr := err
		originalErrType := fmt.Sprintf("%T", originalErr)
		originalErrMsg := originalErr.Error()

		// 分析错误，确定失败阶段和详细描述
		// Ping 包含多个阶段：1) TCP连接 2) 协议握手 3) 认证 4) 连接到service_name
		failureStage, errorDetails := analyzeError(originalErr, target.Config.Type)

		// 增强错误信息，明确标注失败阶段
		errMsg := fmt.Sprintf("[%s阶段失败] %s (host=%s, port=%d, ip=%s, timeout=%v",
			failureStage, errorDetails, target.Config.Host, target.Config.Port, target.IP, p.config.ProbeTimeout)
		if target.Config.Type == "oracle" {
			serviceName := target.Config.ServiceName
			if serviceName == "" {
				serviceName = "ORCL"
			}
			errMsg += fmt.Sprintf(", service_name=%s", serviceName)
		}
		errMsg += ")"
		// 使用 %s 而不是直接使用变量作为格式字符串，避免 linter 警告
		err = fmt.Errorf("%s", errMsg)

		up = false
		logFields := []interface{}{
			"db_name", target.Config.Name,
			"db_type", target.Config.Type,
			"db_host", target.Config.Host,
			"db_port", target.Config.Port,
			"db_ip", target.IP,
			"failure_stage", failureStage, // 失败阶段
			"ping_duration_seconds", pingDuration,
			"timeout", p.config.ProbeTimeout,
			"error_type", originalErrType,
			"error", err.Error(),
			"error_details", errorDetails, // 详细错误描述
			"original_error", originalErrMsg,
		}
		if target.Config.Type == "oracle" {
			serviceName := target.Config.ServiceName
			if serviceName == "" {
				serviceName = "ORCL"
			}
			logFields = append(logFields, "service_name", serviceName)
		}
		logger.L().Debugw("数据库 Ping 失败", logFields...)
	} else {
		// Ping 成功
		pingDuration := time.Since(pingStart).Seconds()
		metrics.UpdatePingResult(target.Labels, true, pingDuration)

		// 检测重连：如果距离上次 Ping 时间很长，可能是重连
		now := time.Now()
		if !lastPingTime.IsZero() {
			timeSinceLastPing := now.Sub(lastPingTime)
			// 如果距离上次 Ping 超过探测间隔的 2 倍，可能是重连
			// 重连通常发生在连接断开后，需要重新建立连接
			// 我们通过 Ping 耗时来估算重连时间（如果 Ping 耗时明显增加，可能是重连）
			if timeSinceLastPing > p.config.ProbeInterval*2 && pingDuration > 0.05 {
				// 可能是重连，记录重连时间（使用 Ping 耗时作为估算）
				// 注意：这是估算值，实际重连时间可能包含在 Ping 耗时中
				metrics.RecordReconnect(target.Labels, pingDuration)
			}
		}

		// 更新连接信息
		target.mu.Lock()
		target.lastPingTime = now
		target.mu.Unlock()

		// Ping 成功，连接有效，执行探测 SQL
		queryStart := time.Now()
		var result int
		err = target.DB.QueryRowContext(ctx, target.query).Scan(&result)
		queryDuration := time.Since(queryStart).Seconds()

		if err != nil {
			// 保存原始错误类型和消息
			originalErr := err
			originalErrType := fmt.Sprintf("%T", originalErr)
			originalErrMsg := originalErr.Error()

			// 分析错误，确定失败阶段和详细描述
			// SQL 查询阶段可能失败的原因：SQL语法错误、权限不足、表不存在等
			failureStage, errorDetails := analyzeError(originalErr, target.Config.Type)
			if failureStage == "未知阶段" || failureStage == "" {
				failureStage = "SQL执行"
			}

			// 增强错误信息，明确标注失败阶段
			err = fmt.Errorf("[%s阶段失败] %s (query=%s, host=%s, port=%d, ip=%s, timeout=%v)",
				failureStage, errorDetails, target.query, target.Config.Host, target.Config.Port, target.IP, p.config.ProbeTimeout)

			querySuccess = false
			up = false
			metrics.RecordQueryFailure(target.Labels) // 记录 SQL 查询失败次数
			metrics.RecordFailure(target.Labels)      // 记录总体失败次数

			logger.L().Debugw("数据库 SQL 查询失败",
				"db_name", target.Config.Name,
				"db_type", target.Config.Type,
				"db_host", target.Config.Host,
				"db_port", target.Config.Port,
				"db_ip", target.IP,
				"query", target.query,
				"failure_stage", failureStage, // 失败阶段
				"query_duration_seconds", queryDuration,
				"timeout", p.config.ProbeTimeout,
				"error_type", originalErrType,
				"error", err.Error(),
				"error_details", errorDetails, // 详细错误描述
				"original_error", originalErrMsg,
			)
		} else {
			querySuccess = true
			up = true
		}

		metrics.UpdateQueryResult(target.Labels, querySuccess, queryDuration)
	}

	duration := time.Since(start).Seconds()

	// 更新 target 状态并检测状态变化
	target.mu.Lock()
	lastUpStatus := target.lastUpStatus
	statusChanged := false
	if lastUpStatus == nil {
		// 首次探测，记录状态
		statusChanged = true
	} else if *lastUpStatus != up {
		// 状态发生变化
		statusChanged = true
	}
	target.LastError = err
	if target.lastUpStatus == nil {
		target.lastUpStatus = new(bool)
	}
	*target.lastUpStatus = up
	target.mu.Unlock()

	// 更新总体指标
	metrics.UpdateProbeResult(target.Labels, up, duration)

	// 每次探测都记录日志，便于实时了解探测状态
	if err != nil {
		// 分析错误阶段（如果还没有分析过）
		failureStage, errorDetails := analyzeError(err, target.Config.Type)

		logFields := []interface{}{
			"db_name", target.Config.Name,
			"db_type", target.Config.Type,
			"db_host", target.Config.Host,
			"db_port", target.Config.Port,
			"db_ip", target.IP,
			"duration_seconds", duration,
			"sql", target.query,
			"error_type", fmt.Sprintf("%T", err),
			"error", err.Error(),
		}

		if failureStage != "" {
			logFields = append(logFields, "failure_stage", failureStage)
		}
		if errorDetails != "" {
			logFields = append(logFields, "error_details", errorDetails)
		}

		// 如果是状态变化，使用 Warn 级别；否则使用 Info 级别（避免重复刷屏）
		if statusChanged {
			logger.L().Warnw("数据库探测失败", logFields...)
		} else {
			logger.L().Infow("数据库探测失败", logFields...)
		}
	} else {
		logFields := []interface{}{
			"db_name", target.Config.Name,
			"db_type", target.Config.Type,
			"db_host", target.Config.Host,
			"db_port", target.Config.Port,
			"db_ip", target.IP,
			"duration_seconds", duration,
			"sql", target.query,
		}
		// 如果是 Oracle，添加 service_name
		if target.Config.Type == "oracle" {
			serviceName := target.Config.ServiceName
			if serviceName == "" {
				serviceName = "ORCL"
			}
			logFields = append(logFields, "service_name", serviceName)
		}

		// 成功时使用 Info 级别，每次探测都记录
		logger.L().Infow("数据库探测成功", logFields...)
	}
}

// GetTargets 获取所有目标（用于调试）
func (p *Prober) GetTargets() []*DBTarget {
	return p.targets
}

// TargetInfo 目标信息（用于 HTTP 接口）
type TargetInfo struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Host      string `json:"host"`
	IP        string `json:"ip"`
	LastError string `json:"last_error,omitempty"`
}

// GetTargetsInfo 获取所有目标信息（用于调试）
func (p *Prober) GetTargetsInfo() []TargetInfo {
	var infos []TargetInfo
	for _, target := range p.targets {
		target.mu.RLock()
		info := TargetInfo{
			Name: target.Config.Name,
			Type: target.Config.Type,
			Host: target.Config.Host,
			IP:   target.IP,
		}
		if target.LastError != nil {
			info.LastError = target.LastError.Error()
		}
		target.mu.RUnlock()
		infos = append(infos, info)
	}
	return infos
}
