package grpc

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nzws/flux-encoder/internal/shared/logger"
	"github.com/nzws/flux-encoder/internal/worker/encoder"
	"github.com/nzws/flux-encoder/internal/worker/uploader"
	workerv1 "github.com/nzws/flux-encoder/proto/worker/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server は Worker の gRPC サーバー
type Server struct {
	workerv1.UnimplementedWorkerServiceServer

	encoder  *encoder.Encoder
	uploader uploader.Uploader

	activeJobs      int32
	maxConcurrent   int32
	activeJobsMutex sync.RWMutex
	activeJobIDs    map[string]context.CancelFunc

	grpcServer *grpc.Server
	workerID   string
	version    string
}

// NewServer は新しい gRPC サーバーを作成する
func NewServer(
	encoder *encoder.Encoder,
	uploader uploader.Uploader,
	maxConcurrent int32,
	workerID string,
	version string,
) *Server {
	return &Server{
		encoder:       encoder,
		uploader:      uploader,
		maxConcurrent: maxConcurrent,
		activeJobIDs:  make(map[string]context.CancelFunc),
		workerID:      workerID,
		version:       version,
	}
}

// SetGRPCServer は gRPC サーバーインスタンスをセットする
func (s *Server) SetGRPCServer(server *grpc.Server) {
	s.grpcServer = server
}

