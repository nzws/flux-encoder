package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nzws/flux-encoder/internal/controlplane/api"
	"github.com/nzws/flux-encoder/internal/controlplane/auth"
	"github.com/nzws/flux-encoder/internal/controlplane/balancer"
	"github.com/nzws/flux-encoder/internal/shared/logger"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"

	_ "github.com/nzws/flux-encoder/docs"
)

const version = "0.1.0"

// @title Flux Encoder API
// @version 0.1.0

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.bearerAuth bearerAuth
// @in header
// @name Authorization
func main() {
	// ロガー初期化
	isDev := os.Getenv("ENV") == "development"
	if err := logger.Init(isDev); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("Starting flux-encoder control plane", zap.String("version", version))

	// 環境変数から設定を取得
	port := getEnvOrDefault("PORT", "8080")
	workerNodesStr := os.Getenv("WORKER_NODES")
	if workerNodesStr == "" {
		logger.Fatal("WORKER_NODES environment variable is required")
	}

	workerNodes := strings.Split(workerNodesStr, ",")
	for i := range workerNodes {
		workerNodes[i] = strings.TrimSpace(workerNodes[i])
	}

	workerTimeout := time.Duration(getEnvInt("WORKER_STARTUP_TIMEOUT", 60)) * time.Second

	logger.Info("Control plane configuration",
		zap.String("port", port),
		zap.Strings("workers", workerNodes),
		zap.Duration("worker_timeout", workerTimeout),
	)

	// Balancer 作成
	bal := balancer.New(workerNodes, workerTimeout)

	// API ハンドラー作成
	handler := api.NewHandler(bal)

	// Gin セットアップ
	if !isDev {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// 認証ミドルウェアを適用
	r.Use(auth.APIKeyMiddleware())

	// ルート設定
	v1 := r.Group("/api/v1")
	{
		v1.POST("/jobs", handler.CreateJob)
		v1.GET("/jobs/:id/stream", handler.StreamJobProgress)
		v1.GET("/workers/status", handler.GetWorkerStatus)
	}

	// ヘルスチェック
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "version": version})
	})

	// Prometheusメトリクス
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Swagger UI
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// サーバー起動
	logger.Info("Control plane started", zap.String("addr", ":"+port))
	if err := r.Run(":" + port); err != nil {
		logger.Fatal("Failed to start server", zap.Error(err))
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
		var i int
		if _, err := fmt.Sscanf(value, "%d", &i); err == nil {
			return i
		}
	}
	return defaultValue
}
