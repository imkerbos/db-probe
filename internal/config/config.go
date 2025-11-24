// Package config 提供配置管理功能
// 支持从 YAML 配置文件加载配置，并支持环境变量覆盖
// 配置包括监听地址、探测间隔、超时时间以及数据库实例列表
package config

import (
	"fmt"
	"time"

	"github.com/imkerbos/db-probe/pkg/logger"
	"github.com/spf13/viper"
)

// Config 主配置结构
type Config struct {
	ListenAddress string        `mapstructure:"listen_address"`
	ProbeInterval time.Duration `mapstructure:"probe_interval"`
	ProbeTimeout  time.Duration `mapstructure:"probe_timeout"`
	Databases     []DBConfig    `mapstructure:"databases"`
}

// DBConfig 数据库配置
type DBConfig struct {
	Name        string            `mapstructure:"name"`
	Type        string            `mapstructure:"type"` // mysql, tidb, oracle
	Host        string            `mapstructure:"host"`
	Port        int               `mapstructure:"port"`
	User        string            `mapstructure:"user"`
	Password    string            `mapstructure:"password"`
	DSN         string            `mapstructure:"dsn"`          // 可选，如果提供则优先使用
	Query       string            `mapstructure:"query"`        // 可选，自定义探测 SQL
	ServiceName string            `mapstructure:"service_name"` // Oracle 专用：服务名称（默认 "ORCL"）
	Project     string            `mapstructure:"project"`      // 项目名称
	Env         string            `mapstructure:"env"`          // 环境标识
	Labels      map[string]string `mapstructure:"labels"`       // 额外的 label 维度
}

var (
	globalConfig *Config
)

// Load 加载配置（固定从 configs/config.yaml 读取）
func Load() (*Config, error) {
	configPath := "configs/config.yaml"

	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")

	// 支持环境变量覆盖（前缀 DB_PROBE_）
	viper.SetEnvPrefix("DB_PROBE")
	viper.AutomaticEnv()

	// 读取配置文件
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 校验配置
	if err := Validate(&cfg); err != nil {
		return nil, err
	}

	globalConfig = &cfg
	logger.L().Infof("配置加载成功: %s", viper.ConfigFileUsed())
	return &cfg, nil
}

// Validate 校验配置
func Validate(cfg *Config) error {
	// 校验探测间隔和超时时间
	if cfg.ProbeInterval <= 0 {
		return fmt.Errorf("probe_interval 必须大于 0")
	}
	if cfg.ProbeTimeout <= 0 {
		return fmt.Errorf("probe_timeout 必须大于 0")
	}
	// 超时时间不应该超过探测间隔，避免连接被占用影响下一次探测
	// 允许 timeout 等于 interval（100%），但超过则报错
	if cfg.ProbeTimeout > cfg.ProbeInterval {
		return fmt.Errorf("probe_timeout (%v) 不应超过 probe_interval (%v)，建议超时时间为探测间隔的 40%%-60%%", cfg.ProbeTimeout, cfg.ProbeInterval)
	}
	// 对于实时性要求高的场景（2秒间隔），建议超时时间为 800ms-1.2s
	// 这样可以覆盖正常延迟（20-400ms）和轻微网络延迟（200-500ms）
	// 同时避免超时时间过长影响下一次探测
	recommendedTimeout := cfg.ProbeInterval / 2 // 推荐：50%的间隔
	minTimeout := cfg.ProbeInterval / 3         // 最小：33%的间隔（约 667ms for 2s）
	maxTimeout := cfg.ProbeInterval * 3 / 5     // 最大：60%的间隔（约 1.2s for 2s）

	if cfg.ProbeTimeout < minTimeout {
		logger.L().Warnw("probe_timeout 过短，可能导致正常网络延迟也被判定为超时",
			"probe_timeout", cfg.ProbeTimeout,
			"probe_interval", cfg.ProbeInterval,
			"recommended_timeout", recommendedTimeout,
			"min_timeout", minTimeout,
		)
	} else if cfg.ProbeTimeout > maxTimeout {
		// 如果 timeout 超过推荐的最大值（60%），但不超过 interval，给出警告
		if cfg.ProbeTimeout <= cfg.ProbeInterval {
			logger.L().Warnw("probe_timeout 过长，可能影响下一次探测的及时性，建议设置为探测间隔的 40%%-60%%",
				"probe_timeout", cfg.ProbeTimeout,
				"probe_interval", cfg.ProbeInterval,
				"recommended_timeout", recommendedTimeout,
				"max_timeout", maxTimeout,
			)
		}
	}

	if len(cfg.Databases) == 0 {
		return fmt.Errorf("配置项 databases 不能为空")
	}

	// 检查数据库名称唯一性
	nameMap := make(map[string]bool)
	for i, db := range cfg.Databases {
		if db.Name == "" {
			return fmt.Errorf("databases[%d].name 不能为空", i)
		}
		if nameMap[db.Name] {
			return fmt.Errorf("数据库名称重复: %s", db.Name)
		}
		nameMap[db.Name] = true

		// 校验项目和环境
		if db.Project == "" {
			return fmt.Errorf("databases[%d].project 不能为空", i)
		}
		if db.Env == "" {
			return fmt.Errorf("databases[%d].env 不能为空", i)
		}

		// 校验数据库类型
		validTypes := map[string]bool{
			"mysql":  true,
			"tidb":   true,
			"oracle": true,
		}
		if !validTypes[db.Type] {
			return fmt.Errorf("databases[%d].type 必须是 mysql、tidb 或 oracle，当前值: %s", i, db.Type)
		}

		// 如果 DSN 为空，则必须提供 host、port、user、password
		if db.DSN == "" {
			if db.Host == "" {
				return fmt.Errorf("databases[%d].host 不能为空（当 dsn 未提供时）", i)
			}
			if db.Port == 0 {
				return fmt.Errorf("databases[%d].port 不能为空（当 dsn 未提供时）", i)
			}
			if db.User == "" {
				return fmt.Errorf("databases[%d].user 不能为空（当 dsn 未提供时）", i)
			}
			if db.Password == "" {
				return fmt.Errorf("databases[%d].password 不能为空（当 dsn 未提供时）", i)
			}
		}
	}

	return nil
}

// Get 获取全局配置
func Get() *Config {
	return globalConfig
}