// SubmitJob はジョブを受け付けて処理する
func (s *Server) SubmitJob(req *workerv1.JobRequest, stream workerv1.WorkerService_SubmitJobServer) error {
	ctx := stream.Context()

	logger.Info("Received job",
		zap.String("job_id", req.JobId),
		zap.String("input_url", req.InputUrl),
		zap.String("preset", req.Preset),
	)

	// 同時実行数チェック
	current := atomic.LoadInt32(&s.activeJobs)
	if current >= s.maxConcurrent {
		return status.Errorf(codes.ResourceExhausted, "worker is at maximum capacity (%d/%d)", current, s.maxConcurrent)
	}

	// ジョブ開始
	atomic.AddInt32(&s.activeJobs, 1)

	// キャンセル可能なコンテキスト作成
	jobCtx, cancel := context.WithCancel(ctx)
	s.activeJobsMutex.Lock()
	s.activeJobIDs[req.JobId] = cancel
	s.activeJobsMutex.Unlock()

	defer func() {
		// ジョブ終了処理
		atomic.AddInt32(&s.activeJobs, -1)

		s.activeJobsMutex.Lock()
		delete(s.activeJobIDs, req.JobId)
		s.activeJobsMutex.Unlock()

		// クリーンアップ
		if err := s.encoder.Cleanup(req.JobId); err != nil {
			logger.Error("Failed to cleanup job",
				zap.String("job_id", req.JobId),
				zap.Error(err),
			)
		}

		// ジョブがなくなったら自動停止（環境変数で無効化可能）
		newCount := atomic.LoadInt32(&s.activeJobs)
		if newCount == 0 {
			disableAutoShutdown := os.Getenv("DISABLE_AUTO_SHUTDOWN")
			if disableAutoShutdown != "true" && disableAutoShutdown != "1" {
				go s.gracefulShutdown()
			} else {
				logger.Info("Auto shutdown is disabled (DISABLE_AUTO_SHUTDOWN is set)")
			}
		}
	}()

	// キュー状態を通知
	if err := stream.Send(&workerv1.JobProgress{
		JobId:     req.JobId,
		Status:    workerv1.JobStatus_JOB_STATUS_QUEUED,
		Progress:  0,
		Message:   "Job queued",
		Timestamp: time.Now().Format(time.RFC3339),
	}); err != nil {
		return err
	}

	// エンコード開始
	if err := stream.Send(&workerv1.JobProgress{
		JobId:     req.JobId,
		Status:    workerv1.JobStatus_JOB_STATUS_PROCESSING,
		Progress:  0,
		Message:   "Starting encoding",
		Timestamp: time.Now().Format(time.RFC3339),
	}); err != nil {
		return err
	}

	// エンコード実行
	outputPath, err := s.encoder.Encode(
		jobCtx,
		req.JobId,
		req.InputUrl,
		req.Preset,
		func(progress float32, message string) {
			// 進捗を通知（送信失敗時はエンコードをキャンセル）
			if sendErr := stream.Send(&workerv1.JobProgress{
				JobId:     req.JobId,
				Status:    workerv1.JobStatus_JOB_STATUS_PROCESSING,
				Progress:  progress,
				Message:   message,
				Timestamp: time.Now().Format(time.RFC3339),
			}); sendErr != nil {
				logger.Warn("Failed to send progress, cancelling job",
					zap.String("job_id", req.JobId),
					zap.Error(sendErr),
				)
				cancel()
			}
		},
	)

	if err != nil {
		logger.Error("Encoding failed",
			zap.String("job_id", req.JobId),
			zap.Error(err),
		)

		return stream.Send(&workerv1.JobProgress{
			JobId:     req.JobId,
			Status:    workerv1.JobStatus_JOB_STATUS_FAILED,
			Progress:  0,
			Message:   "Encoding failed",
			Error:     err.Error(),
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}

	// アップロード開始
	if err := stream.Send(&workerv1.JobProgress{
		JobId:     req.JobId,
		Status:    workerv1.JobStatus_JOB_STATUS_UPLOADING,
		Progress:  100,
		Message:   "Uploading output",
		Timestamp: time.Now().Format(time.RFC3339),
	}); err != nil {
		return err
	}

	// アップロード実行（ファイルまたはディレクトリ）
	var outputURL string
	fileInfo, err := os.Stat(outputPath)
	if err != nil {
		logger.Error("Failed to stat output path",
			zap.String("job_id", req.JobId),
			zap.String("path", outputPath),
			zap.Error(err),
		)

		return stream.Send(&workerv1.JobProgress{
			JobId:     req.JobId,
			Status:    workerv1.JobStatus_JOB_STATUS_FAILED,
			Progress:  100,
			Message:   "Failed to stat output path",
			Error:     err.Error(),
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}

	if fileInfo.IsDir() {
		// ディレクトリアップロード
		outputURL, err = s.uploader.UploadDirectory(jobCtx, outputPath, req.Output.Path)
	} else {
		// 単一ファイルアップロード
		outputURL, err = s.uploader.Upload(jobCtx, outputPath, req.Output.Path)
	}
	if err != nil {
		logger.Error("Upload failed",
			zap.String("job_id", req.JobId),
			zap.Error(err),
		)

		return stream.Send(&workerv1.JobProgress{
			JobId:     req.JobId,
			Status:    workerv1.JobStatus_JOB_STATUS_FAILED,
			Progress:  100,
			Message:   "Upload failed",
			Error:     err.Error(),
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}

	// 完了通知
	logger.Info("Job completed",
		zap.String("job_id", req.JobId),
		zap.String("output_url", outputURL),
	)

	return stream.Send(&workerv1.JobProgress{
		JobId:     req.JobId,
		Status:    workerv1.JobStatus_JOB_STATUS_COMPLETED,
		Progress:  100,
		Message:   "Job completed",
		OutputUrl: outputURL,
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

// GetStatus は Worker の現在の状態を返す
func (s *Server) GetStatus(ctx context.Context, req *workerv1.StatusRequest) (*workerv1.WorkerStatus, error) {
	s.activeJobsMutex.RLock()
	jobIDs := make([]string, 0, len(s.activeJobIDs))
	for id := range s.activeJobIDs {
		jobIDs = append(jobIDs, id)
	}
	s.activeJobsMutex.RUnlock()

	return &workerv1.WorkerStatus{
		CurrentJobs:       atomic.LoadInt32(&s.activeJobs),
		MaxConcurrentJobs: s.maxConcurrent,
		ActiveJobIds:      jobIDs,
		WorkerId:          s.workerID,
		Version:           s.version,
	}, nil
}

// CancelJob は実行中のジョブをキャンセルする
func (s *Server) CancelJob(ctx context.Context, req *workerv1.CancelRequest) (*workerv1.CancelResponse, error) {
	s.activeJobsMutex.RLock()
	cancel, exists := s.activeJobIDs[req.JobId]
	s.activeJobsMutex.RUnlock()

	if !exists {
		return &workerv1.CancelResponse{
			Success: false,
			Message: fmt.Sprintf("job not found: %s", req.JobId),
		}, nil
	}

	cancel()

	logger.Info("Job cancelled",
		zap.String("job_id", req.JobId),
	)

	return &workerv1.CancelResponse{
		Success: true,
		Message: "job cancelled",
	}, nil
}

// gracefulShutdown はジョブがなくなったときに自動停止する
func (s *Server) gracefulShutdown() {
	// 少し待機（新しいジョブが来る可能性）
	time.Sleep(1 * time.Second)

	// まだジョブがないことを確認
	if atomic.LoadInt32(&s.activeJobs) == 0 {
		logger.Info("No active jobs, shutting down worker...")

		if s.grpcServer != nil {
			s.grpcServer.GracefulStop()
		}

		os.Exit(0)
	}
}
