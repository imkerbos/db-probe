// Package logger 提供统一的日志记录功能
// 基于 zap 日志库，始终使用 JSON 格式输出，便于日志收集和分析
// 提供全局 logger 实例，支持结构化日志记录
package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	globalLogger *zap.Logger
	sugar        *zap.SugaredLogger
)

// InitLogger 初始化全局 logger（始终使用 JSON 格式输出）
func InitLogger() error {
	var err error
	config := zap.NewProductionConfig()

	// 确保使用 JSON 编码
	config.Encoding = "json"
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.EncoderConfig.LevelKey = "level"
	config.EncoderConfig.MessageKey = "message"
	config.EncoderConfig.CallerKey = "caller"
	config.EncoderConfig.StacktraceKey = "stacktrace"
	config.EncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder

	globalLogger, err = config.Build()
	if err != nil {
		return err
	}

	sugar = globalLogger.Sugar()
	return nil
}

// L 返回全局 SugaredLogger 实例
func L() *zap.SugaredLogger {
	if sugar == nil {
		// 如果未初始化，使用默认的 development logger
		logger, _ := zap.NewDevelopment()
		sugar = logger.Sugar()
	}
	return sugar
}

// Logger 返回全局 Logger 实例（非 Sugared）
func Logger() *zap.Logger {
	if globalLogger == nil {
		// 如果未初始化，使用默认的 development logger
		globalLogger, _ = zap.NewDevelopment()
	}
	return globalLogger
}

// Sync 同步日志缓冲区
func Sync() error {
	if globalLogger != nil {
		return globalLogger.Sync()
	}
	return nil
}
