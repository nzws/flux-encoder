package logger

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// Log はグローバルロガー
	Log *zap.Logger
)

// Init はロガーを初期化する
func Init(isDevelopment bool) error {
	var config zap.Config

	if isDevelopment {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		config = zap.NewProductionConfig()
	}

	// 環境変数からログレベルを取得
	if level := os.Getenv("LOG_LEVEL"); level != "" {
		var zapLevel zapcore.Level
		if err := zapLevel.UnmarshalText([]byte(level)); err == nil {
			config.Level = zap.NewAtomicLevelAt(zapLevel)
		}
	}

	logger, err := config.Build()
	if err != nil {
		return err
	}

	Log = logger
	return nil
}

// Sync はロガーをフラッシュする（defer で呼ぶ）
func Sync() {
	if Log != nil {
		if err := Log.Sync(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "logger sync failed:", err)
		}
	}
}

// Info は Info レベルのログを出力する
func Info(msg string, fields ...zap.Field) {
	if Log != nil {
		Log.Info(msg, fields...)
	}
}

// Error は Error レベルのログを出力する
func Error(msg string, fields ...zap.Field) {
	if Log != nil {
		Log.Error(msg, fields...)
	}
}

// Warn は Warn レベルのログを出力する
func Warn(msg string, fields ...zap.Field) {
	if Log != nil {
		Log.Warn(msg, fields...)
	}
}

// Debug は Debug レベルのログを出力する
func Debug(msg string, fields ...zap.Field) {
	if Log != nil {
		Log.Debug(msg, fields...)
	}
}

// Fatal は Fatal レベルのログを出力してプログラムを終了する
func Fatal(msg string, fields ...zap.Field) {
	if Log != nil {
		Log.Fatal(msg, fields...)
	}
}

// With は追加のフィールドを持つ新しいロガーを返す
func With(fields ...zap.Field) *zap.Logger {
	if Log != nil {
		return Log.With(fields...)
	}
	return zap.NewNop()
}
