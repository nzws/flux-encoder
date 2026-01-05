package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nzws/flux-encoder/internal/controlplane/balancer"
	"github.com/nzws/flux-encoder/internal/shared/logger"
	workerv1 "github.com/nzws/flux-encoder/proto/worker/v1"
	"go.uber.org/zap"
)

// Handler は REST API のハンドラー
type Handler struct {
	balancer   *balancer.Balancer
	jobManager *JobManager
}

// NewHandler は新しい Handler を作成する
func NewHandler(balancer *balancer.Balancer) *Handler {
	return &Handler{
		balancer:   balancer,
		jobManager: NewJobManager(),
	}
}

// JobRequest はジョブ作成のリクエスト
type JobRequest struct {
	InputURL string       `json:"input_url" binding:"required" example:"https://example.com/video.mp4"`
	Preset   string       `json:"preset" binding:"required" example:"720p_h264"`
	Output   OutputConfig `json:"output" binding:"required"`
}

// OutputConfig はアップロード先の設定
type OutputConfig struct {
	Storage  string            `json:"storage" binding:"required" example:"s3"`
	Path     string            `json:"path" binding:"required" example:"output/video.mp4"`
	Metadata map[string]string `json:"metadata" example:"key1:value1,key2:value2"`
}

// JobResponse はジョブ作成のレスポンス
type JobResponse struct {
	JobID     string `json:"job_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Status    string `json:"status" example:"accepted"`
	StreamURL string `json:"stream_url" example:"/api/v1/jobs/550e8400-e29b-41d4-a716-446655440000/stream"`
}

// ErrorResponse はエラーレスポンス
type ErrorResponse struct {
	Error string `json:"error" example:"error message"`
}

// CreateJob はジョブを作成する
// @Summary Create encoding job
// @Description Submit a new video encoding job to the Control Plane. The job will be distributed to an available Worker.
// @Tags jobs
// @Accept json
// @Produce json
// @Param job body JobRequest true "Job parameters"
// @Success 202 {object} JobResponse "Job accepted"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 503 {object} ErrorResponse "No available workers"
// @Security bearerAuth
// @Router /jobs [post]
func (h *Handler) CreateJob(c *gin.Context) {
	var req JobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// ジョブIDを生成
	jobID := uuid.New().String()

	logger.Info("Creating job",
		zap.String("job_id", jobID),
		zap.String("input_url", req.InputURL),
		zap.String("preset", req.Preset),
	)

	// Worker を選択
	_, conn, err := h.balancer.SelectWorker(c.Request.Context())
	if err != nil {
		logger.Error("Failed to select worker", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no available workers"})
		return
	}

	// 進捗チャネル作成
	progressCh := h.jobManager.CreateProgressChannel(jobID)

	// Worker にジョブを送信（ゴルーチンで非同期実行）
	go func() {
		defer func() {
			if err := conn.Close(); err != nil {
				logger.Warn("Failed to close worker connection", zap.Error(err))
			}
		}()
		defer h.jobManager.CloseProgressChannel(jobID)

		client := workerv1.NewWorkerServiceClient(conn)
		stream, err := client.SubmitJob(context.Background(), &workerv1.JobRequest{
			JobId:    jobID,
			InputUrl: req.InputURL,
			Preset:   req.Preset,
			Output: &workerv1.OutputConfig{
				Storage:  req.Output.Storage,
				Path:     req.Output.Path,
				Metadata: req.Output.Metadata,
			},
		})
		if err != nil {
			logger.Error("Failed to submit job", zap.Error(err))
			progressCh <- &workerv1.JobProgress{
				JobId:   jobID,
				Status:  workerv1.JobStatus_JOB_STATUS_FAILED,
				Message: "Failed to submit job",
				Error:   err.Error(),
			}
			return
		}

		// 進捗を受信してチャネルに送信
		for {
			progress, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				logger.Error("Failed to receive progress", zap.Error(err))
				progressCh <- &workerv1.JobProgress{
					JobId:   jobID,
					Status:  workerv1.JobStatus_JOB_STATUS_FAILED,
					Message: "Failed to receive progress",
					Error:   err.Error(),
				}
				return
			}
			progressCh <- progress
		}
	}()

	// ジョブ作成レスポンス
	c.JSON(http.StatusAccepted, gin.H{
		"job_id":     jobID,
		"status":     "accepted",
		"stream_url": fmt.Sprintf("/api/v1/jobs/%s/stream", jobID),
	})
}

// StreamJobProgress はジョブの進捗をSSEでストリーム
// @Summary Stream job progress
// @Description Get real-time job progress updates via Server-Sent Events (SSE)
// @Tags jobs
// @Produce text/event-stream
// @Param id path string true "Job ID"
// @Success 200 {string} string "SSE stream of job progress"
// @Failure 404 {object} ErrorResponse "Job not found"
// @Security bearerAuth
// @Router /jobs/{id}/stream [get]
func (h *Handler) StreamJobProgress(c *gin.Context) {
	jobID := c.Param("id")

	logger.Info("Streaming job progress", zap.String("job_id", jobID))

	// SSE ヘッダー設定
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // Nginxのバッファリング無効化

	// 進捗チャネル取得
	progressCh, exists := h.jobManager.GetProgressChannel(jobID)
	if !exists {
		logger.Warn("Job not found", zap.String("job_id", jobID))
		if _, err := fmt.Fprintf(c.Writer, "data: {\"error\":\"job not found\"}\n\n"); err != nil {
			logger.Warn("Failed to write SSE error", zap.Error(err))
		}
		c.Writer.Flush()
		return
	}

	// 進捗を SSE で送信
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		logger.Error("Streaming not supported")
		if _, err := fmt.Fprintf(c.Writer, "data: {\"error\":\"streaming not supported\"}\n\n"); err != nil {
			logger.Warn("Failed to write SSE error", zap.Error(err))
		}
		return
	}

	for {
		select {
		case progress, ok := <-progressCh:
			if !ok {
				// チャネルが閉じられた
				logger.Info("Progress channel closed", zap.String("job_id", jobID))
				return
			}

			// JSON形式で送信（安全にエスケープ）
			data := map[string]interface{}{
				"job_id":   progress.JobId,
				"status":   progress.Status.String(),
				"progress": progress.Progress,
				"message":  progress.Message,
			}
			if progress.OutputUrl != "" {
				data["output_url"] = progress.OutputUrl
			}
			if progress.Error != "" {
				data["error"] = progress.Error
			}

			jsonData, err := json.Marshal(data)
			if err != nil {
				logger.Error("Failed to marshal progress", zap.Error(err))
				continue
			}

			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData); err != nil {
				logger.Warn("Failed to write SSE progress", zap.Error(err))
				continue
			}
			flusher.Flush()

		case <-c.Request.Context().Done():
			// クライアント切断
			logger.Info("Client disconnected", zap.String("job_id", jobID))
			return
		}
	}
}

// WorkerStatusResponse はWorker状態のレスポンス
type WorkerStatusResponse struct {
	Message string `json:"message" example:"not implemented yet"`
}

// GetWorkerStatus はすべての Worker の状態を取得
// @Summary Get worker status
// @Description Get status of all registered Workers (not implemented yet)
// @Tags workers
// @Produce json
// @Success 200 {object} WorkerStatusResponse
// @Security bearerAuth
// @Router /workers/status [get]
func (h *Handler) GetWorkerStatus(c *gin.Context) {
	// 実装は省略（管理用APIとして将来実装）
	c.JSON(http.StatusOK, gin.H{"message": "not implemented yet"})
}
