package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/nzws/flux-encoder/internal/shared/logger"
	"github.com/nzws/flux-encoder/internal/worker/encoder"
	workergrpc "github.com/nzws/flux-encoder/internal/worker/grpc"
	"github.com/nzws/flux-encoder/internal/worker/uploader"
	workerv1 "github.com/nzws/flux-encoder/proto/worker/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

const version = "0.1.0"

func main() {
	// ロガー初期化
	isDev := os.Getenv("ENV") == "development"
	if err := logger.Init(isDev); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("Starting flux-encoder worker", zap.String("version", version))

	// 環境変数から設定を取得
	port := getEnvOrDefault("GRPC_PORT", "50051")
	maxConcurrent := getEnvInt("MAX_CONCURRENT_JOBS", 2)
	workDir := getEnvOrDefault("WORK_DIR", "/tmp/ffmpeg-jobs")
	storageType := getEnvOrDefault("STORAGE_TYPE", "s3")
	workerID := getEnvOrDefault("WORKER_ID", "worker-1")

	logger.Info("Worker configuration",
		zap.String("port", port),
		zap.Int("max_concurrent", maxConcurrent),
		zap.String("work_dir", workDir),
		zap.String("storage_type", storageType),
		zap.String("worker_id", workerID),
	)

	// 作業ディレクトリ作成
	if err := os.MkdirAll(workDir, 0755); err != nil {
		logger.Fatal("Failed to create work directory",
			zap.String("dir", workDir),
			zap.Error(err),
		)
	}

	// エンコーダー初期化
	enc := encoder.New(workDir)

	// アップローダー初期化
	ctx := context.Background()
	upl, err := uploader.NewUploader(ctx, storageType)
	if err != nil {
		logger.Fatal("Failed to create uploader",
			zap.String("storage_type", storageType),
			zap.Error(err),
		)
	}

	// gRPC サーバー作成
	grpcServer := grpc.NewServer()
	workerServer := workergrpc.NewServer(enc, upl, int32(maxConcurrent), workerID, version)
	workerServer.SetGRPCServer(grpcServer)

	workerv1.RegisterWorkerServiceServer(grpcServer, workerServer)

	// リフレクション有効化（開発用）
	if isDev {
		reflection.Register(grpcServer)
	}

	// リスナー作成
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		logger.Fatal("Failed to listen", zap.String("port", port), zap.Error(err))
	}

	// シグナルハンドリング
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info("Received shutdown signal, gracefully stopping...")
		grpcServer.GracefulStop()
	}()

	// サーバー起動
	logger.Info("Worker started", zap.String("addr", ":"+port))
	if err := grpcServer.Serve(lis); err != nil {
		logger.Fatal("Failed to serve", zap.Error(err))
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}
