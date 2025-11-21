// Package main 是 db-probe 程序的入口点
// db-probe 是一个数据库可用性探针，支持监控 MySQL、TiDB 和 Oracle 数据库
// 通过周期性执行轻量级 SQL 查询来检测数据库可用性和延迟
// 并通过 Prometheus 指标暴露监控数据
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/go-sql-driver/mysql" // MySQL/TiDB 驱动
	_ "github.com/godror/godror"       // Oracle 驱动

	"github.com/imkerbos/db-probe/internal/config"
	"github.com/imkerbos/db-probe/internal/prober"
	"github.com/imkerbos/db-probe/pkg/logger"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// 初始化 logger（JSON 格式输出）
	if err := logger.InitLogger(); err != nil {
		panic(fmt.Sprintf("初始化 logger 失败: %v", err))
	}
	defer logger.Sync()

	// 加载配置（固定从 configs/config.yaml 读取）
	cfg, err := config.Load()
	if err != nil {
		logger.L().Fatalw("加载配置失败", "error", err)
	}

	logger.L().Infow("配置加载成功",
		"listen_address", cfg.ListenAddress,
		"probe_interval", cfg.ProbeInterval,
		"probe_timeout", cfg.ProbeTimeout,
		"databases_count", len(cfg.Databases),
	)

	// 初始化探针
	probe, err := prober.NewProber(cfg)
	if err != nil {
		logger.L().Fatalw("初始化探针失败", "error", err)
	}

	// 启动探针
	probe.Start()
	defer probe.Stop()

	// 设置 HTTP 路由
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/targets", func(w http.ResponseWriter, r *http.Request) {
		targetsHandler(w, r, probe)
	})
	http.Handle("/metrics", promhttp.Handler())

	// 启动 HTTP 服务器
	server := &http.Server{
		Addr:    cfg.ListenAddress,
		Handler: nil,
	}

	go func() {
		logger.L().Infow("HTTP 服务器启动",
			"listen_address", cfg.ListenAddress,
			"metrics_endpoint", "/metrics",
			"health_endpoint", "/health",
			"targets_endpoint", "/targets",
		)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.L().Fatalw("HTTP 服务器启动失败", "error", err)
		}
	}()

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.L().Info("收到停止信号，正在关闭...")
}

// healthHandler 处理健康检查请求
// 返回 HTTP 200 状态码和 "OK" 文本，用于 Kubernetes/Docker 健康检查
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// targetsHandler 处理目标信息查询请求
// 返回所有数据库目标的详细信息（名称、类型、主机、IP、最后错误等）
// 以 JSON 格式返回，用于调试和监控
func targetsHandler(w http.ResponseWriter, r *http.Request, probe *prober.Prober) {
	infos := probe.GetTargetsInfo()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(infos)
}
