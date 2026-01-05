package balancer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nzws/flux-encoder/internal/shared/logger"
	workerv1 "github.com/nzws/flux-encoder/proto/worker/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Balancer は Worker の負荷分散を行う
type Balancer struct {
	workers         []string
	lastWorkerIndex int
	mutex           sync.Mutex
	timeout         time.Duration
}

// New は新しい Balancer を作成する
func New(workers []string, timeout time.Duration) *Balancer {
	return &Balancer{
		workers:         workers,
		lastWorkerIndex: -1,
		timeout:         timeout,
	}
}

// SelectWorker は空いている Worker を選択する
func (b *Balancer) SelectWorker(ctx context.Context) (string, *grpc.ClientConn, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	startIdx := (b.lastWorkerIndex + 1) % len(b.workers)

	for i := 0; i < len(b.workers); i++ {
		idx := (startIdx + i) % len(b.workers)
		worker := b.workers[idx]

		logger.Debug("Checking worker availability",
			zap.String("worker", worker),
			zap.Int("attempt", i+1),
		)

		// Worker に接続して状態を確認
		conn, status, err := b.getWorkerStatus(ctx, worker)
		if err != nil {
			logger.Warn("Failed to connect to worker",
				zap.String("worker", worker),
				zap.Error(err),
			)
			continue
		}

		// 空きがあるかチェック
		if status.CurrentJobs < status.MaxConcurrentJobs {
			b.lastWorkerIndex = idx
			logger.Info("Selected worker",
				zap.String("worker", worker),
				zap.Int32("current_jobs", status.CurrentJobs),
				zap.Int32("max_jobs", status.MaxConcurrentJobs),
			)
			return worker, conn, nil
		}

		// 空きがない場合は接続を閉じる
		if err := conn.Close(); err != nil {
			logger.Warn("Failed to close worker connection", zap.Error(err))
		}
	}

	return "", nil, fmt.Errorf("no available workers (all %d workers are busy)", len(b.workers))
}

// getWorkerStatus は Worker の状態を取得する
func (b *Balancer) getWorkerStatus(ctx context.Context, workerAddr string) (*grpc.ClientConn, *workerv1.WorkerStatus, error) {
	// タイムアウト付きコンテキスト
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	// Worker に接続
	conn, err := grpc.NewClient(
		workerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect: %w", err)
	}

	// 状態取得
	client := workerv1.NewWorkerServiceClient(conn)
	status, err := client.GetStatus(ctx, &workerv1.StatusRequest{})
	if err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			logger.Warn("Failed to close worker connection", zap.Error(closeErr))
		}
		return nil, nil, fmt.Errorf("failed to get status: %w", err)
	}

	return conn, status, nil
}
