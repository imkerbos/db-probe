// Package prober 实现数据库探测核心逻辑
// 负责管理多个数据库目标，周期性执行探测任务
// 探测过程包括：Ping 心跳检测和 SQL 查询执行
// 自动处理连接池管理、重连检测、错误处理和指标更新
package prober

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/imkerbos/db-probe/internal/config"
	"github.com/imkerbos/db-probe/internal/db"
	"github.com/imkerbos/db-probe/internal/metrics"
	"github.com/imkerbos/db-probe/pkg/logger"
	"github.com/prometheus/client_golang/prometheus"
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
	if dsn == "" {
		if dbCfg.Type == "oracle" {
			// Oracle DSN 格式: user/password@host:port/service_name
			serviceName := dbCfg.ServiceName
			if serviceName == "" {
				serviceName = "ORCL" // 默认 service name
			}
			dsn = fmt.Sprintf("%s/%s@%s:%d/%s",
				dbCfg.User,
				dbCfg.Password,
				dbCfg.Host,
				dbCfg.Port,
				serviceName,
			)
		} else {
			// MySQL/TiDB DSN 格式: user:password@tcp(host:port)/database?timeout=5s
			dsn = fmt.Sprintf("%s:%s@tcp(%s:%d)/?timeout=5s&readTimeout=5s&writeTimeout=5s",
				dbCfg.User,
				dbCfg.Password,
				dbCfg.Host,
				dbCfg.Port,
			)
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

	logger.L().Infow("数据库目标初始化成功",
		"db_name", dbCfg.Name,
		"db_type", dbCfg.Type,
		"db_host", dbCfg.Host,
		"db_ip", ip,
	)

	return target, nil
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

		up = false
		logger.L().Debugw("数据库 Ping 失败，连接可能已断开",
			"db_name", target.Config.Name,
			"db_type", target.Config.Type,
			"db_host", target.Config.Host,
			"error", err.Error(),
		)
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
			querySuccess = false
			up = false
			metrics.RecordQueryFailure(target.Labels) // 记录 SQL 查询失败次数
			metrics.RecordFailure(target.Labels)      // 记录总体失败次数
		} else {
			querySuccess = true
			up = true
		}

		metrics.UpdateQueryResult(target.Labels, querySuccess, queryDuration)
	}

	duration := time.Since(start).Seconds()

	// 更新 target 状态
	target.mu.Lock()
	target.LastError = err
	target.mu.Unlock()

	// 更新总体指标
	metrics.UpdateProbeResult(target.Labels, up, duration)

	// 记录日志
	if err != nil {
		logger.L().Warnw("数据库探测失败",
			"db_name", target.Config.Name,
			"db_type", target.Config.Type,
			"db_host", target.Config.Host,
			"db_ip", target.IP,
			"duration_seconds", duration,
			"error", err.Error(),
		)
	} else {
		logger.L().Debugw("数据库探测成功",
			"db_name", target.Config.Name,
			"db_type", target.Config.Type,
			"db_host", target.Config.Host,
			"db_ip", target.IP,
			"duration_seconds", duration,
		)
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
