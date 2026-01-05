package retry

import (
	"context"
	"fmt"
	"time"

	"github.com/nzws/flux-encoder/internal/shared/logger"
	"go.uber.org/zap"
)

// Config はリトライの設定
type Config struct {
	MaxAttempts int           // 最大試行回数
	InitialWait time.Duration // 初回待機時間
	MaxWait     time.Duration // 最大待機時間
	Multiplier  float64       // 待機時間の倍率
}

// DefaultConfig はデフォルトのリトライ設定
var DefaultConfig = Config{
	MaxAttempts: 3,
	InitialWait: 1 * time.Second,
	MaxWait:     30 * time.Second,
	Multiplier:  2.0,
}

// Do はexponential backoffでリトライを実行する
func Do(ctx context.Context, config Config, fn func() error) error {
	var lastErr error
	wait := config.InitialWait

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// 最後の試行ならリトライしない
		if attempt == config.MaxAttempts {
			break
		}

		logger.Warn("Operation failed, retrying",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", config.MaxAttempts),
			zap.Duration("wait", wait),
			zap.Error(err),
		)

		// コンテキストキャンセルチェック
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(wait):
		}

		// 次回の待機時間を計算（exponential backoff）
		wait = time.Duration(float64(wait) * config.Multiplier)
		if wait > config.MaxWait {
			wait = config.MaxWait
		}
	}

	return fmt.Errorf("max retry attempts reached (%d): %w", config.MaxAttempts, lastErr)
}
